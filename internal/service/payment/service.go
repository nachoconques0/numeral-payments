// Package payment holds the payment business logic: the idempotency rule and
// the order in which the database and the bank folder are written.
package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"numeral-payments/internal/bank"
	paymentEntity "numeral-payments/internal/entity/payment"
	apperrors "numeral-payments/internal/errors"
)

// Repository persists payments.
type Repository interface {
	FindByIdempotencyKey(ctx context.Context, key string) (*paymentEntity.Payment, error)
	Insert(ctx context.Context, p *paymentEntity.Payment) error
	UpdateStatus(ctx context.Context, key string, status paymentEntity.Status, updatedAt time.Time) (int64, error)
}

// BankAdapter is the port to a bank: how a payment is deposited, and where that
// bank's responses appear and how to read them. One implementation per bank.
type BankAdapter interface {
	Name() string
	Deposit(ctx context.Context, p *paymentEntity.Payment) error
	ResponsePattern() string
	ParseResponse(data []byte) ([]bank.Response, error)
}

// Service coordinates payment creation and bank responses.
type Service struct {
	repo Repository
	bank BankAdapter
}

// NewService returns a service writing through repo to bank.
func NewService(repo Repository, bank BankAdapter) *Service {
	return &Service{repo: repo, bank: bank}
}

// CreatePayment stores a payment as PENDING, then deposits it with the bank. The
// row is committed first, so the file follows the record and not the reverse.
func (s *Service) CreatePayment(ctx context.Context, in paymentEntity.Input) (*paymentEntity.Payment, error) {
	p, err := paymentEntity.New(in, time.Now().UTC())
	if err != nil {
		return nil, apperrors.BadRequest("invalid payment", err)
	}

	existing, err := s.findExisting(ctx, p.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return resolveReplay(p, existing)
	}

	if err := s.repo.Insert(ctx, p); err != nil {
		if errors.Is(err, paymentEntity.ErrDuplicateIdempotencyKey) {
			return s.replay(ctx, p)
		}
		return nil, apperrors.InternalError("could not store the payment", err)
	}

	if err := s.bank.Deposit(ctx, p); err != nil {
		s.markFailed(ctx, p)
		return nil, apperrors.InternalError("could not deposit the payment with the bank", err)
	}

	slog.Info("payment stored and deposited",
		"idempotency_key", p.IdempotencyKey, "amount_cents", p.AmountCents, "bank", s.bank.Name())

	return p, nil
}

// ApplyBankResponse records the status the bank reported. Applying the same
// response twice is a no-op; one contradicting a terminal status is refused.
func (s *Service) ApplyBankResponse(ctx context.Context, idempotencyKey string, status paymentEntity.Status) error {
	final, err := paymentEntity.ParseFinalStatus(string(status))
	if err != nil {
		return apperrors.BadRequest("invalid bank status", err)
	}

	affected, err := s.repo.UpdateStatus(ctx, idempotencyKey, final, time.Now().UTC())
	if err != nil {
		return apperrors.InternalError("could not update the payment status", err)
	}
	if affected > 0 {
		return nil
	}

	existing, err := s.findExisting(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	switch {
	case existing == nil:
		return fmt.Errorf("%w: %s", paymentEntity.ErrNotFound, idempotencyKey)
	case existing.Status == final:
		return fmt.Errorf("%w: %s is already %s", paymentEntity.ErrAlreadyApplied, idempotencyKey, final)
	default:
		return fmt.Errorf("%w: %s is %s, the bank now reports %s",
			paymentEntity.ErrConflictingStatus, idempotencyKey, existing.Status, final)
	}
}

// FindByIdempotencyKey returns a stored payment.
func (s *Service) FindByIdempotencyKey(ctx context.Context, key string) (*paymentEntity.Payment, error) {
	p, err := s.repo.FindByIdempotencyKey(ctx, key)
	if errors.Is(err, paymentEntity.ErrNotFound) {
		return nil, apperrors.NotFound("payment not found", err)
	}
	if err != nil {
		return nil, apperrors.InternalError("could not read the payment", err)
	}
	return p, nil
}

func (s *Service) findExisting(ctx context.Context, key string) (*paymentEntity.Payment, error) {
	p, err := s.repo.FindByIdempotencyKey(ctx, key)
	if errors.Is(err, paymentEntity.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.InternalError("could not read the payment", err)
	}
	return p, nil
}

// replay handles losing the unique constraint to a competing request: the
// winner's payment decides the answer.
func (s *Service) replay(ctx context.Context, p *paymentEntity.Payment) (*paymentEntity.Payment, error) {
	existing, err := s.findExisting(ctx, p.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, apperrors.InternalError("payment reported as duplicate but could not be read", nil)
	}
	return resolveReplay(p, existing)
}

// resolveReplay returns the stored payment for a genuine retry, a conflict when
// the key was reused for a different payment, and an error when the stored
// payment never reached the bank: 200 has to mean the file was deposited.
func resolveReplay(p, existing *paymentEntity.Payment) (*paymentEntity.Payment, error) {
	if !p.SameLogicalPayment(existing) {
		slog.Warn("idempotency key reused for a different payment", "idempotency_key", p.IdempotencyKey)
		return nil, apperrors.Conflict("idempotency key already used for a different payment", nil)
	}

	if existing.Status == paymentEntity.StatusFailed {
		return nil, apperrors.InternalError("payment was recorded but the bank deposit previously failed", nil)
	}

	slog.Info("idempotent replay, payment already stored",
		"idempotency_key", existing.IdempotencyKey, "status", existing.Status)
	return existing, nil
}

// markFailed is best effort: the caller already has the deposit error to return.
func (s *Service) markFailed(ctx context.Context, p *paymentEntity.Payment) {
	if _, err := s.repo.UpdateStatus(ctx, p.IdempotencyKey, paymentEntity.StatusFailed, time.Now().UTC()); err != nil {
		slog.Error("could not mark the payment as failed",
			"idempotency_key", p.IdempotencyKey, "error", err)
		return
	}

	p.Status = paymentEntity.StatusFailed
	slog.Warn("payment marked as failed, it was stored but not deposited",
		"idempotency_key", p.IdempotencyKey)
}

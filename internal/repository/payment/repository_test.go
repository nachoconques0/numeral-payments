package payment_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"numeral-payments/internal/db"
	paymentEntity "numeral-payments/internal/entity/payment"
	paymentRepository "numeral-payments/internal/repository/payment"
)

func TestInsertAndFind(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)

	p := newPayment()
	if err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if p.ID == 0 {
		t.Error("the inserted payment must carry its id")
	}

	found, err := repo.FindByIdempotencyKey(ctx, "JXJ984XXXZ")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.AmountCents != 4299 || found.Status != paymentEntity.StatusPending {
		t.Errorf("unexpected stored payment: %+v", found)
	}
	if found.DebtorIBAN != p.DebtorIBAN || found.CreditorName != p.CreditorName {
		t.Errorf("unexpected parties: %+v", found)
	}
}

func TestFindReportsMissingPayments(t *testing.T) {
	repo := newRepository(t)

	_, err := repo.FindByIdempotencyKey(context.Background(), "NOSUCHKEY0")
	if !errors.Is(err, paymentEntity.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestInsertRejectsADuplicateIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)

	if err := repo.Insert(ctx, newPayment()); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	err := repo.Insert(ctx, newPayment())
	if !errors.Is(err, paymentEntity.ErrDuplicateIdempotencyKey) {
		t.Fatalf("expected ErrDuplicateIdempotencyKey, got %v", err)
	}
}

func TestInsertLeavesTheStoredPaymentUntouchedOnADuplicate(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)

	if err := repo.Insert(ctx, newPayment()); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	second := newPayment()
	second.AmountCents = 999
	if err := repo.Insert(ctx, second); !errors.Is(err, paymentEntity.ErrDuplicateIdempotencyKey) {
		t.Fatalf("expected ErrDuplicateIdempotencyKey, got %v", err)
	}

	found, err := repo.FindByIdempotencyKey(ctx, "JXJ984XXXZ")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.AmountCents != 4299 {
		t.Errorf("a rejected duplicate must not overwrite the stored payment, got %d cents", found.AmountCents)
	}
}

func TestUpdateStatus(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)

	if err := repo.Insert(ctx, newPayment()); err != nil {
		t.Fatalf("insert: %v", err)
	}

	affected, err := repo.UpdateStatus(ctx, "JXJ984XXXZ", paymentEntity.StatusProcessed, time.Now().UTC())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected 1 row updated, got %d", affected)
	}

	found, err := repo.FindByIdempotencyKey(ctx, "JXJ984XXXZ")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Status != paymentEntity.StatusProcessed {
		t.Errorf("expected PROCESSED, got %q", found.Status)
	}

	affected, err = repo.UpdateStatus(ctx, "JXJ984XXXZ", paymentEntity.StatusRejected, time.Now().UTC())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if affected != 0 {
		t.Errorf("a terminal payment must not be updated again, got %d rows", affected)
	}

	found, err = repo.FindByIdempotencyKey(ctx, "JXJ984XXXZ")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Status != paymentEntity.StatusProcessed {
		t.Errorf("the stored status must survive a contradicting update, got %q", found.Status)
	}

	affected, err = repo.UpdateStatus(ctx, "NOSUCHKEY0", paymentEntity.StatusProcessed, time.Now().UTC())
	if err != nil {
		t.Fatalf("update of an unknown payment must not error: %v", err)
	}
	if affected != 0 {
		t.Errorf("expected no rows updated, got %d", affected)
	}
}

func newRepository(t *testing.T) *paymentRepository.Repository {
	t.Helper()

	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "payments.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	repo := paymentRepository.NewRepository(database)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return repo
}

func newPayment() *paymentEntity.Payment {
	now := time.Now().UTC()
	return &paymentEntity.Payment{
		IdempotencyKey: "JXJ984XXXZ",
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		AmountCents:    4299,
		Currency:       "EUR",
		Status:         paymentEntity.StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

package payment_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"numeral-payments/internal/bank"
	paymentEntity "numeral-payments/internal/entity/payment"
	apperrors "numeral-payments/internal/errors"
	paymentService "numeral-payments/internal/service/payment"
)

func TestCreatePaymentStoresAndDeposits(t *testing.T) {
	repo := &fakeRepository{}
	adapter := &fakeBank{}
	service := paymentService.NewService(repo, adapter)

	created, err := service.CreatePayment(context.Background(), validInput())
	if err != nil {
		t.Fatalf("create payment: %v", err)
	}

	if created.Status != paymentEntity.StatusPending {
		t.Errorf("a new payment must be PENDING, got %q", created.Status)
	}
	if created.AmountCents != 4299 {
		t.Errorf("expected 4299 cents, got %d", created.AmountCents)
	}
	if repo.rows() != 1 {
		t.Errorf("expected 1 stored payment, got %d", repo.rows())
	}
	if adapter.depositCount() != 1 {
		t.Errorf("expected exactly 1 deposit, got %d", adapter.depositCount())
	}
}

func TestCreatePaymentMarksThePaymentFailedWhenTheDepositFails(t *testing.T) {
	repo := &fakeRepository{}
	adapter := &fakeBank{depositErr: errors.New("bank folder is read only")}
	service := paymentService.NewService(repo, adapter)

	_, err := service.CreatePayment(context.Background(), validInput())
	if !isStatus(err, 500) {
		t.Fatalf("expected a 500 AppError, got %#v", err)
	}

	stored := repo.current()
	if stored == nil {
		t.Fatal("the payment row must survive so the failure is visible")
	}
	if stored.Status != paymentEntity.StatusFailed {
		t.Errorf("a payment that could not be deposited must be FAILED, got %q", stored.Status)
	}
}

func TestCreatePaymentReportsAStorageFailure(t *testing.T) {
	repo := &fakeRepository{insertErr: errors.New("database is gone")}
	adapter := &fakeBank{}
	service := paymentService.NewService(repo, adapter)

	_, err := service.CreatePayment(context.Background(), validInput())
	if !isStatus(err, 500) {
		t.Fatalf("expected a 500 AppError, got %#v", err)
	}
	if adapter.depositCount() != 0 {
		t.Error("nothing must be deposited when the payment could not be recorded first")
	}
}

func TestCreatePaymentReplaysTheSameLogicalPayment(t *testing.T) {
	repo := &fakeRepository{}
	adapter := &fakeBank{}
	service := paymentService.NewService(repo, adapter)

	first, err := service.CreatePayment(context.Background(), validInput())
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	second, err := service.CreatePayment(context.Background(), validInput())
	if err != nil {
		t.Fatalf("a genuine retry must succeed: %v", err)
	}

	if second.IdempotencyKey != first.IdempotencyKey {
		t.Errorf("the retry must return the stored payment, got %+v", second)
	}
	if repo.rows() != 1 {
		t.Errorf("a retry must not store a second payment, got %d", repo.rows())
	}
	if adapter.depositCount() != 1 {
		t.Errorf("a retry must not deposit a second file, got %d", adapter.depositCount())
	}
}

func TestCreatePaymentRejectsAKeyReusedForADifferentPayment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paymentEntity.Input)
	}{
		{name: "different amount", mutate: func(in *paymentEntity.Input) { in.AmountCents = 500000 }},
		{name: "different creditor iban", mutate: func(in *paymentEntity.Input) { in.CreditorIBAN = "DE65500105179799248553" }},
		{name: "different creditor name", mutate: func(in *paymentEntity.Input) { in.CreditorName = "someone else" }},
		{name: "different debtor iban", mutate: func(in *paymentEntity.Input) { in.DebtorIBAN = "FR1112739000504482744411A65" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakeRepository{}
			adapter := &fakeBank{}
			service := paymentService.NewService(repo, adapter)

			if _, err := service.CreatePayment(context.Background(), validInput()); err != nil {
				t.Fatalf("first request: %v", err)
			}

			reused := validInput()
			test.mutate(&reused)

			_, err := service.CreatePayment(context.Background(), reused)
			if !isStatus(err, 409) {
				t.Fatalf("expected 409 Conflict, got %#v", err)
			}
			if adapter.depositCount() != 1 {
				t.Errorf("a conflict must not deposit anything, got %d deposits", adapter.depositCount())
			}
		})
	}
}

// TestCreatePaymentUnderConcurrentDuplicates covers the case the preliminary
// lookup cannot: both requests find nothing, and the unique constraint decides.
func TestCreatePaymentUnderConcurrentDuplicates(t *testing.T) {
	repo := &fakeRepository{}
	adapter := &fakeBank{}
	service := paymentService.NewService(repo, adapter)

	const requests = 8
	var wg sync.WaitGroup
	failures := make([]error, requests)

	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, failures[i] = service.CreatePayment(context.Background(), validInput())
		}(i)
	}
	wg.Wait()

	for i, err := range failures {
		if err != nil {
			t.Errorf("identical request %d must succeed, got %v", i, err)
		}
	}
	if repo.rows() != 1 {
		t.Errorf("expected exactly 1 stored payment, got %d", repo.rows())
	}
	if adapter.depositCount() != 1 {
		t.Errorf("expected exactly 1 deposit, got %d", adapter.depositCount())
	}
}

func TestApplyBankResponse(t *testing.T) {
	tests := []struct {
		name    string
		start   paymentEntity.Status
		report  paymentEntity.Status
		wantErr error
		wantEnd paymentEntity.Status
	}{
		{name: "pending to processed", start: paymentEntity.StatusPending, report: paymentEntity.StatusProcessed, wantEnd: paymentEntity.StatusProcessed},
		{name: "pending to rejected", start: paymentEntity.StatusPending, report: paymentEntity.StatusRejected, wantEnd: paymentEntity.StatusRejected},
		{name: "processed replayed", start: paymentEntity.StatusProcessed, report: paymentEntity.StatusProcessed, wantErr: paymentEntity.ErrAlreadyApplied, wantEnd: paymentEntity.StatusProcessed},
		{name: "rejected replayed", start: paymentEntity.StatusRejected, report: paymentEntity.StatusRejected, wantErr: paymentEntity.ErrAlreadyApplied, wantEnd: paymentEntity.StatusRejected},
		{name: "processed contradicted", start: paymentEntity.StatusProcessed, report: paymentEntity.StatusRejected, wantErr: paymentEntity.ErrConflictingStatus, wantEnd: paymentEntity.StatusProcessed},
		{name: "rejected contradicted", start: paymentEntity.StatusRejected, report: paymentEntity.StatusProcessed, wantErr: paymentEntity.ErrConflictingStatus, wantEnd: paymentEntity.StatusRejected},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakeRepository{stored: storedPayment(test.start)}
			service := paymentService.NewService(repo, &fakeBank{})

			err := service.ApplyBankResponse(context.Background(), "JXJ984XXXZ", test.report)
			if test.wantErr == nil && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			if got := repo.current().Status; got != test.wantEnd {
				t.Errorf("expected the payment to end as %q, got %q", test.wantEnd, got)
			}
		})
	}
}

func TestApplyBankResponseRejectsWhatTheBankMayNotReport(t *testing.T) {
	repo := &fakeRepository{stored: storedPayment(paymentEntity.StatusPending)}
	service := paymentService.NewService(repo, &fakeBank{})

	if err := service.ApplyBankResponse(context.Background(), "JXJ984XXXZ", "WHAT"); !errors.Is(err, paymentEntity.ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
	if err := service.ApplyBankResponse(context.Background(), "NOSUCHKEY0", paymentEntity.StatusProcessed); !errors.Is(err, paymentEntity.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func validInput() paymentEntity.Input {
	return paymentEntity.Input{
		IdempotencyKey: "JXJ984XXXZ",
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		AmountCents:    4299,
	}
}

func storedPayment(status paymentEntity.Status) *paymentEntity.Payment {
	in := validInput()
	return &paymentEntity.Payment{
		ID:             1,
		IdempotencyKey: in.IdempotencyKey,
		DebtorIBAN:     in.DebtorIBAN,
		DebtorName:     in.DebtorName,
		CreditorIBAN:   in.CreditorIBAN,
		CreditorName:   in.CreditorName,
		AmountCents:    in.AmountCents,
		Currency:       paymentEntity.DefaultCurrency,
		Status:         status,
	}
}

func isStatus(err error, code int) bool {
	var appErr *apperrors.AppError
	return errors.As(err, &appErr) && appErr.Code == code
}

// fakeRepository stands in for sqlite, including the unique constraint on the
// idempotency key, which is what decides a concurrent duplicate.
type fakeRepository struct {
	mu        sync.Mutex
	stored    *paymentEntity.Payment
	insertErr error
}

func (r *fakeRepository) FindByIdempotencyKey(_ context.Context, key string) (*paymentEntity.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stored == nil || r.stored.IdempotencyKey != key {
		return nil, paymentEntity.ErrNotFound
	}

	found := *r.stored
	return &found, nil
}

func (r *fakeRepository) Insert(_ context.Context, p *paymentEntity.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.insertErr != nil {
		return r.insertErr
	}
	if r.stored != nil {
		return paymentEntity.ErrDuplicateIdempotencyKey
	}

	p.ID = 1
	inserted := *p
	r.stored = &inserted
	return nil
}

func (r *fakeRepository) UpdateStatus(_ context.Context, key string, status paymentEntity.Status, _ time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stored == nil || r.stored.IdempotencyKey != key || r.stored.Status != paymentEntity.StatusPending {
		return 0, nil
	}

	r.stored.Status = status
	return 1, nil
}

func (r *fakeRepository) rows() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stored == nil {
		return 0
	}
	return 1
}

func (r *fakeRepository) current() *paymentEntity.Payment {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.stored
}

type fakeBank struct {
	mu         sync.Mutex
	deposits   int
	depositErr error
}

func (b *fakeBank) Name() string { return "fake" }

func (b *fakeBank) Deposit(_ context.Context, _ *paymentEntity.Payment) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.depositErr != nil {
		return b.depositErr
	}
	b.deposits++
	return nil
}

func (b *fakeBank) depositCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.deposits
}

func (b *fakeBank) ResponsePattern() string { return "*.csv" }

func (b *fakeBank) ParseResponse(_ []byte) ([]bank.Response, error) { return nil, nil }

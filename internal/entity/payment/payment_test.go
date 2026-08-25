package payment_test

import (
	"errors"
	"testing"
	"time"

	paymentEntity "numeral-payments/internal/entity/payment"
)

var createdAt = time.Date(2026, 8, 25, 9, 30, 47, 0, time.UTC)

func TestNewStartsAPaymentPending(t *testing.T) {
	p, err := paymentEntity.New(validInput(), createdAt)
	if err != nil {
		t.Fatalf("new payment: %v", err)
	}

	if p.Status != paymentEntity.StatusPending {
		t.Errorf("a new payment must be PENDING, got %q", p.Status)
	}
	if p.Currency != paymentEntity.DefaultCurrency {
		t.Errorf("currency must default to EUR, got %q", p.Currency)
	}
	if p.AmountCents != 4299 {
		t.Errorf("expected 4299 cents, got %d", p.AmountCents)
	}
	if !p.CreatedAt.Equal(createdAt) || !p.UpdatedAt.Equal(createdAt) {
		t.Errorf("timestamps must come from the caller, got %s and %s", p.CreatedAt, p.UpdatedAt)
	}
}

func TestNewKeepsAnExplicitCurrency(t *testing.T) {
	in := validInput()
	in.Currency = "GBP"

	p, err := paymentEntity.New(in, createdAt)
	if err != nil {
		t.Fatalf("new payment: %v", err)
	}
	if p.Currency != "GBP" {
		t.Errorf("expected GBP, got %q", p.Currency)
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*paymentEntity.Input)
		wantErr error
	}{
		{
			name:    "key too short",
			mutate:  func(in *paymentEntity.Input) { in.IdempotencyKey = "SHORT" },
			wantErr: paymentEntity.ErrInvalidIdempotencyKey,
		},
		{
			name:    "key too long",
			mutate:  func(in *paymentEntity.Input) { in.IdempotencyKey = "WAYTOOLONGKEY" },
			wantErr: paymentEntity.ErrInvalidIdempotencyKey,
		},
		{
			name:    "missing debtor name",
			mutate:  func(in *paymentEntity.Input) { in.DebtorName = "" },
			wantErr: paymentEntity.ErrMissingParty,
		},
		{
			name:    "missing creditor iban",
			mutate:  func(in *paymentEntity.Input) { in.CreditorIBAN = "" },
			wantErr: paymentEntity.ErrMissingParty,
		},
		{
			name:    "zero amount",
			mutate:  func(in *paymentEntity.Input) { in.AmountCents = 0 },
			wantErr: paymentEntity.ErrInvalidAmount,
		},
		{
			name:    "negative amount",
			mutate:  func(in *paymentEntity.Input) { in.AmountCents = -1 },
			wantErr: paymentEntity.ErrInvalidAmount,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := validInput()
			test.mutate(&in)

			if _, err := paymentEntity.New(in, createdAt); !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
		})
	}
}

func TestCentsFromDecimal(t *testing.T) {
	tests := []struct {
		text string
		want int64
	}{
		{text: "0", want: 0},
		{text: "1", want: 100},
		{text: "1.01", want: 101},
		{text: "42.9", want: 4290},
		{text: "42.99", want: 4299},
		{text: "0.05", want: 5},
		// 1.15 and 0.29 are not exactly representable as binary floats, so a
		// float conversion could land a cent off. Reading the digits cannot.
		{text: "1.15", want: 115},
		{text: "0.29", want: 29},
		{text: "1234567.89", want: 123456789},
		{text: "999999999.99", want: 99999999999},
		// The largest amount that still fits in int64 cents.
		{text: "92233720368547757.99", want: 9223372036854775799},
		{text: "-42.99", want: -4299},
	}

	for _, test := range tests {
		got, err := paymentEntity.CentsFromDecimal(test.text)
		if err != nil {
			t.Errorf("CentsFromDecimal(%q) returned %v", test.text, err)
			continue
		}
		if got != test.want {
			t.Errorf("CentsFromDecimal(%q) = %d, want %d", test.text, got, test.want)
		}
	}
}

func TestCentsFromDecimalRejectsAmountsItCannotStoreExactly(t *testing.T) {
	// 92233720368547758.99 passes a units-only bound but overflows once the cents
	// are added, so the bound has to cover the whole sum.
	for _, text := range []string{"42.999", "0.001", "1.0000", "4.2e1", "1E2", "", "abc", "4 2",
		"92233720368547758.99"} {
		if _, err := paymentEntity.CentsFromDecimal(text); !errors.Is(err, paymentEntity.ErrInvalidAmount) {
			t.Errorf("CentsFromDecimal(%q) must be rejected, got %v", text, err)
		}
	}
}

func TestFormattedAmount(t *testing.T) {
	tests := []struct {
		cents int64
		want  string
	}{
		{cents: 4299, want: "42.99"},
		{cents: 5, want: "0.05"},
		{cents: 100, want: "1.00"},
		{cents: 0, want: "0.00"},
		{cents: 123456789, want: "1234567.89"},
		{cents: -4299, want: "-42.99"},
	}

	for _, test := range tests {
		p := paymentEntity.Payment{AmountCents: test.cents}
		if got := p.FormattedAmount(); got != test.want {
			t.Errorf("FormattedAmount(%d) = %q, want %q", test.cents, got, test.want)
		}
	}
}

func TestParseFinalStatus(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  paymentEntity.Status
		valid bool
	}{
		{name: "processed", raw: "PROCESSED", want: paymentEntity.StatusProcessed, valid: true},
		{name: "rejected", raw: "REJECTED", want: paymentEntity.StatusRejected, valid: true},
		{name: "lower case", raw: "processed", want: paymentEntity.StatusProcessed, valid: true},
		{name: "surrounding whitespace", raw: "  rejected  ", want: paymentEntity.StatusRejected, valid: true},
		{name: "pending is not a bank status", raw: "PENDING"},
		{name: "failed is not a bank status", raw: "FAILED"},
		{name: "garbage", raw: "WHAT"},
		{name: "empty", raw: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := paymentEntity.ParseFinalStatus(test.raw)

			if !test.valid {
				if !errors.Is(err, paymentEntity.ErrInvalidStatus) {
					t.Fatalf("expected ErrInvalidStatus for %q, got %v", test.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected %q to parse, got %v", test.raw, err)
			}
			if got != test.want {
				t.Errorf("ParseFinalStatus(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestIsFinal(t *testing.T) {
	tests := []struct {
		status paymentEntity.Status
		want   bool
	}{
		{status: paymentEntity.StatusPending, want: false},
		{status: paymentEntity.StatusProcessed, want: true},
		{status: paymentEntity.StatusRejected, want: true},
		// Nothing was deposited, so no bank response will ever arrive.
		{status: paymentEntity.StatusFailed, want: true},
	}

	for _, test := range tests {
		p := paymentEntity.Payment{Status: test.status}
		if got := p.IsFinal(); got != test.want {
			t.Errorf("%q IsFinal() = %v, want %v", test.status, got, test.want)
		}
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

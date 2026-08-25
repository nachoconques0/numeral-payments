// Package payment holds the pure payment domain: the entity, its statuses and
// the invariants they must satisfy. It has no HTTP, database or framework
// dependencies.
package payment

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Status is the lifecycle state of a payment.
type Status string

const (
	// StatusPending means the payment is recorded and awaiting a terminal bank outcome.
	StatusPending Status = "PENDING"
	// StatusProcessed means the bank accepted the payment.
	StatusProcessed Status = "PROCESSED"
	// StatusRejected means the bank refused the payment.
	StatusRejected Status = "REJECTED"
	// StatusFailed means the payment was recorded but the deposit failed. The
	// bank does not report it; we set it so the row is not left as PENDING.
	StatusFailed Status = "FAILED"
)

// IdempotencyKeyLength is the fixed key length required by the payment schema.
const IdempotencyKeyLength = 10

// Domain errors. They are sentinels so that callers can react with errors.Is.
var (
	ErrInvalidIdempotencyKey   = errors.New("idempotency_unique_key must be exactly 10 characters")
	ErrMissingParty            = errors.New("debtor and creditor name and IBAN are required")
	ErrInvalidAmount           = errors.New("invalid amount")
	ErrInvalidStatus           = errors.New("status must be PROCESSED or REJECTED")
	ErrDuplicateIdempotencyKey = errors.New("payment with this idempotency key already exists")
	ErrNotFound                = errors.New("payment not found")
	ErrAlreadyApplied          = errors.New("bank response already applied")
	ErrConflictingStatus       = errors.New("bank response contradicts the stored status")
)

// Payment is a single payment order.
type Payment struct {
	ID             int64
	IdempotencyKey string
	DebtorIBAN     string
	DebtorName     string
	CreditorIBAN   string
	CreditorName   string
	AmountCents    int64
	Currency       string
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Input carries the values needed to create a payment, already in cents.
type Input struct {
	IdempotencyKey string
	DebtorIBAN     string
	DebtorName     string
	CreditorIBAN   string
	CreditorName   string
	AmountCents    int64
	Currency       string
}

// DefaultCurrency is used when the request does not carry one; the provided
// schema has no currency field and the sample XML settles in euro.
const DefaultCurrency = "EUR"

// New builds a payment in the PENDING state, enforcing the domain invariants.
func New(in Input, now time.Time) (*Payment, error) {
	if len(strings.TrimSpace(in.IdempotencyKey)) != IdempotencyKeyLength {
		return nil, ErrInvalidIdempotencyKey
	}
	if in.DebtorIBAN == "" || in.DebtorName == "" || in.CreditorIBAN == "" || in.CreditorName == "" {
		return nil, ErrMissingParty
	}
	if in.AmountCents <= 0 {
		return nil, fmt.Errorf("%w: must be greater than zero", ErrInvalidAmount)
	}

	currency := in.Currency
	if currency == "" {
		currency = DefaultCurrency
	}

	return &Payment{
		IdempotencyKey: in.IdempotencyKey,
		DebtorIBAN:     in.DebtorIBAN,
		DebtorName:     in.DebtorName,
		CreditorIBAN:   in.CreditorIBAN,
		CreditorName:   in.CreditorName,
		AmountCents:    in.AmountCents,
		Currency:       currency,
		Status:         StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// CentsFromDecimal parses a decimal literal such as "42.99" into cents by
// reading its digits, so no float rounding can alter the amount on the way in.
func CentsFromDecimal(text string) (int64, error) {
	if strings.ContainsAny(text, "eE") {
		return 0, fmt.Errorf("%w: exponent notation is not supported", ErrInvalidAmount)
	}

	sign := int64(1)
	if rest, found := strings.CutPrefix(text, "-"); found {
		sign, text = -1, rest
	}

	// We settle in EUR, so this service assumes two minor-unit digits. The
	// provided schema only says "number"; rejecting beats silently rounding.
	whole, fraction, _ := strings.Cut(text, ".")
	if len(fraction) > 2 {
		return 0, fmt.Errorf("%w: at most two decimal places are supported", ErrInvalidAmount)
	}

	var cents int64
	if fraction != "" {
		parsed, err := strconv.ParseInt((fraction + "0")[:2], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q is not an amount we can store", ErrInvalidAmount, text)
		}
		cents = parsed
	}

	// The bound accounts for cents so it covers the whole sum, not just units*100.
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || units > (math.MaxInt64-cents)/100 {
		return 0, fmt.Errorf("%w: %q is not an amount we can store", ErrInvalidAmount, text)
	}

	return sign * (units*100 + cents), nil
}

// FormattedAmount renders the amount with two decimals directly from the
// integer cents, avoiding any floating point rounding on the way out.
func (p *Payment) FormattedAmount() string {
	cents := p.AmountCents
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// SameLogicalPayment reports whether both describe the same transfer. Status and
// timestamps are excluded: they change, the payment instruction does not.
func (p *Payment) SameLogicalPayment(other *Payment) bool {
	return p.DebtorIBAN == other.DebtorIBAN &&
		p.DebtorName == other.DebtorName &&
		p.CreditorIBAN == other.CreditorIBAN &&
		p.CreditorName == other.CreditorName &&
		p.AmountCents == other.AmountCents &&
		p.Currency == other.Currency
}

// IsFinal reports whether the payment has stopped moving on its own. FAILED
// counts: nothing was deposited, so there is no response to wait for.
func (p *Payment) IsFinal() bool {
	return p.Status == StatusProcessed || p.Status == StatusRejected || p.Status == StatusFailed
}

// ParseFinalStatus reads a status coming from a bank response, accepting only
// the two terminal states the bank is allowed to report.
func ParseFinalStatus(raw string) (Status, error) {
	switch Status(strings.ToUpper(strings.TrimSpace(raw))) {
	case StatusProcessed:
		return StatusProcessed, nil
	case StatusRejected:
		return StatusRejected, nil
	default:
		return "", fmt.Errorf("%w: got %q", ErrInvalidStatus, raw)
	}
}

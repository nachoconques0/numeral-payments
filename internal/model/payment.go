// Package model holds the data transfer objects exchanged over HTTP and the
// mappers between them and the domain entities.
package model

import (
	"encoding/json"

	paymentEntity "numeral-payments/internal/entity/payment"
)

// CreatePaymentRequest is the payment request body. The amount is spelled
// "ammount" because the provided schema spells it that way.
type CreatePaymentRequest struct {
	DebtorIBAN     string      `json:"debtor_iban"`
	DebtorName     string      `json:"debtor_name"`
	CreditorIBAN   string      `json:"creditor_iban"`
	CreditorName   string      `json:"creditor_name"`
	Amount         json.Number `json:"ammount"`
	IdempotencyKey string      `json:"idempotency_unique_key"`
}

// ToEntityInput maps the request onto the domain input, reading the amount as a
// decimal literal rather than a float.
func (r CreatePaymentRequest) ToEntityInput() (paymentEntity.Input, error) {
	cents, err := paymentEntity.CentsFromDecimal(r.Amount.String())
	if err != nil {
		return paymentEntity.Input{}, err
	}

	return paymentEntity.Input{
		IdempotencyKey: r.IdempotencyKey,
		DebtorIBAN:     r.DebtorIBAN,
		DebtorName:     r.DebtorName,
		CreditorIBAN:   r.CreditorIBAN,
		CreditorName:   r.CreditorName,
		AmountCents:    cents,
	}, nil
}

// PaymentResponse is the payment representation returned to clients.
type PaymentResponse struct {
	IdempotencyKey string `json:"idempotency_unique_key"`
	Status         string `json:"status"`
	Amount         string `json:"amount"`
	Currency       string `json:"currency"`
	CreatedAt      string `json:"created_at"`
}

// NewPaymentResponse maps a payment entity onto its API representation.
func NewPaymentResponse(p *paymentEntity.Payment) PaymentResponse {
	return PaymentResponse{
		IdempotencyKey: p.IdempotencyKey,
		Status:         string(p.Status),
		Amount:         p.FormattedAmount(),
		Currency:       p.Currency,
		CreatedAt:      p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

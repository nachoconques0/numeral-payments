// Package csvbank is a second bank adapter, taking flat CSV instructions instead
// of ISO 20022 XML. It exists to keep the bank port honest.
package csvbank

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"

	"numeral-payments/internal/bank"
	paymentEntity "numeral-payments/internal/entity/payment"
)

// Adapter deposits payments as CSV into the bank folder.
type Adapter struct {
	folder string
}

// NewAdapter returns an adapter writing to and reading from folder.
func NewAdapter(folder string) *Adapter {
	return &Adapter{folder: folder}
}

// Name identifies the adapter in logs and configuration.
func (a *Adapter) Name() string { return "csv" }

// ResponsePattern is narrowed to response files because this adapter deposits
// CSV too. Only the adapter can know that, which is why it is on the port.
func (a *Adapter) ResponsePattern() string { return "response_*.csv" }

var depositHeader = []string{
	"IDEMPOTENCY_KEY", "DEBTOR_IBAN", "DEBTOR_NAME", "CREDITOR_IBAN", "CREDITOR_NAME", "AMOUNT", "CURRENCY",
}

// Deposit writes the payment as a CSV instruction file into the bank folder.
func (a *Adapter) Deposit(ctx context.Context, p *paymentEntity.Payment) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	payload, err := Marshal(p)
	if err != nil {
		return err
	}

	if _, err := bank.WriteFileAtomic(a.folder, fileName(p.ID), payload); err != nil {
		return fmt.Errorf("deposit payment %s: %w", p.IdempotencyKey, err)
	}

	return nil
}

// ParseResponse reads a bank response file.
func (a *Adapter) ParseResponse(data []byte) ([]bank.Response, error) {
	return bank.ParseResponseCSV(data)
}

// fileName uses the internal row id, never the client supplied key, so a key
// cannot steer the path.
func fileName(id int64) string {
	return fmt.Sprintf("payment_%d.csv", id)
}

// Marshal renders a payment as this bank's CSV instruction.
func Marshal(p *paymentEntity.Payment) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	rows := [][]string{
		depositHeader,
		{
			p.IdempotencyKey,
			p.DebtorIBAN,
			p.DebtorName,
			p.CreditorIBAN,
			p.CreditorName,
			p.FormattedAmount(),
			p.Currency,
		},
	}

	if err := writer.WriteAll(rows); err != nil {
		return nil, fmt.Errorf("marshal payment csv: %w", err)
	}

	return buf.Bytes(), nil
}

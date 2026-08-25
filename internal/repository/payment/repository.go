// Package payment is the persistence adapter for payments, backed by sqlite
// through database/sql.
package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	paymentEntity "numeral-payments/internal/entity/payment"
)

const timeLayout = time.RFC3339Nano

const schema = `
CREATE TABLE IF NOT EXISTS payments (
	id                     INTEGER PRIMARY KEY AUTOINCREMENT,
	idempotency_unique_key TEXT    NOT NULL UNIQUE,
	debtor_iban            TEXT    NOT NULL,
	debtor_name            TEXT    NOT NULL,
	creditor_iban          TEXT    NOT NULL,
	creditor_name          TEXT    NOT NULL,
	amount_cents           INTEGER NOT NULL,
	currency               TEXT    NOT NULL,
	status                 TEXT    NOT NULL,
	created_at             TEXT    NOT NULL,
	updated_at             TEXT    NOT NULL
);`

// Repository reads and writes payments. It is stateless beyond its connection.
type Repository struct {
	db *sql.DB
}

// NewRepository returns a repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Migrate creates the payments table if it does not exist.
func (r *Repository) Migrate(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create payments table: %w", err)
	}
	return nil
}

// FindByIdempotencyKey returns the payment stored under key, or
// paymentEntity.ErrNotFound when there is none.
func (r *Repository) FindByIdempotencyKey(ctx context.Context, key string) (*paymentEntity.Payment, error) {
	const query = `
SELECT id, idempotency_unique_key, debtor_iban, debtor_name, creditor_iban, creditor_name,
       amount_cents, currency, status, created_at, updated_at
FROM payments
WHERE idempotency_unique_key = ?`

	return scanPayment(r.db.QueryRowContext(ctx, query, key))
}

// UpdateStatus moves a payment out of PENDING and returns the rows affected. The
// status guard makes a replayed bank response a no-op instead of an overwrite.
func (r *Repository) UpdateStatus(ctx context.Context, key string, status paymentEntity.Status, updatedAt time.Time) (int64, error) {
	const query = `
UPDATE payments SET status = ?, updated_at = ?
WHERE idempotency_unique_key = ? AND status = ?`

	result, err := r.db.ExecContext(ctx, query,
		string(status), updatedAt.UTC().Format(timeLayout), key, string(paymentEntity.StatusPending))
	if err != nil {
		return 0, fmt.Errorf("update payment status: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read affected rows: %w", err)
	}

	return affected, nil
}

// Insert stores a payment. A single INSERT is already atomic, so there is no
// explicit transaction here.
//
// It returns paymentEntity.ErrDuplicateIdempotencyKey when the key is taken.
func (r *Repository) Insert(ctx context.Context, p *paymentEntity.Payment) error {
	const query = `
INSERT INTO payments (idempotency_unique_key, debtor_iban, debtor_name, creditor_iban, creditor_name,
                      amount_cents, currency, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := r.db.ExecContext(ctx, query,
		p.IdempotencyKey, p.DebtorIBAN, p.DebtorName, p.CreditorIBAN, p.CreditorName,
		p.AmountCents, p.Currency, string(p.Status),
		p.CreatedAt.UTC().Format(timeLayout), p.UpdatedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %s", paymentEntity.ErrDuplicateIdempotencyKey, p.IdempotencyKey)
		}
		return fmt.Errorf("insert payment: %w", err)
	}

	if id, err := result.LastInsertId(); err == nil {
		p.ID = id
	}

	return nil
}

func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

func scanPayment(row *sql.Row) (*paymentEntity.Payment, error) {
	var (
		p                    paymentEntity.Payment
		status               string
		createdAt, updatedAt string
	)

	err := row.Scan(&p.ID, &p.IdempotencyKey, &p.DebtorIBAN, &p.DebtorName, &p.CreditorIBAN,
		&p.CreditorName, &p.AmountCents, &p.Currency, &status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, paymentEntity.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan payment: %w", err)
	}

	p.Status = paymentEntity.Status(status)
	if p.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if p.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}

	return &p, nil
}

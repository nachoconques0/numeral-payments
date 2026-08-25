// Package bank holds the types every bank adapter shares: the response a bank
// reports back, and the file helpers used to exchange files with it.
package bank

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	paymentEntity "numeral-payments/internal/entity/payment"
)

// Sub folders of the bank folder where consumed response files are quarantined.
const (
	ProcessedDir = "processed"
	FailedDir    = "failed"
)

// Response is one status update reported by the bank for one payment.
type Response struct {
	IdempotencyKey string
	Status         paymentEntity.Status
}

// EnsureFolders creates the bank folder and the quarantine sub folders.
func EnsureFolders(root string) error {
	for _, dir := range []string{root, filepath.Join(root, ProcessedDir), filepath.Join(root, FailedDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create bank folder %s: %w", dir, err)
		}
	}
	return nil
}

// WriteFileAtomic writes name into dir via a temporary file and a rename, so the
// bank cannot read a half written payment. It refuses a name that escapes dir.
func WriteFileAtomic(dir, name string, data []byte) (string, error) {
	target := filepath.Clean(filepath.Join(dir, name))
	if parent := filepath.Clean(dir); filepath.Dir(target) != parent {
		return "", fmt.Errorf("payment file name %q escapes the bank folder", name)
	}
	tmp := target + ".tmp"

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("write temporary payment file: %w", err)
	}

	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("move payment file into place: %w", err)
	}

	return target, nil
}

// ParseResponseCSV reads "ID, STATUS" rows, trimming the space after the comma.
// Only a structurally broken file is an error; judging a row's status is the
// service's job.
func ParseResponseCSV(data []byte) ([]Response, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse bank response csv: %w", err)
	}

	responses := make([]Response, 0, len(records))
	for i, record := range records {
		if len(record) < 2 {
			return nil, fmt.Errorf("row %d: expected 2 columns, got %d", i+1, len(record))
		}

		id := strings.TrimSpace(record[0])
		if i == 0 && strings.EqualFold(id, "ID") {
			continue
		}
		if id == "" {
			continue
		}

		responses = append(responses, Response{
			IdempotencyKey: id,
			Status:         paymentEntity.Status(strings.TrimSpace(record[1])),
		})
	}

	return responses, nil
}

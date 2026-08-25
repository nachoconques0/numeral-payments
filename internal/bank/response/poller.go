// Package response polls the bank folder for response files and hands the
// statuses it finds to the application.
package response

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"numeral-payments/internal/bank"
	paymentEntity "numeral-payments/internal/entity/payment"
)

// ResponseSource is the slice of a bank adapter the poller needs. It does not
// know how payments are deposited.
type ResponseSource interface {
	Name() string
	ResponsePattern() string
	ParseResponse(data []byte) ([]bank.Response, error)
}

// StatusUpdater applies a bank response to a payment. It is implemented by the
// payment service: the poller never reaches into the repository itself.
type StatusUpdater interface {
	ApplyBankResponse(ctx context.Context, idempotencyKey string, status paymentEntity.Status) error
}

// Poller polls a folder on an interval.
type Poller struct {
	folder   string
	interval time.Duration
	source   ResponseSource
	updater  StatusUpdater
}

// Options configures a Poller.
type Options struct {
	Folder   string
	Interval time.Duration
	Source   ResponseSource
	Updater  StatusUpdater
}

// NewPoller returns a poller reading opts.Folder every opts.Interval.
func NewPoller(opts Options) *Poller {
	return &Poller{
		folder:   opts.Folder,
		interval: opts.Interval,
		source:   opts.Source,
		updater:  opts.Updater,
	}
}

// Run polls until ctx is cancelled. It is meant to be started in its own
// goroutine by the application, which also owns its shutdown.
func (p *Poller) Run(ctx context.Context) {
	slog.Info("bank response poller started",
		"folder", p.folder, "pattern", p.source.ResponsePattern(), "interval", p.interval, "adapter", p.source.Name())

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("bank response poller stopped")
			return
		case <-ticker.C:
			if err := p.scan(ctx); err != nil {
				slog.Error("scan bank folder", "error", err)
			}
		}
	}
}

func (p *Poller) scan(ctx context.Context) error {
	matches, err := filepath.Glob(filepath.Join(p.folder, p.source.ResponsePattern()))
	if err != nil {
		return fmt.Errorf("glob bank folder: %w", err)
	}

	for _, path := range matches {
		if ctx.Err() != nil {
			return nil
		}

		ready, err := p.isStable(path)
		if err != nil {
			slog.Warn("stat bank response", "file", path, "error", err)
			continue
		}
		if !ready {
			continue
		}

		p.consume(ctx, path)
	}

	return nil
}

// isStable reports whether a file has been untouched for a poll interval. It is
// a heuristic, not a completion protocol: see the README.
func (p *Poller) isStable(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	return time.Since(info.ModTime()) >= p.interval, nil
}

// consume applies a response file and quarantines it either way. Any row we
// could not apply sends the file to failed/, so it stays visible.
func (p *Poller) consume(ctx context.Context, path string) {
	responses, err := p.read(path)
	if err != nil {
		slog.Error("read bank response", "file", path, "error", err)
		p.quarantine(path, bank.FailedDir)
		return
	}

	file := filepath.Base(path)
	unresolved := 0

	for _, response := range responses {
		err := p.updater.ApplyBankResponse(ctx, response.IdempotencyKey, response.Status)
		switch {
		case err == nil:
			slog.Info("payment status updated from bank response",
				"idempotency_key", response.IdempotencyKey, "status", response.Status, "file", file)
		case errors.Is(err, paymentEntity.ErrAlreadyApplied):
			slog.Info("bank response already applied",
				"idempotency_key", response.IdempotencyKey, "status", response.Status, "file", file)
		case errors.Is(err, paymentEntity.ErrNotFound):
			unresolved++
			slog.Warn("bank reported an unknown payment id",
				"idempotency_key", response.IdempotencyKey, "file", file)
		case errors.Is(err, paymentEntity.ErrConflictingStatus):
			unresolved++
			slog.Warn("conflicting bank response, keeping the stored status",
				"idempotency_key", response.IdempotencyKey, "file", file, "error", err)
		default:
			unresolved++
			slog.Error("apply bank response",
				"idempotency_key", response.IdempotencyKey, "file", file, "error", err)
		}
	}

	if unresolved > 0 {
		slog.Warn("bank response quarantined, some rows could not be applied",
			"file", file, "unresolved", unresolved, "rows", len(responses))
		p.quarantine(path, bank.FailedDir)
		return
	}

	p.quarantine(path, bank.ProcessedDir)
}

func (p *Poller) read(path string) ([]bank.Response, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return p.source.ParseResponse(data)
}

func (p *Poller) quarantine(path, dir string) {
	target := filepath.Join(p.folder, dir, filepath.Base(path))
	if err := os.Rename(path, target); err != nil {
		slog.Error("quarantine bank response", "file", path, "target", target, "error", err)
	}
}

// Package app wires the service together and owns its lifecycle.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"numeral-payments/internal/bank"
	"numeral-payments/internal/bank/csvbank"
	bankresponse "numeral-payments/internal/bank/response"
	"numeral-payments/internal/bank/xmlbank"
	"numeral-payments/internal/config"
	paymentController "numeral-payments/internal/controller/payment"
	"numeral-payments/internal/db"
	httprouter "numeral-payments/internal/http"
	paymentRepository "numeral-payments/internal/repository/payment"
	paymentService "numeral-payments/internal/service/payment"
	"numeral-payments/internal/validator"
)

// App holds the running pieces of the service.
type App struct {
	db        *sql.DB
	responses *bankresponse.Poller
	server    *nethttp.Server
}

// New wires every dependency, bottom up.
func New(cfg config.Config) (*App, error) {
	if err := bank.EnsureFolders(cfg.BankFolder); err != nil {
		return nil, err
	}

	adapter, err := newBankAdapter(cfg)
	if err != nil {
		return nil, err
	}

	database, err := db.OpenSQLite(cfg.SQLiteDBFileLocation)
	if err != nil {
		return nil, err
	}

	paymentRepo := paymentRepository.NewRepository(database)
	if err := paymentRepo.Migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}

	requestValidator, err := validator.New()
	if err != nil {
		_ = database.Close()
		return nil, err
	}

	paymentSvc := paymentService.NewService(paymentRepo, adapter)
	paymentCtrl := paymentController.NewController(paymentSvc, requestValidator)

	responsePoller := bankresponse.NewPoller(bankresponse.Options{
		Folder:   cfg.BankFolder,
		Interval: cfg.PollInterval,
		Source:   adapter,
		Updater:  paymentSvc,
	})

	slog.Info("service configured",
		"bank_folder", cfg.BankFolder, "bank_adapter", adapter.Name(),
		"database", cfg.SQLiteDBFileLocation, "poll_interval", cfg.PollInterval)

	return &App{
		db:        database,
		responses: responsePoller,
		server: &nethttp.Server{
			Addr:              cfg.Addr,
			Handler:           httprouter.NewRouter(cfg.Auth, paymentCtrl),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

// newBankAdapter picks the bank integration named in the configuration.
// Adding a bank means adding one case here.
func newBankAdapter(cfg config.Config) (paymentService.BankAdapter, error) {
	switch cfg.BankAdapter {
	case "xml":
		return xmlbank.NewAdapter(cfg.BankFolder), nil
	case "csv":
		return csvbank.NewAdapter(cfg.BankFolder), nil
	default:
		return nil, fmt.Errorf("unknown BANK_ADAPTER %q, expected xml or csv", cfg.BankAdapter)
	}
}

// Run starts the response poller and the HTTP server and blocks until the
// service is asked to stop.
func (a *App) Run() error {
	defer a.db.Close()

	// Cancels ctx on Ctrl-C or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bank response poller runs until ctx is cancelled.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.responses.Run(ctx)
	}()

	// HTTP server runs until Shutdown is called.
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Whichever happens first: the server dies, or we are asked to stop.
	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	// Stop accepting new requests, let the in-flight ones finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	wg.Wait()
	slog.Info("shutdown complete")
	return nil
}

// Command server runs the payment service.
package main

import (
	"log/slog"
	"os"

	"numeral-payments/internal/app"
	"numeral-payments/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	instance, err := app.New(cfg)
	if err != nil {
		slog.Error("could not start the service", "error", err)
		os.Exit(1)
	}

	if err := instance.Run(); err != nil {
		slog.Error("service stopped with an error", "error", err)
		os.Exit(1)
	}
}

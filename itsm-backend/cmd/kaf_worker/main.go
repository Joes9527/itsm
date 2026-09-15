package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"itsm-backend/internal/bootstrap"
)

func run(ctx context.Context, newApplication func() (*bootstrap.KAFWorkerApplication, error)) error {
	application, err := newApplication()
	if err != nil {
		return err
	}

	return application.Run(ctx)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, bootstrap.NewKAFWorkerApplication); err != nil {
		log.Fatalf("KAF worker failed: %v", err)
	}
}

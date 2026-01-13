// Command ratelimit starts the service application.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ratelimit/internal/ratelimit"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := &ratelimit.Config{
		Region:         "local",
		EnableHTTP:     true,
		HTTPListenAddr: ":8080",
	}
	app, err := ratelimit.NewApplication(cfg)
	if err != nil {
		log.Fatalf("failed to create application: %v", err)
	}

	if err := app.Start(ctx); err != nil {
		log.Fatalf("failed to start application: %v", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("failed to shutdown application: %v", err)
	}
}

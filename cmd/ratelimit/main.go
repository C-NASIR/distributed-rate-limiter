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
	args := os.Args[1:]
	command := ""
	if len(args) > 0 && args[0] == "print_config" {
		command = args[0]
		args = args[1:]
	}
	if len(args) > 0 && (args[0] == "help" || args[0] == "usage") {
		printUsage(os.Stdout)
		return
	}

	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printUsage(os.Stdout)
		return
	}

	cfg, err := ratelimit.LoadConfig(ratelimit.LoadOptions{Args: args})
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if command == "print_config" {
		if err := ratelimit.PrintConfig(os.Stdout, cfg); err != nil {
			log.Fatalf("failed to print config: %v", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := ratelimit.NewApplication(cfg)
	if err != nil {
		log.Fatalf("failed to create application: %v", err)
	}

	if err := app.Start(ctx); err != nil {
		log.Fatalf("failed to start application: %v", err)
	}

	<-ctx.Done()

	shutdownTimeout := 5 * time.Second
	if cfg != nil && cfg.DrainTimeout > 0 {
		shutdownTimeout = cfg.DrainTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := app.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("failed to shutdown application: %v", err)
	}
}

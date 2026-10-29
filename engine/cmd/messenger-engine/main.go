package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	globalLog "github.com/rs/zerolog/log"
	"messenger.local/engine/internal/engine"
)

func main() {
	dir := flag.String("data-dir", "", "Local profile directory")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "--data-dir is required")
		os.Exit(2)
	}
	// Upstream trace logs can contain plaintext and session data. Never enable them here.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	globalLog.Logger = zerolog.Nop()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); _ = os.Stdin.Close() }()
	if err := engine.Serve(ctx, os.Stdin, os.Stdout, *dir); err != nil {
		os.Exit(1)
	}
}

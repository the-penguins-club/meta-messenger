package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	globalLog "github.com/rs/zerolog/log"
	"messenger.local/engine/internal/engine"
	"messenger.local/engine/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("data-dir", "", "Required persistent profile directory")
	address := flag.String("listen", "127.0.0.1:8787", "HTTP address (use a TLS reverse proxy for remote access)")
	tokenFile := flag.String("token-file", "", "Bearer token file; generated in profile if absent")
	auto := flag.Bool("connect", true, "Connect a previously imported session on startup")
	flag.Parse()
	if *dir == "" {
		return fmt.Errorf("--data-dir is required")
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	if *tokenFile == "" {
		*tokenFile = filepath.Join(*dir, "service-token")
	}
	token, err := os.ReadFile(*tokenFile)
	if os.IsNotExist(err) {
		bytes := make([]byte, 32)
		if _, err = rand.Read(bytes); err != nil {
			return err
		}
		token = []byte(hex.EncodeToString(bytes))
		f, openErr := os.OpenFile(*tokenFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr != nil {
			return openErr
		}
		_, err = f.Write(token)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if err != nil {
		return err
	}
	secret := strings.TrimSpace(string(token))
	if len(secret) < 32 {
		return fmt.Errorf("token file must contain at least 32 characters")
	}
	zerolog.SetGlobalLevel(zerolog.Disabled)
	globalLog.Logger = zerolog.Nop()
	handler := service.New(secret)
	runtime, err := engine.OpenRuntime(*dir, handler.Publish)
	if err != nil {
		return err
	}
	defer runtime.Close()
	handler.Attach(runtime)
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 70 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); _ = server.Close() }()
	if *auto {
		go func() { runtime.Request(`{"id":"startup","method":"connect"}`) }()
	}
	fmt.Fprintf(os.Stderr, "Messenger service listening on %s; token file: %s\n", listener.Addr(), *tokenFile)
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

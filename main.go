package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"boot.dev/linko/internal/store"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()
	os.Exit(status)
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	kennyLoggins, closer, err := initializeLogger(os.Getenv("LINKO_LOG_FILE"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		return 1
	}
	defer func() {
		if err := closer(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close logger: %v\n", err)
		}
	}()

	st, err := store.New(dataDir, kennyLoggins)
	if err != nil {
		kennyLoggins.Error("failed to create store", "error", err)
		return 1
	}
	s := newServer(*st, httpPort, cancel, kennyLoggins)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	kennyLoggins.Debug("Linko is shutting down")

	if err := s.shutdown(shutdownCtx); err != nil {
		kennyLoggins.Error("failed to shutdown server", "error", err)
		return 1
	}
	if serverErr != nil {
		kennyLoggins.Error("server error", "error", serverErr)
		return 1
	}
	return 0
}

func initializeLogger(logFile string) (*slog.Logger, closeFunc, error) {
	debugLoggins := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, err
		}
		buffLoggins := bufio.NewWriterSize(f, 8192)
		infoLoggins := slog.NewJSONHandler(buffLoggins, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})

		close := func() error {
			if err := buffLoggins.Flush(); err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			return nil
		}
		canHeLoggins := slog.New(slog.NewMultiHandler(debugLoggins, infoLoggins))

		return canHeLoggins, close, nil
	}
	close := func() error {
		return nil
	}
	return slog.New(debugLoggins), close, nil
}

type closeFunc func() error

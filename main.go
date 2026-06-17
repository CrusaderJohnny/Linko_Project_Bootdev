package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"boot.dev/linko/internal/build"
	"boot.dev/linko/internal/linkoerr"
	"boot.dev/linko/internal/store"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	pkgerr "github.com/pkg/errors"
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
	debugLoggins := tint.NewHandler(os.Stderr, &tint.Options{
		Level:       slog.LevelDebug,
		ReplaceAttr: replaceAttr,
		NoColor:     !(isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())),
	})

	var handler slog.Handler = debugLoggins
	var closeFn closeFunc = func() error { return nil }

	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, err
		}

		buffLoggins := bufio.NewWriterSize(f, 8192)
		infoLoggins := slog.NewJSONHandler(buffLoggins, &slog.HandlerOptions{
			ReplaceAttr: replaceAttr,
		})

		closeFn = func() error {
			if err := buffLoggins.Flush(); err != nil {
				return err
			}
			return f.Close()
		}
		handler = slog.NewMultiHandler(debugLoggins, infoLoggins)
	}
	env := os.Getenv("ENV")
	hostname, _ := os.Hostname()

	logger := slog.New(handler).With(
		slog.String("git_sha", build.GitSHA),
		slog.String("build_time", build.BuildTime),
		slog.String("env", env),
		slog.String("hostname", hostname),
	)
	return logger, closeFn, nil
}

type closeFunc func() error

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == "error" {
		err, ok := a.Value.Any().(error)
		if !ok {
			return a
		}
		attrs := []slog.Attr{
			{Key: "message", Value: slog.StringValue(err.Error())},
		}

		attrs = append(attrs, linkoerr.Attrs(err)...)

		if stackErr, ok := errors.AsType[stackTracer](err); ok {
			attrs = append(attrs, slog.Attr{
				Key:   "stack_trace",
				Value: slog.StringValue(fmt.Sprintf("%+v", stackErr.StackTrace())),
			})
		}

		if multError, ok := errors.AsType[multiError](err); ok {
			var multAttrs []slog.Attr
			for i, err := range multError.Unwrap() {
				errAttrs := []slog.Attr{
					{Key: "message", Value: slog.StringValue(err.Error())},
				}
				errAttrs = append(errAttrs, linkoerr.Attrs(err)...)
				multAttrs = append(multAttrs, slog.GroupAttrs(fmt.Sprintf("error_%d", i+1), errAttrs...))
			}
			return slog.GroupAttrs("errors", multAttrs...)
		}
		return slog.GroupAttrs("error", attrs...)
	}
	return a
}

type stackTracer interface {
	error
	StackTrace() pkgerr.StackTrace
}

type multiError interface {
	error
	Unwrap() []error
}

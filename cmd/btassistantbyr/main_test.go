package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/config"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

func TestRunReportsMissingConfiguration(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run(
		context.Background(),
		[]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")},
		strings.NewReader(""),
		&output,
		&output,
	)
	if !errors.Is(err, config.ErrReadConfig) {
		t.Fatalf("run() error = %v, want ErrReadConfig", err)
	}
}

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run(context.Background(), []string{"-help"}, strings.NewReader(""), &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run() error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(output.String(), "-config") {
		t.Errorf("help output does not describe -config:\n%s", output.String())
	}
}

func TestRunVersionDoesNotLoadConfiguration(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := run(context.Background(), []string{"-version"}, strings.NewReader(""), &output, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if output.String() != version+"\n" {
		t.Errorf("version output = %q, want %q", output.String(), version+"\n")
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	t.Parallel()

	err := run(
		context.Background(),
		[]string{"config/config.yaml"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestAssembleUntilReachableWaitsAfterExhaustedNetworkFailure(t *testing.T) {
	t.Parallel()

	var attempts int
	result, err := assembleUntilReachable(context.Background(), time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)), func() (int, error) {
		attempts++
		if attempts == 1 {
			return 0, netretry.Transient(errors.New("temporarily unavailable"))
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("assembleUntilReachable() error = %v", err)
	}
	if result != 42 || attempts != 2 {
		t.Fatalf("result = %d, attempts = %d, want 42 and 2", result, attempts)
	}
}

func TestAssembleUntilReachableReturnsBusinessErrorImmediately(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("invalid configuration")
	var attempts int
	_, err := assembleUntilReachable(context.Background(), time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)), func() (int, error) {
		attempts++
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) || attempts != 1 {
		t.Fatalf("error = %v, attempts = %d, want business error after one attempt", err, attempts)
	}
}

func TestAssembleUntilReachableStopsWhileWaiting(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()
	_, err := assembleUntilReachable(ctx, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)), func() (int, error) {
		close(started)
		return 0, netretry.Transient(errors.New("temporarily unavailable"))
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("assembleUntilReachable() error = %v, want context.Canceled", err)
	}
}

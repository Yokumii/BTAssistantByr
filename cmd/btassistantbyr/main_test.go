package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/config"
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

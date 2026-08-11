package logging

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSystemFiltersColorsAndPersistsLogs(t *testing.T) {
	t.Parallel()

	var console bytes.Buffer
	system, err := New(Options{
		Level:   "info",
		Color:   ColorAlways,
		LogDir:  t.TempDir(),
		Console: &console,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger := system.Logger().With("component", "test")
	logger.Debug("hidden detail")
	logger.Info("started")
	logger.Warn("attention")
	logger.Error("failed")
	logPath := system.FilePath()
	if err := system.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	consoleText := console.String()
	for _, expected := range []string{"\x1b[32m", "\x1b[33m", "\x1b[31m", "level=INFO", "component=test"} {
		if !strings.Contains(consoleText, expected) {
			t.Errorf("console log does not contain %q:\n%s", expected, consoleText)
		}
	}
	if strings.Contains(consoleText, "hidden detail") {
		t.Errorf("console contains filtered DEBUG log: %s", consoleText)
	}

	fileContents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	fileText := string(fileContents)
	if strings.Contains(fileText, "\x1b[") || strings.Contains(fileText, "hidden detail") {
		t.Errorf("file log contains color or filtered DEBUG log: %s", fileText)
	}
	for _, expected := range []string{"level=INFO", "level=WARN", "level=ERROR", "component=test"} {
		if !strings.Contains(fileText, expected) {
			t.Errorf("file log does not contain %q:\n%s", expected, fileText)
		}
	}
}

func TestDebugLogsIncludeSourceAndAttributes(t *testing.T) {
	t.Parallel()

	var console bytes.Buffer
	system, err := New(Options{Level: "DEBUG", Color: ColorNever, LogDir: t.TempDir(), Console: &console})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	system.Logger().Debug("processing step", "operation", "preflight")
	if err := system.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for _, expected := range []string{"level=DEBUG", "source=", "operation=preflight"} {
		if !strings.Contains(console.String(), expected) {
			t.Errorf("DEBUG log does not contain %q:\n%s", expected, console.String())
		}
	}
	if strings.Contains(console.String(), "\x1b[") {
		t.Errorf("never color mode emitted ANSI: %s", console.String())
	}
}

func TestAutoColorDoesNotColorNonTerminalWriter(t *testing.T) {
	t.Parallel()

	var console bytes.Buffer
	system, err := New(Options{Level: "info", Color: ColorAuto, LogDir: t.TempDir(), Console: &console})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	system.Logger().Info("plain")
	if err := system.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if strings.Contains(console.String(), "\x1b[") {
		t.Errorf("auto color emitted ANSI for a buffer: %s", console.String())
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options Options
		wantErr error
	}{
		{name: "console", options: Options{Level: "info", Color: ColorAuto, LogDir: t.TempDir()}, wantErr: ErrConsoleRequired},
		{name: "directory", options: Options{Level: "info", Color: ColorAuto, Console: &bytes.Buffer{}}, wantErr: ErrLogDirRequired},
		{name: "level", options: Options{Level: "trace", Color: ColorAuto, LogDir: t.TempDir(), Console: &bytes.Buffer{}}, wantErr: ErrInvalidLevel},
		{name: "color", options: Options{Level: "info", Color: "sometimes", LogDir: t.TempDir(), Console: &bytes.Buffer{}}, wantErr: ErrInvalidColor},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.options)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

package logging

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestSystemFiltersColorsAndPersistsLogs(t *testing.T) {
	t.Parallel()

	var console bytes.Buffer
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5,
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
	for _, expected := range []string{"\x1b[32mINFO ", "\x1b[33mWARN ", "\x1b[31mERROR", "[test] started"} {
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

type countingLogValue struct {
	calls int
}

type attrsOwnershipHandler struct {
	slog.Handler
	attrs  []slog.Attr
	mutate bool
}

func (handler *attrsOwnershipHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handler.attrs = attrs
	if handler.mutate {
		attrs[0] = slog.String("field", "modified by first child")
	}
	return handler
}

func TestFanoutGivesEachChildOwnershipOfAttributes(t *testing.T) {
	first := &attrsOwnershipHandler{mutate: true}
	second := &attrsOwnershipHandler{}
	original := slog.String("field", "original")
	attrs := []slog.Attr{original}
	fanoutHandler{first, second}.WithAttrs(attrs)
	if !second.attrs[0].Equal(original) {
		t.Fatalf("first child modified second child's attributes: %v", second.attrs)
	}
	if !attrs[0].Equal(original) {
		t.Fatalf("first child modified caller's attributes: %v", attrs)
	}
	first.attrs[0] = slog.String("field", "modified after WithAttrs")
	if !second.attrs[0].Equal(original) {
		t.Fatalf("children retained the same attribute slice: %v", second.attrs)
	}
}

func (value *countingLogValue) LogValue() slog.Value {
	value.calls++
	return slog.IntValue(value.calls)
}

func TestSystemResolvesLogValuesOnceForBothOutputs(t *testing.T) {
	var console bytes.Buffer
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &console})
	if err != nil {
		t.Fatal(err)
	}
	value := &countingLogValue{}
	logger := system.Logger().With(slog.Group("bound", slog.Any("value", value)))
	logger.Info("first", slog.Group("record", slog.Group("nested", slog.Any("value", value))))
	logger.Info("second", "value", value)
	if err := system.Close(); err != nil {
		t.Fatal(err)
	}
	if value.calls != 3 {
		t.Fatalf("LogValue called %d times, want once per bound/record value (3)", value.calls)
	}
	contents, err := os.ReadFile(system.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{"console": console.String(), "file": string(contents)} {
		for _, want := range []string{"bound.value=1", "record.nested.value=2", "second", " value=3"} {
			if !strings.Contains(output, want) {
				t.Errorf("%s output missing %q: %s", name, want, output)
			}
		}
	}
}

func TestSystemReportsOutputFailureOnCloseAndContinuesOtherOutput(t *testing.T) {
	writeErr := errors.New("console unavailable")
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: consoleTestWriter{err: writeErr}})
	if err != nil {
		t.Fatal(err)
	}
	system.Logger().With("component", "test").WithGroup("operation").Info("still persisted", "id", 7)
	if err := system.Close(); !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "write console log") {
		t.Fatalf("Close() error = %v, want console write failure", err)
	}
	contents, err := os.ReadFile(system.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `msg="still persisted"`) || !strings.Contains(string(contents), "operation.id=7") {
		t.Fatalf("file output was skipped after console failure: %s", contents)
	}
}

func TestSystemReportsShortWriteOnClose(t *testing.T) {
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: consoleTestWriter{}})
	if err != nil {
		t.Fatal(err)
	}
	system.Logger().Info("message")
	if err := system.Close(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Close() error = %v, want short write", err)
	}
}

func TestSystemReportsFileWriteFailureOnClose(t *testing.T) {
	var console bytes.Buffer
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &console})
	if err != nil {
		t.Fatal(err)
	}
	if err := system.file.Close(); err != nil {
		t.Fatal(err)
	}
	system.Logger().Info("console still available")
	if err := system.Close(); !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "write file log") {
		t.Fatalf("Close() error = %v, want recorded file write failure", err)
	}
	if !strings.Contains(console.String(), "console still available") {
		t.Fatalf("console output was skipped: %s", console.String())
	}
}

func TestDebugLogsIncludeSourceAndAttributes(t *testing.T) {
	t.Parallel()

	var console bytes.Buffer
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "DEBUG", Color: ColorNever, LogDir: t.TempDir(), Console: &console})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	system.Logger().Debug("processing step", "operation", "preflight")
	if err := system.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for _, expected := range []string{"DEBUG", "(logging/logging_test.go:", "operation=preflight"} {
		if !strings.Contains(console.String(), expected) {
			t.Errorf("DEBUG log does not contain %q:\n%s", expected, console.String())
		}
	}
	if strings.Contains(console.String(), "\x1b[") {
		t.Errorf("never color mode emitted ANSI: %s", console.String())
	}
	if strings.Contains(console.String(), "source=") || strings.Contains(console.String(), "time=") {
		t.Errorf("console contains structured metadata: %s", console.String())
	}
	contents, err := os.ReadFile(system.FilePath())
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(contents), "source=") || !strings.Contains(string(contents), "level=DEBUG") {
		t.Errorf("file log lost structured debug metadata: %s", contents)
	}
}

func TestAutoColorDoesNotColorNonTerminalWriter(t *testing.T) {
	t.Parallel()

	var console bytes.Buffer
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorAuto, LogDir: t.TempDir(), Console: &console})
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
		{name: "console", options: Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorAuto, LogDir: t.TempDir()}, wantErr: ErrConsoleRequired},
		{name: "directory", options: Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorAuto, Console: &bytes.Buffer{}}, wantErr: ErrLogDirRequired},
		{name: "level", options: Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "trace", Color: ColorAuto, LogDir: t.TempDir(), Console: &bytes.Buffer{}}, wantErr: ErrInvalidLevel},
		{name: "color", options: Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: "sometimes", LogDir: t.TempDir(), Console: &bytes.Buffer{}}, wantErr: ErrInvalidColor},
		{name: "zero size", options: Options{Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &bytes.Buffer{}, MaxBackups: 5}, wantErr: ErrInvalidRotation},
		{name: "negative size", options: Options{Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &bytes.Buffer{}, MaxSizeBytes: -1, MaxBackups: 5}, wantErr: ErrInvalidRotation},
		{name: "zero backups", options: Options{Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &bytes.Buffer{}, MaxSizeBytes: 1024}, wantErr: ErrInvalidRotation},
		{name: "excessive backups", options: Options{Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &bytes.Buffer{}, MaxSizeBytes: 1024, MaxBackups: 101}, wantErr: ErrInvalidRotation},
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

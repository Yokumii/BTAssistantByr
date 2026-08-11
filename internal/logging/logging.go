// Package logging configures structured console and file logging.
package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const FileName = "btassistantbyr.log"

const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

var (
	ErrConsoleRequired = errors.New("log console output is required")
	ErrLogDirRequired  = errors.New("log directory is required")
	ErrInvalidLevel    = errors.New("invalid log level")
	ErrInvalidColor    = errors.New("invalid log color mode")
)

// Options controls both console and persistent file logs.
type Options struct {
	Level   string
	Color   string
	LogDir  string
	Console io.Writer
}

// System owns the logger and its persistent file.
type System struct {
	logger   *slog.Logger
	file     *os.File
	filePath string
}

// New creates a level-filtered console logger and an ANSI-free file logger.
func New(options Options) (*System, error) {
	if options.Console == nil {
		return nil, ErrConsoleRequired
	}
	options.LogDir = strings.TrimSpace(options.LogDir)
	if options.LogDir == "" {
		return nil, ErrLogDirRequired
	}
	level, err := parseLevel(options.Level)
	if err != nil {
		return nil, err
	}
	color, err := useColor(options.Color, options.Console)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(options.LogDir, 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	filePath := filepath.Join(options.LogDir, FileName)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	handlerOptions := &slog.HandlerOptions{
		AddSource: level == slog.LevelDebug,
		Level:     level,
	}
	consoleOutput := options.Console
	if color {
		consoleOutput = colorWriter{writer: options.Console}
	}
	consoleHandler := slog.NewTextHandler(consoleOutput, handlerOptions)
	fileHandler := slog.NewTextHandler(file, handlerOptions)
	return &System{
		logger:   slog.New(fanoutHandler{consoleHandler, fileHandler}),
		file:     file,
		filePath: filePath,
	}, nil
}

// Logger returns the configured structured logger.
func (system *System) Logger() *slog.Logger {
	return system.logger
}

// FilePath returns the persistent log destination.
func (system *System) FilePath() string {
	return system.filePath
}

// Close flushes and closes the persistent log file.
func (system *System) Close() error {
	if system.file == nil {
		return nil
	}
	syncErr := system.file.Sync()
	closeErr := system.file.Close()
	system.file = nil
	return errors.Join(syncErr, closeErr)
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidLevel, value)
	}
}

func useColor(mode string, output io.Writer) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ColorAlways:
		return true, nil
	case ColorNever:
		return false, nil
	case ColorAuto:
		file, ok := output.(*os.File)
		if !ok {
			return false, nil
		}
		info, err := file.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrInvalidColor, mode)
	}
}

type fanoutHandler []slog.Handler

func (handler fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, child := range handler {
		if child.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (handler fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var result error
	for _, child := range handler {
		if child.Enabled(ctx, record.Level) {
			result = errors.Join(result, child.Handle(ctx, record.Clone()))
		}
	}
	return result
}

func (handler fanoutHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	children := make(fanoutHandler, len(handler))
	for index, child := range handler {
		children[index] = child.WithAttrs(attributes)
	}
	return children
}

func (handler fanoutHandler) WithGroup(name string) slog.Handler {
	children := make(fanoutHandler, len(handler))
	for index, child := range handler {
		children[index] = child.WithGroup(name)
	}
	return children
}

type colorWriter struct {
	writer io.Writer
}

func (writer colorWriter) Write(payload []byte) (int, error) {
	color := levelColor(payload)
	if color == "" {
		return writer.writer.Write(payload)
	}
	colored := make([]byte, 0, len(color)+len(payload)+len(ansiReset))
	colored = append(colored, color...)
	colored = append(colored, payload...)
	colored = append(colored, ansiReset...)
	written, err := writer.writer.Write(colored)
	if written <= len(color) {
		return 0, err
	}
	written -= len(color)
	if written > len(payload) {
		written = len(payload)
	}
	return written, err
}

const ansiReset = "\x1b[0m"

func levelColor(payload []byte) string {
	switch {
	case bytes.Contains(payload, []byte(" level=DEBUG ")):
		return "\x1b[36m"
	case bytes.Contains(payload, []byte(" level=INFO ")):
		return "\x1b[32m"
	case bytes.Contains(payload, []byte(" level=WARN ")):
		return "\x1b[33m"
	case bytes.Contains(payload, []byte(" level=ERROR ")):
		return "\x1b[31m"
	default:
		return ""
	}
}

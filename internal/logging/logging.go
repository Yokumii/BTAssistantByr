// Package logging configures structured console and file logging.
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	ErrInvalidRotation = errors.New("log maximum size must be positive and backups must be between 1 and 100")
)

// Options controls both console and persistent file logs. MaxSizeBytes must be
// positive; MaxBackups must be between 1 and 100. The config loader supplies defaults.
type Options struct {
	Level        string
	Color        string
	LogDir       string
	Console      io.Writer
	MaxSizeBytes int64
	MaxBackups   int
}

// System owns the logger and its persistent file.
type System struct {
	logger   *slog.Logger
	file     *rotatingFile
	filePath string
	writes   *writeFailure
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
	if options.MaxSizeBytes <= 0 || options.MaxBackups < 1 || options.MaxBackups > 100 {
		return nil, ErrInvalidRotation
	}

	if err := os.MkdirAll(options.LogDir, 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	filePath := filepath.Join(options.LogDir, FileName)
	file, err := newRotatingFile(filePath, options.MaxSizeBytes, options.MaxBackups)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	handlerOptions := &slog.HandlerOptions{
		AddSource: level == slog.LevelDebug,
		Level:     level,
	}
	writes := &writeFailure{}
	consoleHandler := newConsoleHandler(logWriter{writer: options.Console, name: "console", failure: writes}, level, color)
	fileHandler := slog.NewTextHandler(logWriter{writer: file, name: "file", failure: writes}, handlerOptions)
	return &System{
		logger:   slog.New(fanoutHandler{consoleHandler, fileHandler}),
		file:     file,
		filePath: filePath,
		writes:   writes,
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

// Close reports the first output failure as well as flush and close errors.
func (system *System) Close() error {
	if system.file == nil {
		return system.writes.err()
	}
	closeErr := system.file.Close()
	return errors.Join(system.writes.err(), closeErr)
}

// slog discards Handler errors, so keep the first write failure for Close.
type writeFailure struct {
	mu    sync.Mutex
	first error
}

func (failure *writeFailure) err() error {
	failure.mu.Lock()
	defer failure.mu.Unlock()
	return failure.first
}

type logWriter struct {
	writer  io.Writer
	name    string
	failure *writeFailure
}

func (writer logWriter) Write(payload []byte) (int, error) {
	written, err := writer.writer.Write(payload)
	if err == nil && written != len(payload) {
		err = io.ErrShortWrite
	}
	if err != nil {
		err = fmt.Errorf("write %s log: %w", writer.name, err)
		writer.failure.mu.Lock()
		if writer.failure.first == nil {
			writer.failure.first = err
		}
		writer.failure.mu.Unlock()
	}
	return written, err
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
	resolved := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		resolved.AddAttrs(resolveAttr(attr))
		return true
	})
	var result error
	for _, child := range handler {
		if child.Enabled(ctx, record.Level) {
			result = errors.Join(result, child.Handle(ctx, resolved.Clone()))
		}
	}
	return result
}

func (handler fanoutHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	resolved := make([]slog.Attr, len(attributes))
	for index, attr := range attributes {
		resolved[index] = resolveAttr(attr)
	}
	children := make(fanoutHandler, len(handler))
	for index, child := range handler {
		// Each child owns its attribute slice and may retain or modify it.
		children[index] = child.WithAttrs(slices.Clone(resolved))
	}
	return children
}

// Resolve once before fan-out, including group members, so both outputs agree.
func resolveAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		members := attr.Value.Group()
		resolved := make([]slog.Attr, len(members))
		for index, member := range members {
			resolved[index] = resolveAttr(member)
		}
		attr.Value = slog.GroupValue(resolved...)
	}
	return attr
}

func (handler fanoutHandler) WithGroup(name string) slog.Handler {
	children := make(fanoutHandler, len(handler))
	for index, child := range handler {
		children[index] = child.WithGroup(name)
	}
	return children
}

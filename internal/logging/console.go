package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
)

type consoleOutput struct {
	mu     sync.Mutex
	writer io.Writer
}

type consoleHandler struct {
	output    *consoleOutput
	level     slog.Level
	color     bool
	group     string
	attrs     string
	component string
}

func newConsoleHandler(writer io.Writer, level slog.Level, color bool) *consoleHandler {
	return &consoleHandler{output: &consoleOutput{writer: writer}, level: level, color: color}
}

func (handler *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level
}

func (handler *consoleHandler) Handle(_ context.Context, record slog.Record) error {
	var attrs strings.Builder
	attrs.WriteString(handler.attrs)
	component := handler.component
	record.Attrs(func(attr slog.Attr) bool {
		appendConsoleAttr(&attrs, handler.group, attr, &component)
		return true
	})

	var line strings.Builder
	if !record.Time.IsZero() {
		writeStyled(&line, record.Time.Format("2006-01-02 15:04:05.000"), ansiDim, handler.color)
		line.WriteByte(' ')
	}
	writeStyled(&line, fmt.Sprintf("%-5s", record.Level), levelColor(record.Level), handler.color)
	line.WriteByte(' ')
	if component != "" {
		line.WriteByte('[')
		line.WriteString(consoleText(component, false))
		line.WriteString("] ")
	}
	line.WriteString(consoleText(record.Message, false))
	line.WriteString(attrs.String())
	if handler.level == slog.LevelDebug && record.PC != 0 {
		frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
		if frame.File != "" {
			source := filepath.Base(filepath.Dir(frame.File)) + "/" + filepath.Base(frame.File)
			line.WriteString("  ")
			writeStyled(&line, fmt.Sprintf("(%s:%d)", source, frame.Line), ansiDim, handler.color)
		}
	}
	line.WriteByte('\n')

	// Derived loggers share one lock so concurrent records remain whole lines.
	handler.output.mu.Lock()
	defer handler.output.mu.Unlock()
	payload := line.String()
	written, err := io.WriteString(handler.output.writer, payload)
	if err == nil && written != len(payload) {
		return io.ErrShortWrite
	}
	return err
}

func (handler *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *handler
	var formatted strings.Builder
	formatted.WriteString(handler.attrs)
	for _, attr := range attrs {
		appendConsoleAttr(&formatted, handler.group, attr, &clone.component)
	}
	clone.attrs = formatted.String()
	return &clone
}

func (handler *consoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	clone := *handler
	clone.group += name + "."
	return &clone
}

func appendConsoleAttr(output *strings.Builder, group string, attr slog.Attr, component *string) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		if attr.Key != "" {
			group += attr.Key + "."
		}
		for _, child := range attr.Value.Group() {
			appendConsoleAttr(output, group, child, component)
		}
		return
	}
	if group == "" && attr.Key == "component" && attr.Value.Kind() == slog.KindString {
		*component = attr.Value.String()
		return
	}
	key, value := attr.Key, attr.Value.String()
	if isCapacityKey(key) && attr.Value.Kind() == slog.KindInt64 {
		key = strings.TrimSuffix(key, "_bytes")
		value = fmt.Sprintf("%.2fGiB", float64(attr.Value.Int64())/(1<<30))
	}
	if attr.Value.Kind() == slog.KindTime {
		value = attr.Value.Time().Format(time.RFC3339Nano)
	}
	output.WriteString(" ")
	output.WriteString(consoleText(group+key, true))
	output.WriteByte('=')
	output.WriteString(consoleText(value, true))
}

// Only known capacity fields are converted; IDs and unrelated numeric fields stay exact.
func isCapacityKey(key string) bool {
	switch key {
	case "free_bytes", "reserve_bytes", "required_bytes", "max_torrent_size_bytes",
		"current_disk_bytes", "projected_disk_bytes", "initial_free_bytes",
		"projected_free_bytes", "projected_free_after_cleanup_bytes":
		return true
	default:
		return false
	}
}

func consoleText(value string, quoteSpaces bool) string {
	if value == "" && quoteSpaces {
		return strconv.Quote(value)
	}
	for _, char := range value {
		if unicode.IsControl(char) || (quoteSpaces && (unicode.IsSpace(char) || char == '=' || char == '"')) {
			return strconv.Quote(value)
		}
	}
	return value
}

func writeStyled(output *strings.Builder, value, color string, enabled bool) {
	if enabled {
		output.WriteString(color)
	}
	output.WriteString(value)
	if enabled {
		output.WriteString(ansiReset)
	}
}

func levelColor(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "\x1b[31m"
	case level >= slog.LevelWarn:
		return "\x1b[33m"
	case level >= slog.LevelInfo:
		return "\x1b[32m"
	default:
		return "\x1b[36m"
	}
}

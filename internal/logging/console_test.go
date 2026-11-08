package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConsoleRecordLayoutAndCapacity(t *testing.T) {
	var output bytes.Buffer
	handler := newConsoleHandler(&output, slog.LevelDebug, false)
	observedAt := time.Date(2026, 11, 6, 16, 59, 0, 742000000, time.FixedZone("UTC+8", 8*60*60))
	record := slog.NewRecord(observedAt, slog.LevelDebug, "management scope applied", 0)
	record.Add("component", "topfree_coordinator", "in_scope", 21, "free_bytes", int64(138512695296), "reserve_bytes", int64(20<<30), "other_bytes", 42)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	want := "2026-11-06 16:59:00.742 DEBUG [topfree_coordinator] management scope applied in_scope=21 free=129.00GiB reserve=20.00GiB other_bytes=42\n"
	if output.String() != want {
		t.Fatalf("console = %q, want %q", output.String(), want)
	}
}

func TestConsoleGroupsAndBoundAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, false)).With("component", "worker", "bound", 1)
	child := logger.WithGroup("request").With("id", 2)
	child.Info("child", slog.Group("details", "component", "nested", "free_bytes", int64(1<<30)), slog.Group("", "inline", true))
	logger.Info("parent", "component", "parent", slog.Attr{}, slog.Group("empty"))
	text := output.String()
	for _, want := range []string{
		"INFO  [worker] child bound=1 request.id=2 request.details.component=nested request.details.free=1.00GiB request.inline=true\n",
		"INFO  [parent] parent bound=1\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("console = %q, missing %q", text, want)
		}
	}
}

type consoleTestValue struct {
	value string
}

func (value *consoleTestValue) LogValue() slog.Value {
	return slog.StringValue(value.value)
}

func TestConsoleResolvesBoundValuesOnceAndEscapesControls(t *testing.T) {
	var output bytes.Buffer
	value := &consoleTestValue{value: "before"}
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, false)).With("value", value)
	value.value = "after"
	logger.Info("line\nbreak\x1b[31m", "path", "/downloads/my files", "error", errors.New("failed\nagain"), "separator", "a\u2028b\u202ec")
	text := output.String()
	if strings.Count(text, "\n") != 1 || strings.ContainsAny(text, "\x1b\u2028\u202e") {
		t.Fatalf("record contains unescaped controls: %q", text)
	}
	for _, want := range []string{`"line\nbreak\x1b[31m"`, "value=before", `path="/downloads/my files"`, `error="failed\nagain"`, `separator="a\u2028b\u202ec"`} {
		if !strings.Contains(text, want) {
			t.Errorf("console = %q, missing %q", text, want)
		}
	}
}

func TestConsoleCandidateCapacityFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, false))
	logger.Info("candidate", "size_bytes", int64(2<<30), "completed_bytes", int64(1<<30), "amount_left_bytes", int64(1<<30), "available_bytes", int64(3<<30))
	want := "size=2.00GiB completed=1.00GiB amount_left=1.00GiB available=3.00GiB"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("console = %q, missing readable candidate capacities %q", output.String(), want)
	}
}

func TestConsoleColorDoesNotCoverMessage(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, true))
	logger.With("component", "test").Info("plain message", "count", 2)
	if !strings.Contains(output.String(), "\x1b[32mINFO \x1b[0m [test] plain message count=2\n") {
		t.Fatalf("message or attributes were colored: %q", output.String())
	}
}

func TestConsoleDerivedLoggersSerializeWrites(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, false))
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			child := logger.With("component", fmt.Sprintf("worker-%d", id)).WithGroup("task")
			for item := 0; item < 20; item++ {
				child.Info("completed", "id", item)
			}
		}(worker)
	}
	workers.Wait()
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 160 {
		t.Fatalf("got %d records, want 160", len(lines))
	}
	for worker := 0; worker < 8; worker++ {
		for item := 0; item < 20; item++ {
			want := fmt.Sprintf("[worker-%d] completed task.id=%d\n", worker, item)
			if strings.Count(output.String(), want) != 1 {
				t.Errorf("record missing or duplicated: %q", want)
			}
		}
	}
}

type consoleTestWriter struct {
	err error
}

func (writer consoleTestWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func TestConsolePropagatesWriteErrors(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "failure", err: writeErr, want: writeErr},
		{name: "short write", want: io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newConsoleHandler(consoleTestWriter{err: test.err}, slog.LevelInfo, false)
			err := handler.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "message", 0))
			if !errors.Is(err, test.want) {
				t.Fatalf("Handle() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestFileRetainsRawCapacityFields(t *testing.T) {
	var console bytes.Buffer
	system, err := New(Options{MaxSizeBytes: 1 << 20, MaxBackups: 5, Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &console})
	if err != nil {
		t.Fatal(err)
	}
	system.Logger().Info("space", "free_bytes", int64(1<<30), "reserve_bytes", int64(0))
	if err := system.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(system.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "free_bytes=1073741824 reserve_bytes=0") {
		t.Fatalf("file lost raw capacity: %s", contents)
	}
	if !strings.Contains(console.String(), "free=1.00GiB reserve=0.00GiB") {
		t.Fatalf("console lost readable capacity: %s", console.String())
	}
}

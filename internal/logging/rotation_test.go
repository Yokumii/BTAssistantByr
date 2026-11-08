package logging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotationRetainsNewestRecordsAndResumesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	writer, err := newRotatingFile(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{"aaa\n", "bbb\n", "ccc\n", "ddd\n"} {
		writeLogRecord(t, writer, payload)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err = newRotatingFile(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, payload := range []string{"eee\n", "fff\n", "ggg\n"} {
		writeLogRecord(t, writer, payload)
	}
	for suffix, want := range map[string]string{"": "ggg\n", ".1": "eee\nfff\n", ".2": "ccc\nddd\n"} {
		assertLogContents(t, path+suffix, want)
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected extra backup: %v", err)
	}
}

func TestRotationArchivesOversizedExistingFileAndPrunesOnlyOwnedBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	for suffix, contents := range map[string]string{"": "legacy oversized log\n", ".1": "previous\n", ".3": "expired\n", ".99": "expired\n", ".01": "unrelated\n", ".notes": "unrelated\n"} {
		if err := os.WriteFile(path+suffix, []byte(contents), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := newRotatingFile(path, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	assertLogContents(t, path, "")
	assertLogContents(t, path+".1", "legacy oversized log\n")
	assertLogContents(t, path+".2", "previous\n")
	assertLogContents(t, path+".01", "unrelated\n")
	assertLogContents(t, path+".notes", "unrelated\n")
	for _, suffix := range []string{".3", ".99"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup %s was not pruned: %v", suffix, err)
		}
	}
	writeLogRecord(t, writer, "new\n")
	assertLogContents(t, path, "new\n")
}

func TestRotationPrunesBackupsWithoutRotatingSmallActiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, suffix := range []string{"", ".1", ".2"} {
		if err := os.WriteFile(path+suffix, []byte("old\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := newRotatingFile(path, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writeLogRecord(t, writer, "new\n")
	assertLogContents(t, path, "old\nnew\n")
	assertLogContents(t, path+".1", "old\n")
	if _, err := os.Stat(path + ".2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excess backup retained: %v", err)
	}
}

func TestRotationRejectsOversizedRecordWithoutChangingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	writer, err := newRotatingFile(path, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writeLogRecord(t, writer, "old\n")
	if n, err := writer.Write([]byte("oversized\n")); n != 0 || err == nil {
		t.Fatalf("oversized write = (%d, %v)", n, err)
	}
	assertLogContents(t, path, "old\n")
	if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized record unexpectedly rotated the file: %v", err)
	}
	writeLogRecord(t, writer, "new\n")
	assertLogContents(t, path, "new\n")
	assertLogContents(t, path+".1", "old\n")
}

func TestRotationSerializesConcurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	writer, err := newRotatingFile(path, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for index := range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			payload := fmt.Sprintf("%03d\n", index)
			if n, err := writer.Write([]byte(payload)); err != nil || n != len(payload) {
				t.Errorf("write = (%d, %v)", n, err)
			}
		}()
	}
	workers.Wait()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		contents, err := os.ReadFile(filepath.Join(filepath.Dir(path), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) > 16 {
			t.Fatalf("file %s exceeds size limit", entry.Name())
		}
		for _, record := range strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n") {
			if len(record) != 3 || seen[record] {
				t.Fatalf("partial or duplicate record %q", record)
			}
			seen[record] = true
		}
	}
	for index := range 32 {
		if !seen[fmt.Sprintf("%03d", index)] {
			t.Errorf("record %d missing", index)
		}
	}
}

func TestSystemReportsRotationFailureAndContinuesConsole(t *testing.T) {
	var console bytes.Buffer
	system, err := New(Options{Level: "info", Color: ColorNever, LogDir: t.TempDir(), Console: &console, MaxSizeBytes: 128, MaxBackups: 1})
	if err != nil {
		t.Fatal(err)
	}
	system.Logger().Info("first")
	// A nonempty directory at the oldest backup path makes removal fail on
	// every supported platform, without depending on permissions or root.
	backup := system.FilePath() + ".1"
	if err := os.Mkdir(backup, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "block"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		system.Logger().Info("next")
	}
	if err := system.Close(); err == nil || !strings.Contains(err.Error(), "rotate log file") || !strings.Contains(err.Error(), "write file log") {
		t.Fatalf("Close() = %v, want rotation write failure", err)
	}
	if strings.Count(console.String(), "next") != 4 {
		t.Fatalf("console stopped after rotation failure: %s", console.String())
	}
	assertLogContents(t, filepath.Join(backup, "block"), "keep")
	contents, err := os.ReadFile(system.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "first") || len(contents) > 128 {
		t.Fatalf("active file changed unexpectedly: %s", contents)
	}
}

func writeLogRecord(t *testing.T, writer *rotatingFile, payload string) {
	t.Helper()
	if n, err := writer.Write([]byte(payload)); err != nil || n != len(payload) {
		t.Fatalf("write = (%d, %v), want %d bytes", n, err, len(payload))
	}
}

func assertLogContents(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != want {
		t.Fatalf("%s = %q, want %q", path, contents, want)
	}
}

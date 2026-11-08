package logging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// rotatingFile serializes complete records with rotation and close. A failed
// rotation stops file writes so a retry cannot shift a partially moved archive.
type rotatingFile struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	size       int64
	maxSize    int64
	maxBackups int
	failure    error
}

func newRotatingFile(path string, maxSize int64, maxBackups int) (*rotatingFile, error) {
	writer := &rotatingFile{path: path, maxSize: maxSize, maxBackups: maxBackups}
	if err := writer.open(); err != nil {
		return nil, err
	}
	// Apply a reduced backup count even when the active file is still small.
	err := writer.pruneBackups()
	if err == nil && writer.size > writer.maxSize {
		err = writer.rotate()
	}
	if err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	return writer, nil
}

func (writer *rotatingFile) open() error {
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() {
		return errors.Join(fmt.Errorf("log destination must be a regular file"), file.Close())
	}
	writer.file = file
	writer.size = info.Size()
	return nil
}

func (writer *rotatingFile) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.failure != nil {
		return 0, writer.failure
	}
	if writer.file == nil {
		return 0, os.ErrClosed
	}
	if int64(len(payload)) > writer.maxSize {
		return 0, fmt.Errorf("log record size %d exceeds maximum file size %d bytes", len(payload), writer.maxSize)
	}
	if int64(len(payload)) > writer.maxSize-writer.size {
		if err := writer.rotate(); err != nil {
			writer.failure = fmt.Errorf("rotate log file: %w", err)
			return 0, writer.failure
		}
	}
	written, err := writer.file.Write(payload)
	writer.size += int64(written)
	return written, err
}

func (writer *rotatingFile) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.closeFile()
}

func (writer *rotatingFile) closeFile() error {
	if writer.file == nil {
		return nil
	}
	syncErr := writer.file.Sync()
	closeErr := writer.file.Close()
	writer.file = nil
	return errors.Join(syncErr, closeErr)
}

func (writer *rotatingFile) backupPath(index int) string {
	return writer.path + "." + strconv.Itoa(index)
}

func (writer *rotatingFile) rotate() error {
	if err := writer.closeFile(); err != nil {
		return err
	}
	if err := os.Remove(writer.backupPath(writer.maxBackups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := writer.maxBackups - 1; index >= 1; index-- {
		if err := os.Rename(writer.backupPath(index), writer.backupPath(index+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(writer.path, writer.backupPath(1)); err != nil {
		return err
	}
	return writer.open()
}

func (writer *rotatingFile) pruneBackups() error {
	entries, err := os.ReadDir(filepath.Dir(writer.path))
	if err != nil {
		return err
	}
	prefix := filepath.Base(writer.path) + "."
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok {
			continue
		}
		index, err := strconv.Atoi(suffix)
		if err != nil || index <= writer.maxBackups || strconv.Itoa(index) != suffix {
			continue
		}
		if err := os.Remove(filepath.Join(filepath.Dir(writer.path), entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

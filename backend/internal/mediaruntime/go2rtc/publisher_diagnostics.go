package go2rtc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	publisherLogFilename = "camera.log"
	publisherLogMaxBytes = 8 << 20
	publisherLogBackups  = 4
)

// publisherDiagnosticLog is shared by the camera kernel's stdout/stderr copy
// goroutines and Core lifecycle events. Write deliberately becomes a no-op
// after a filesystem error so a full or unavailable log volume cannot break
// the camera media process.
type publisherDiagnosticLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
	closed   bool
}

func openPublisherDiagnosticLog(directory string) (*publisherDiagnosticLog, error) {
	return openRotatingPublisherLog(
		filepath.Join(directory, publisherLogFilename),
		publisherLogMaxBytes,
		publisherLogBackups,
	)
}

func openRotatingPublisherLog(path string, maxBytes int64, backups int) (*publisherDiagnosticLog, error) {
	if maxBytes <= 0 || backups < 1 {
		return nil, fmt.Errorf("%w: invalid camera diagnostic log rotation", ErrInvalidConfig)
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: camera diagnostic log path is not a regular file", ErrInvalidConfig)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect camera diagnostic log: %w", err)
	}
	file, size, err := openPublisherLogFile(path)
	if err != nil {
		return nil, err
	}
	return &publisherDiagnosticLog{
		path: path, maxBytes: maxBytes, backups: backups, file: file, size: size,
	}, nil
}

func openPublisherLogFile(path string) (*os.File, int64, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("open camera diagnostic log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("secure camera diagnostic log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("inspect camera diagnostic log: %w", err)
	}
	return file, info.Size(), nil
}

func (l *publisherDiagnosticLog) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *publisherDiagnosticLog) Write(payload []byte) (int, error) {
	if l == nil {
		return len(payload), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return len(payload), nil
	}
	if l.size > 0 && l.size+int64(len(payload)) > l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			if l.file != nil {
				_ = l.file.Close()
			}
			l.file = nil
			return len(payload), nil
		}
	}
	written, err := l.file.Write(payload)
	l.size += int64(written)
	if err != nil || written != len(payload) {
		_ = l.file.Close()
		l.file = nil
	}
	// Logging must never turn a healthy camera into a failed child process.
	return len(payload), nil
}

func (l *publisherDiagnosticLog) Event(level, message string, fields map[string]any) {
	if l == nil {
		return
	}
	event := make(map[string]any, len(fields)+4)
	event["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	event["level"] = level
	event["component"] = "homeloom-core"
	event["msg"] = message
	for key, value := range fields {
		event[key] = value
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}
	encoded = append(encoded, '\n')
	_, _ = l.Write(encoded)
}

func (l *publisherDiagnosticLog) rotateLocked() error {
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	for index := l.backups - 1; index >= 1; index-- {
		source := l.path + "." + strconv.Itoa(index)
		destination := l.path + "." + strconv.Itoa(index+1)
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
	}
	firstBackup := l.path + ".1"
	if err := os.Remove(firstBackup); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(l.path, firstBackup); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, size, err := openPublisherLogFile(l.path)
	if err != nil {
		return err
	}
	l.file = file
	l.size = size
	return nil
}

func (l *publisherDiagnosticLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/platform/subprocesslog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestParseMainLogLevel(t *testing.T) {
	for input, want := range map[string]zapcore.Level{"debug": zapcore.DebugLevel, "info": zapcore.InfoLevel, "warn": zapcore.WarnLevel, "error": zapcore.ErrorLevel} {
		got, err := parseMainLogLevel(input)
		if err != nil || got != want {
			t.Fatalf("parseMainLogLevel(%q) = %v, %v", input, got, err)
		}
	}
	if _, err := parseMainLogLevel("trace"); err == nil {
		t.Fatal("parseMainLogLevel accepted trace")
	}
}

func TestRuntimeLoggerMirrorsRedactedMainProcessLogs(t *testing.T) {
	logs := subprocesslog.New(5)
	var terminal bytes.Buffer
	logger := newRuntimeLogger(zapcore.DebugLevel, &terminal, logs)
	logger.Info("provider token=plain", zap.String("password", "secret"))
	entries := logs.Snapshot(0, 5)
	if len(entries) != 1 || entries[0].Process != "backend" || entries[0].Instance != "main" || entries[0].Message != "provider token=********" {
		t.Fatalf("runtime entries = %#v", entries)
	}
	for _, secret := range []string{"plain", "secret"} {
		if strings.Contains(terminal.String(), secret) || strings.Contains(entries[0].Message, secret) {
			t.Fatalf("secret %q leaked into runtime log: terminal=%s entry=%#v", secret, terminal.String(), entries[0])
		}
	}
}

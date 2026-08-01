package main

import (
	"go.uber.org/zap/zapcore"
	"testing"
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

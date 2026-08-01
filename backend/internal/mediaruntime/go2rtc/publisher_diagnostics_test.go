package go2rtc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPublisherDiagnosticLogRotatesAndSecuresFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), publisherLogFilename)
	log, err := openRotatingPublisherLog(path, 12, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"first\n", "second\n", "third\n", "fourth\n"} {
		if _, err := log.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	for name, expected := range map[string]string{
		publisherLogFilename:        "fourth\n",
		publisherLogFilename + ".1": "third\n",
		publisherLogFilename + ".2": "second\n",
	} {
		logPath := filepath.Join(filepath.Dir(path), name)
		content, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(content) != expected {
			t.Fatalf("%s = %q, want %q", name, content, expected)
		}
		info, err := os.Stat(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("rotation retained more than two backups: %v", err)
	}
}

func TestPublisherDiagnosticLogSerializesConcurrentCoreEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), publisherLogFilename)
	log, err := openRotatingPublisherLog(path, 1<<20, 2)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()
			log.Event("debug", "camera diagnostic test", map[string]any{"sequence": value})
		}(index)
	}
	wait.Wait()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 32 {
		t.Fatalf("diagnostic event count = %d, want 32", len(lines))
	}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid concurrent JSON event %q: %v", line, err)
		}
		if event["component"] != "homeloom-core" {
			t.Fatalf("component = %#v", event["component"])
		}
	}
}

func TestPublisherConfigCapturesDetailedRedactedDiagnostics(t *testing.T) {
	config := publisherTestConfig(t.TempDir())
	text := publisherYAML(config)
	required := []string{
		"log:\n",
		"  output: stdout\n",
		"  format: json\n",
		"  level: debug\n",
		"  time: UNIXMS\n",
		"  ffmpeg: info\n",
		"  global: \"-hide_banner -nostats\"\n",
		"${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}",
		"${HOMELOOM_HAP_PIN_CAMERA_MAIN}",
		"${HOMELOOM_HAP_PRIVATE_CAMERA_MAIN}",
	}
	for _, value := range required {
		if !strings.Contains(text, value) {
			t.Fatalf("publisher config missing %q:\n%s", value, text)
		}
	}
	for _, secret := range []string{
		config.Secrets.HomeKitPIN,
		config.Secrets.HomeKitDevicePrivate,
		config.Xiaomi.URI(),
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("publisher diagnostic config persisted secret %q", secret)
		}
	}

	legacy := "app:\n  modules: [api, rtsp, srtp, homekit, xiaomi, streams]\nlog:\n  output: stderr\n  level: info\napi:\n  allow_paths: [/pair-setup, /pair-verify]\nstreams:\n"
	upgraded := applyPublisherLogConfig(legacy)
	upgraded = applyPublisherLogConfig(upgraded)
	if strings.Count(upgraded, "\nlog:\n") != 1 || strings.Count(upgraded, "  level: debug\n") != 1 {
		t.Fatalf("publisher diagnostic upgrade is not idempotent:\n%s", upgraded)
	}
}

func TestPublisherDiagnosticLogRejectsNonRegularPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), publisherLogFilename)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openRotatingPublisherLog(path, 1024, 1); err == nil {
		t.Fatal("non-regular diagnostic log path was accepted")
	}
}

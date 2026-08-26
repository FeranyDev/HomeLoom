package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareMCPAgentRuntimeCreatesPrivateRotatingToken(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "mcp")
	firstToken, aiConfig, err := prepareMCPAgentRuntime(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(firstToken)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(firstToken)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || len(strings.TrimSpace(string(first))) < 24 {
		t.Fatalf("token mode/content = %o/%q", info.Mode().Perm(), first)
	}
	if aiConfig != filepath.Join(runtimeDir, mcpAgentAIConfigName) {
		t.Fatalf("AI config path = %q", aiConfig)
	}
	secondToken, _, err := prepareMCPAgentRuntime(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondToken)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("MCP Agent token was not rotated")
	}
}

func TestPrepareMCPAgentRuntimeRejectsPublicDirectory(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "mcp")
	if err := os.Mkdir(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareMCPAgentRuntime(runtimeDir); err == nil {
		t.Fatal("prepareMCPAgentRuntime accepted a public directory")
	}
}

func TestMCPAgentCommandPassesOnlyPrivateFileReferences(t *testing.T) {
	command := mcpAgentCommand(context.Background(), "/bin/agent", "/data/mcp/core.sock", "127.0.0.1:8091", "/data/mcp/agent.token", "/data/mcp/ai-config.json", false)
	got := strings.Join(command.Args, " ")
	for _, expected := range []string{"--core-socket /data/mcp/core.sock", "--listen 127.0.0.1:8091", "--auth-token-file /data/mcp/agent.token", "--ai-config-file /data/mcp/ai-config.json", "--mcp-http-enabled=false"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("command %q does not contain %q", got, expected)
		}
	}
}

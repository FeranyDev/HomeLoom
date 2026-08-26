package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"github.com/feranydev/homeloom/backend/internal/mcpagent"
	"go.uber.org/zap"
)

const (
	mcpAgentTokenName    = "agent.token"
	mcpAgentAIConfigName = "ai-config.json"
)

// embeddedMCPAgent is a Core-managed child process, analogous to the camera
// kernel. Keeping it as a process isolates the AI/MCP HTTP surface and private
// credentials without requiring a second container or a second lifecycle.
type embeddedMCPAgent struct {
	control *mcpagent.AgentControlClient
	cancel  context.CancelFunc
	exited  chan struct{}

	mu         sync.RWMutex
	processErr error
}

type embeddedMCPAgentConfig struct {
	Binary         string
	SocketPath     string
	RuntimeDir     string
	ListenAddress  string
	MCPHTTPEnabled bool
	LogWriter      io.Writer
}

func newEmbeddedMCPAgent(parent context.Context, config embeddedMCPAgentConfig, logger *zap.Logger) (*embeddedMCPAgent, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("module", "mcp-agent"))
	if strings.TrimSpace(config.SocketPath) == "" || strings.TrimSpace(config.RuntimeDir) == "" || strings.TrimSpace(config.ListenAddress) == "" {
		return nil, errors.New("embedded MCP Agent socket, runtime directory, and listener are required")
	}
	host, port, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("parse MCP Agent listener: %w", err)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() || port == "" {
		return nil, errors.New("embedded MCP Agent listener must use a loopback IP address")
	}
	tokenFile, aiConfigFile, err := prepareMCPAgentRuntime(config.RuntimeDir)
	if err != nil {
		return nil, err
	}
	baseURL := "http://" + net.JoinHostPort(host, port)
	control, err := mcpagent.NewAgentControlClient(baseURL, tokenFile)
	if err != nil {
		return nil, err
	}
	childCtx, cancel := context.WithCancel(parent)
	command := mcpAgentCommand(childCtx, resolveBundledExecutable(config.Binary), config.SocketPath, config.ListenAddress, tokenFile, aiConfigFile, config.MCPHTTPEnabled)
	if config.LogWriter != nil {
		command.Stdout, command.Stderr = config.LogWriter, config.LogWriter
	} else {
		command.Stdout, command.Stderr = io.Discard, io.Discard
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start embedded MCP Agent: %w", err)
	}
	result := &embeddedMCPAgent{control: control, cancel: cancel, exited: make(chan struct{})}
	go func() {
		err := command.Wait()
		if flusher, ok := config.LogWriter.(interface{ Flush() }); ok {
			flusher.Flush()
		}
		result.mu.Lock()
		result.processErr = err
		result.mu.Unlock()
		close(result.exited)
	}()
	if err := result.waitForReady(parent, baseURL); err != nil {
		_ = result.Close()
		return nil, err
	}
	logger.Info("embedded AI Agent enabled", zap.String("listen_address", config.ListenAddress), zap.Bool("mcp_http_enabled", config.MCPHTTPEnabled))
	return result, nil
}

func prepareMCPAgentRuntime(runtimeDir string) (tokenFile, aiConfigFile string, err error) {
	runtimeDir = filepath.Clean(strings.TrimSpace(runtimeDir))
	if runtimeDir == "." || runtimeDir == "" {
		return "", "", errors.New("MCP Agent runtime directory must not be the working directory")
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create MCP Agent runtime directory: %w", err)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return "", "", fmt.Errorf("inspect MCP Agent runtime directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", "", errors.New("MCP Agent runtime directory must be private (mode 0700)")
	}
	tokenFile = filepath.Join(runtimeDir, mcpAgentTokenName)
	if err := writeMCPAgentToken(tokenFile); err != nil {
		return "", "", err
	}
	return tokenFile, filepath.Join(runtimeDir, mcpAgentAIConfigName), nil
}

func writeMCPAgentToken(path string) error {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("generate MCP Agent token: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agent-token-")
	if err != nil {
		return fmt.Errorf("create MCP Agent token: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect MCP Agent token: %w", err)
	}
	if _, err := temporary.WriteString(base64.RawURLEncoding.EncodeToString(random) + "\n"); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write MCP Agent token: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync MCP Agent token: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close MCP Agent token: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("install MCP Agent token: %w", err)
	}
	return nil
}

func mcpAgentCommand(ctx context.Context, binary, socketPath, listenAddress, tokenFile, aiConfigFile string, mcpHTTPEnabled bool) *exec.Cmd {
	return exec.CommandContext(ctx, binary,
		"--core-socket", socketPath,
		"--listen", listenAddress,
		"--auth-token-file", tokenFile,
		"--ai-config-file", aiConfigFile,
		"--mcp-http-enabled="+strconv.FormatBool(mcpHTTPEnabled),
	)
}

func (r *embeddedMCPAgent) waitForReady(parent context.Context, baseURL string) error {
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		request, err := http.NewRequestWithContext(parent, http.MethodGet, baseURL+"/health", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-parent.Done():
			return parent.Err()
		case <-r.exited:
			if err := r.Err(); err != nil {
				return fmt.Errorf("embedded MCP Agent stopped before readiness: %w", err)
			}
			return errors.New("embedded MCP Agent stopped before readiness")
		case <-timeout.C:
			return errors.New("embedded MCP Agent did not become ready within 10 seconds")
		case <-ticker.C:
		}
	}
}

func (r *embeddedMCPAgent) Err() error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.processErr
}

func (r *embeddedMCPAgent) Control() *mcpagent.AgentControlClient {
	if r == nil {
		return nil
	}
	return r.control
}

func (r *embeddedMCPAgent) Done() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.exited
}

func (r *embeddedMCPAgent) Close() error {
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.exited:
		// CommandContext reports the expected signal termination as an error.
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("embedded MCP Agent did not stop within 5 seconds")
	}
}

type mcpAgentAutomationRunner struct{ agent *mcpagent.AgentControlClient }

func (r mcpAgentAutomationRunner) StartAutomation(ctx context.Context, input application.AIAutomationInvocation) (application.AIAutomationRun, error) {
	request := mcpagent.RunRequest{Message: input.Prompt, Context: mcpagent.RunContext{Source: mcpagent.RunSource(input.Source), AutomationID: input.AutomationID, AutomationName: input.AutomationName}}
	if input.Trigger != nil {
		trigger := mcpagent.NewTriggerContext(*input.Trigger)
		request.Context.Trigger = &trigger
	}
	run, err := r.agent.StartAIRunWithContext(ctx, request)
	if err != nil {
		return application.AIAutomationRun{}, err
	}
	return automationRun(run), nil
}

func (r mcpAgentAutomationRunner) ApproveAutomation(ctx context.Context, id string, unattended bool) (application.AIAutomationRun, error) {
	var (
		run mcpagent.Run
		err error
	)
	if unattended {
		run, err = r.agent.ApproveUnattendedAIRun(ctx, id)
	} else {
		run, err = r.agent.ApproveAIRun(ctx, id)
	}
	if err != nil {
		return application.AIAutomationRun{}, err
	}
	return automationRun(run), nil
}

func automationRun(run mcpagent.Run) application.AIAutomationRun {
	result := application.AIAutomationRun{ID: run.ID, Status: string(run.Status), Message: run.Message, CreatedAt: run.CreatedAt, ExpiresAt: run.ExpiresAt}
	if run.Action != nil {
		action := aiautomation.Action{PropertyPath: run.Action.PropertyPath, Value: run.Action.Value, ExpectedStateVersion: run.Action.ExpectedStateVersion, DeviceName: run.Action.DeviceName, PropertyName: run.Action.PropertyName, UsageNote: run.Action.UsageNote}
		result.Action = &action
	}
	return result
}

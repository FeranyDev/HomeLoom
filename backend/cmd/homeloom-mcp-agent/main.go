package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mcpagent"
)

func main() {
	coreSocket := flag.String("core-socket", env("HOMELOOM_MCP_SOCKET", "./data/mcp/core.sock"), "private HomeLoom Core Unix socket")
	listenAddress := flag.String("listen", env("HOMELOOM_MCP_AGENT_LISTEN", "127.0.0.1:8091"), "MCP Agent HTTP listen address")
	defaultMCPHTTPEnabled, err := boolEnv("HOMELOOM_MCP_HTTP_ENABLED", false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-agent configuration failed:", err)
		os.Exit(2)
	}
	mcpHTTPEnabled := flag.Bool("mcp-http-enabled", defaultMCPHTTPEnabled, "enable the optional external MCP JSON-RPC endpoint")
	defaultTokenFile := env("HOMELOOM_MCP_AGENT_TOKEN_FILE", "")
	tokenFile := flag.String("auth-token-file", defaultTokenFile, "file containing the MCP Agent bearer token")
	modelName := flag.String("ai-model", envWithLegacy("HOMELOOM_AI_MODEL", "HOMELOOM_OPENAI_MODEL", ""), "AI model used for Agent runs; leave empty for MCP read-only mode")
	baseURL := flag.String("ai-api-base-url", envWithLegacy("HOMELOOM_AI_API_BASE_URL", "HOMELOOM_OPENAI_BASE_URL", "https://api.openai.com/v1"), "OpenAI-compatible Responses API base URL")
	legacyModelName := flag.String("openai-model", "", "deprecated alias for -ai-model")
	legacyBaseURL := flag.String("openai-base-url", "", "deprecated alias for -ai-api-base-url")
	aiConfigFile := flag.String("ai-config-file", env("HOMELOOM_AI_CONFIG_FILE", filepath.Join(filepath.Dir(defaultTokenFile), "ai-config.json")), "private persisted AI service configuration")
	flag.Parse()
	if strings.TrimSpace(*legacyModelName) != "" {
		*modelName = strings.TrimSpace(*legacyModelName)
	}
	if strings.TrimSpace(*legacyBaseURL) != "" {
		*baseURL = strings.TrimSpace(*legacyBaseURL)
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-agent configuration failed:", err)
		os.Exit(2)
	}
	runtime, err := mcpagent.NewRuntime(*coreSocket, token, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-agent configuration failed:", err)
		os.Exit(2)
	}
	runtime.SetMCPHTTPEnabled(*mcpHTTPEnabled)
	store, err := mcpagent.NewFileAIConfigStore(*aiConfigFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-agent AI configuration failed:", err)
		os.Exit(2)
	}
	initial := mcpagent.AIServiceConfig{APIKey: envWithLegacy("HOMELOOM_AI_API_KEY", "HOMELOOM_OPENAI_API_KEY", ""), Model: *modelName, APIBaseURL: *baseURL}
	if err := runtime.ConfigureAIService(initial, store, func(config mcpagent.AIServiceConfig) (mcpagent.Model, error) {
		return mcpagent.NewAIServiceModel(config)
	}); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-agent AI configuration failed:", err)
		os.Exit(2)
	}
	server := &http.Server{Addr: *listenAddress, Handler: runtime.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: mcpagent.AIRunControlTimeout + 30*time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Fprintf(os.Stderr, "HomeLoom MCP Agent listening on %s\n", *listenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "mcp-agent stopped:", err)
		os.Exit(1)
	}
}

func envWithLegacy(primary, legacy, fallback string) string {
	if value := env(primary, ""); value != "" {
		return value
	}
	return env(legacy, fallback)
}

func env(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolEnv(key string, fallback bool) (bool, error) {
	value, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return enabled, nil
}

func readToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("-auth-token-file or HOMELOOM_MCP_AGENT_TOKEN_FILE is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("token file must be a private regular file (mode 0600)")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	token := strings.TrimSpace(string(content))
	if len(token) < 24 {
		return "", errors.New("token file must contain at least 24 characters")
	}
	return token, nil
}

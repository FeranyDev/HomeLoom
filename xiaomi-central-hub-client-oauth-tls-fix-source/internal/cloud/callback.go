package cloud

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type callbackResult struct {
	code string
	err  error
}

// CallbackServer receives the OAuth redirect on a local HTTP listener.
type CallbackServer struct {
	server *http.Server
	ln     net.Listener
	result chan callbackResult
	once   sync.Once
}

func StartCallbackServer(listenAddress, redirectURL, expectedState string) (*CallbackServer, error) {
	redirect, err := url.Parse(redirectURL)
	if err != nil || redirect.Path == "" {
		if err == nil {
			redirect.Path = "/"
		} else {
			return nil, fmt.Errorf("parse redirect URL: %w", err)
		}
	}
	ln, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth callback on %s: %w", listenAddress, err)
	}
	callback := &CallbackServer{
		ln:     ln,
		result: make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		result := callbackResult{}
		if oauthError := strings.TrimSpace(query.Get("error")); oauthError != "" {
			description := strings.TrimSpace(query.Get("error_description"))
			if description != "" {
				result.err = fmt.Errorf("OAuth authorization failed: %s: %s", oauthError, description)
			} else {
				result.err = fmt.Errorf("OAuth authorization failed: %s", oauthError)
			}
		} else if query.Get("state") != expectedState {
			result.err = errors.New("OAuth callback state mismatch")
		} else if result.code = strings.TrimSpace(query.Get("code")); result.code == "" {
			result.err = errors.New("OAuth callback does not contain an authorization code")
		}
		callback.once.Do(func() { callback.result <- result })

		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		if result.err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(writer, "<!doctype html><meta charset=utf-8><title>授权失败</title><h1>授权失败</h1><p>%s</p>", html.EscapeString(result.err.Error()))
			return
		}
		_, _ = writer.Write([]byte("<!doctype html><meta charset=utf-8><title>授权完成</title><h1>授权完成</h1><p>可以关闭此页面并返回终端。</p>"))
	})
	callback.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if serveErr := callback.server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			callback.once.Do(func() { callback.result <- callbackResult{err: serveErr} })
		}
	}()
	return callback, nil
}

func (s *CallbackServer) Wait(ctx context.Context) (string, error) {
	select {
	case result := <-s.result:
		return result.code, result.err
	case <-ctx.Done():
		return "", fmt.Errorf("wait for OAuth callback: %w", ctx.Err())
	}
}

func (s *CallbackServer) Close(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

package application

import (
	"context"
	"strings"
)

type correlationIDKey struct{}

func WithCorrelationID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if len(id) > 128 {
		id = id[:128]
	}
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey{}, id)
}

func CorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(correlationIDKey{}).(string)
	return id
}

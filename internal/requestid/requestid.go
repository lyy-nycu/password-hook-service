package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type contextKey struct{}

func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func From(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

package logger

import (
	"context"
	"log/slog"
	"strings"
)

func MaskAttrs(attrs ...slog.Attr) []slog.Attr {
	masked := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		masked = append(masked, maskAttr(attr))
	}
	return masked
}

type MaskingHandler struct {
	next slog.Handler
}

func NewMaskingHandler(next slog.Handler) slog.Handler {
	return MaskingHandler{next: next}
}

func (h MaskingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h MaskingHandler) Handle(ctx context.Context, record slog.Record) error {
	masked := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		masked.AddAttrs(maskAttr(attr))
		return true
	})
	return h.next.Handle(ctx, masked)
}

func (h MaskingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return MaskingHandler{next: h.next.WithAttrs(MaskAttrs(attrs...))}
}

func (h MaskingHandler) WithGroup(name string) slog.Handler {
	return MaskingHandler{next: h.next.WithGroup(name)}
}

func maskAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, "****")
	}
	if attr.Value.Kind() == slog.KindGroup {
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(MaskAttrs(attr.Value.Group()...)...)}
	}
	return attr
}

func isSensitiveKey(key string) bool {
	switch strings.ToLower(key) {
	case "password", "passwd", "secret":
		return true
	default:
		return false
	}
}

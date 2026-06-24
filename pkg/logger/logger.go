package logger

import (
	"log/slog"
	"strings"
)

func MaskAttrs(attrs ...slog.Attr) []slog.Attr {
	masked := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if isSensitiveKey(attr.Key) {
			masked = append(masked, slog.String(attr.Key, "****"))
			continue
		}
		masked = append(masked, attr)
	}
	return masked
}

func isSensitiveKey(key string) bool {
	switch strings.ToLower(key) {
	case "password", "passwd", "secret":
		return true
	default:
		return false
	}
}

package logger

import (
	"log/slog"
	"testing"
)

func TestMaskAttrsMasksSensitiveKeys(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{
		slog.String("cn", "311551001"),
		slog.String("password", "cleartext"),
		slog.String("passwd", "cleartext"),
		slog.String("secret", "client-secret"),
	}

	got := MaskAttrs(attrs...)

	values := map[string]string{}
	for _, attr := range got {
		values[attr.Key] = attr.Value.String()
	}

	if values["cn"] != "311551001" {
		t.Fatalf("cn = %q, want original value", values["cn"])
	}
	for _, key := range []string{"password", "passwd", "secret"} {
		if values[key] != "****" {
			t.Fatalf("%s = %q, want masked value", key, values[key])
		}
	}
}

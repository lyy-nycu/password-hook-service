package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestMaskAttrsMasksSensitiveKeys(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{
		slog.String("cn", "311551001"),
		slog.String("password", "cleartext"),
		slog.String("passwd", "cleartext"),
		slog.String("secret", "client-secret"),
		slog.String("passwordCiphertext", "ciphertext"),
		slog.String("password_nonce", "nonce"),
		slog.String("GRAPH_CLIENT_SECRET", "client-secret"),
		slog.String("clientSecret", "client-secret"),
		slog.String("access_token", "token"),
		slog.String("requestNonce", "nonce-for-replay-defense"),
	}

	got := MaskAttrs(attrs...)

	values := map[string]string{}
	for _, attr := range got {
		values[attr.Key] = attr.Value.String()
	}

	if values["cn"] != "311551001" {
		t.Fatalf("cn = %q, want original value", values["cn"])
	}
	if values["requestNonce"] != "nonce-for-replay-defense" {
		t.Fatalf("requestNonce = %q, want nonce preserved", values["requestNonce"])
	}
	for _, key := range []string{"password", "passwd", "secret", "passwordCiphertext", "password_nonce", "GRAPH_CLIENT_SECRET", "clientSecret", "access_token"} {
		if values[key] != "****" {
			t.Fatalf("%s = %q, want masked value", key, values[key])
		}
	}
}

func TestMaskingHandlerMasksSensitiveAttrs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	handler := NewMaskingHandler(slog.NewJSONHandler(&buf, nil))
	log := slog.New(handler)

	log.Info("event", slog.String("password", "cleartext"), slog.String("cn", "311551001"))

	output := buf.String()
	if strings.Contains(output, "cleartext") {
		t.Fatalf("log output leaked password: %s", output)
	}
	if !strings.Contains(output, `"password":"****"`) {
		t.Fatalf("log output did not include masked password: %s", output)
	}
}

package migration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPasswordSyncMessageRoundTripsEventType(t *testing.T) {
	msg := PasswordSyncMessage{
		CN:        "jdoe",
		UPN:       "jdoe@example.edu",
		EventType: EventLoginBootstrap,
		TraceID:   "trace-123",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded PasswordSyncMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.EventType != EventLoginBootstrap {
		t.Errorf("decoded.EventType = %q, want %q", decoded.EventType, EventLoginBootstrap)
	}
	if decoded.TraceID != "trace-123" {
		t.Errorf("decoded.TraceID = %q, want trace-123", decoded.TraceID)
	}
}

func TestPasswordSyncMessageDecodesWithoutEventType(t *testing.T) {
	// Legacy messages already sitting in the queue at deploy time won't have
	// an eventType field. Decoding must not error, and EventType must be the
	// empty string rather than some invalid sentinel.
	legacyJSON := `{"cn":"jdoe","upn":"jdoe@example.edu"}`

	var decoded PasswordSyncMessage
	if err := json.Unmarshal([]byte(legacyJSON), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.EventType != EventType("") {
		t.Errorf("decoded.EventType = %q, want empty string", decoded.EventType)
	}
}

func TestPasswordSyncMessageDecodeAllowsMissingTraceID(t *testing.T) {
	t.Parallel()

	var decoded PasswordSyncMessage
	err := json.Unmarshal([]byte(`{"cn":"311551001","upn":"311551001@nycu.edu.tw","eventType":"login_bootstrap","passwordCiphertext":"ciphertext","passwordNonce":"nonce","passwordKeyId":"password-payload-key-v1","passwordAlg":"AES-256-GCM","displayName":"Student","mail":"student@nycu.edu.tw","enqueuedAt":"2026-07-08T00:00:00Z"}`), &decoded)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if decoded.TraceID != "" {
		t.Fatalf("decoded.TraceID = %q, want empty for legacy message", decoded.TraceID)
	}
}

func TestPasswordSyncMessageNeverSerializesPassword(t *testing.T) {
	msg := PasswordSyncMessage{
		CN:        "jdoe",
		UPN:       "jdoe@example.edu",
		EventType: EventPasswordChange,
		Password:  "cleartext-password",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if strings.Contains(string(data), "cleartext-password") || strings.Contains(string(data), `"password"`) {
		t.Fatalf("marshaled message leaks password: %s", data)
	}
}

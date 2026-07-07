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

package migration

import "testing"

func TestValidEventTypeAcceptsKnownEvents(t *testing.T) {
	for _, et := range []EventType{EventLoginBootstrap, EventPasswordChange, EventPasswordRecovery} {
		if !ValidEventType(et) {
			t.Errorf("ValidEventType(%q) = false, want true", et)
		}
	}
}

func TestValidEventTypeRejectsUnknownEvent(t *testing.T) {
	if ValidEventType(EventType("password_reset")) {
		t.Error("ValidEventType(\"password_reset\") = true, want false")
	}
	if ValidEventType(EventType("")) {
		t.Error("ValidEventType(\"\") = true, want false")
	}
}

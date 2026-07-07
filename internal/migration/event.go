package migration

import "errors"

// EventType identifies why the portal invoked the password hook. See design
// spec section 1.2.1 Amendment for the corrected event model: the hook is not
// invoked on every successful login, but on these three distinct portal
// events.
type EventType string

const (
	// EventLoginBootstrap fires when a user completes SSO login and the
	// portal bootstraps their on-prem AD account. Submit skips enqueueing
	// this event when the UPN is already synced or has a fresh pending sync.
	EventLoginBootstrap EventType = "login_bootstrap"
	// EventPasswordChange fires when a user changes their password. Always
	// enqueued regardless of prior sync status.
	EventPasswordChange EventType = "password_change"
	// EventPasswordRecovery fires when a user recovers/resets their
	// password. Always enqueued regardless of prior sync status.
	EventPasswordRecovery EventType = "password_recovery"
)

// ErrInvalidEventType is returned by Service.Submit when the request's
// EventType is empty or not one of the known constants.
var ErrInvalidEventType = errors.New("migration: invalid event type")

// ValidEventType reports whether t is one of the known EventType constants.
func ValidEventType(t EventType) bool {
	switch t {
	case EventLoginBootstrap, EventPasswordChange, EventPasswordRecovery:
		return true
	default:
		return false
	}
}

package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/internal/sensitiveio"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type Hook struct {
	service        *migration.Service
	problemBaseURL string
	logger         *slog.Logger
	recorder       observability.Recorder
}

type HookOptions struct {
	Logger   *slog.Logger
	Recorder observability.Recorder
}

func NewHook(service *migration.Service, problemBaseURL string) *Hook {
	return NewHookWithOptions(service, problemBaseURL, HookOptions{})
}

func NewHookWithOptions(service *migration.Service, problemBaseURL string, options HookOptions) *Hook {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return &Hook{
		service:        service,
		problemBaseURL: strings.TrimRight(problemBaseURL, "/"),
		logger:         options.Logger,
		recorder:       options.Recorder,
	}
}

func (h *Hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.recordRejected(r, http.StatusMethodNotAllowed, "method_not_allowed", "")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rawBody, err := sensitiveio.ReadAll(r.Body)
	defer passwordcrypto.ZeroBytes(rawBody)
	if err != nil {
		h.recordRejected(r, http.StatusBadRequest, "validation_error", "")
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be readable"))
		return
	}

	var body passwordHookRequest
	err = json.Unmarshal(rawBody, &body)
	defer passwordcrypto.ZeroBytes(body.Password)
	if err != nil {
		h.recordRejected(r, http.StatusBadRequest, "validation_error", "")
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be valid json"))
		return
	}
	if detail := body.validate(); detail != "" {
		h.recordRejected(r, http.StatusBadRequest, "validation_error", body.EventType)
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), detail))
		return
	}

	decision, err := h.service.Submit(r.Context(), migration.Request{
		CN:          body.CN,
		EventType:   body.EventType,
		Password:    []byte(body.Password),
		DisplayName: body.DisplayName,
		Mail:        body.Mail,
	})
	if err != nil {
		if errors.Is(err, migration.ErrUnknownIdentity) || errors.Is(err, migration.ErrExternalIdentity) {
			h.recordRejected(r, http.StatusBadRequest, "validation_error", body.EventType)
			h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), err.Error()))
			return
		}
		h.recordRejected(r, http.StatusInternalServerError, "accept_error", body.EventType)
		h.writeProblem(w, r, problem.Internal(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "failed to accept password sync request"))
		return
	}

	h.recordDecision(r, http.StatusAccepted, body.EventType, decision)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Hook) writeProblem(w http.ResponseWriter, _ *http.Request, p problem.Problem) {
	problem.Write(w, p)
}

func (h *Hook) recordDecision(r *http.Request, status int, eventType migration.EventType, decision migration.Decision) {
	outcome := "accepted"
	if decision.Enqueued {
		outcome = "enqueued"
	}
	if decision.Skipped {
		outcome = "skipped"
	}
	labels := observability.Labels{
		"status":       fmt.Sprint(status),
		"outcome":      outcome,
		"eventType":    string(eventType),
		"identityType": string(decision.IdentityType),
	}
	if decision.Reason != "" {
		labels["reason"] = decision.Reason
	}
	h.recorder.Inc(r.Context(), observability.MetricHookRequestsTotal, labels)
	if decision.Skipped {
		h.recorder.Inc(r.Context(), observability.MetricMigrationSkippedTotal, labels)
	}

	action := observability.ActionHookAccepted
	if decision.Skipped {
		action = observability.ActionHookSkipped
	}
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("outcome", outcome),
		slog.Int("status", status),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID:      requestid.From(r.Context()),
		UPN:          decision.UPN,
		EventType:    string(eventType),
		IdentityType: string(decision.IdentityType),
	})...)
	if decision.Reason != "" {
		attrs = append(attrs, slog.String("reason", decision.Reason))
	}
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, action, attrs...)
}

func (h *Hook) recordRejected(r *http.Request, status int, outcome string, eventType migration.EventType) {
	labels := observability.Labels{"status": fmt.Sprint(status), "outcome": outcome}
	if eventType != "" {
		labels["eventType"] = string(eventType)
	}
	h.recorder.Inc(r.Context(), observability.MetricHookRequestsTotal, labels)
	attrs := []slog.Attr{
		slog.String("action", observability.ActionHookRejected),
		slog.Int("status", status),
		slog.String("outcome", outcome),
	}
	attrs = append(attrs, observability.SafeIdentityAttrs(observability.SafeIdentity{
		TraceID:   requestid.From(r.Context()),
		EventType: string(eventType),
	})...)
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, observability.ActionHookRejected, attrs...)
}

type passwordHookRequest struct {
	CN          string              `json:"cn"`
	EventType   migration.EventType `json:"eventType"`
	Password    passwordBytes       `json:"password"`
	DisplayName string              `json:"displayName"`
	Mail        string              `json:"mail"`
}

func (r passwordHookRequest) validate() string {
	switch {
	case strings.TrimSpace(r.CN) == "":
		return "Field 'cn' is required"
	case len(r.Password) == 0:
		return "Field 'password' is required"
	case strings.TrimSpace(r.DisplayName) == "":
		return "Field 'displayName' is required"
	case strings.TrimSpace(r.Mail) == "":
		return "Field 'mail' is required"
	case strings.TrimSpace(string(r.EventType)) == "":
		return "Field 'eventType' is required"
	case !migration.ValidEventType(r.EventType):
		return "Field 'eventType' must be one of login_bootstrap, password_change, password_recovery"
	default:
		return ""
	}
}

// passwordBytes decodes a JSON string directly into a mutable []byte instead
// of an immutable Go string. This is required so the plaintext password can
// be explicitly zeroed after use; a `string` result is immutable and cannot
// be scrubbed from memory. Standard-library alternatives were rejected:
// unmarshalling into `string`, `json.Decoder.Token`, and `strconv.Unquote`
// all produce immutable plaintext strings; unmarshalling into `[]byte`
// expects base64-encoded input, not a raw string; and `json.RawMessage`
// still requires this same custom JSON-string unquote step, since
// encoding/json's own byte-oriented unquote helper is unexported.
// decodeJSONStringBytes therefore reimplements JSON string unquoting,
// intentionally preserving encoding/json's behavior for null, escapes,
// surrogate pairs, invalid UTF-8, and control characters/invalid escapes.
type passwordBytes []byte

func (p *passwordBytes) UnmarshalJSON(data []byte) error {
	passwordcrypto.ZeroBytes(*p)
	*p = nil

	if bytes.Equal(data, []byte("null")) {
		return nil
	}

	decoded, err := decodeJSONStringBytes(data)
	if err != nil {
		return err
	}
	*p = decoded
	return nil
}

func decodeJSONStringBytes(data []byte) (_ []byte, err error) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return nil, errors.New("password must be a json string")
	}

	const maxBytesPerInvalidUTF8Byte = len("\ufffd")
	out := make([]byte, 0, (len(data)-2)*maxBytesPerInvalidUTF8Byte)
	defer func() {
		if err != nil {
			passwordcrypto.ZeroBytes(out)
		}
	}()
	for i := 1; i < len(data)-1; i++ {
		b := data[i]
		if b != '\\' {
			if b == '"' {
				return nil, errors.New("password contains invalid unescaped json string terminator")
			}
			if b < 0x20 {
				return nil, errors.New("password contains invalid json string control character")
			}
			if b < utf8.RuneSelf {
				out = append(out, b)
				continue
			}
			r, size := utf8.DecodeRune(data[i : len(data)-1])
			out = utf8.AppendRune(out, r)
			i += size - 1
			continue
		}

		i++
		if i >= len(data)-1 {
			return nil, errors.New("password contains invalid json escape")
		}
		switch data[i] {
		case '"', '\\', '/':
			out = append(out, data[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, consumed, err := decodeUnicodeEscape(data[i+1 : len(data)-1])
			if err != nil {
				return nil, err
			}
			i += consumed
			out = utf8.AppendRune(out, r)
		default:
			return nil, fmt.Errorf("password contains invalid json escape %q", data[i])
		}
	}
	return out, nil
}

func decodeUnicodeEscape(data []byte) (rune, int, error) {
	if len(data) < 4 {
		return 0, 0, errors.New("password contains short unicode escape")
	}
	r, err := hex4(data[:4])
	if err != nil {
		return 0, 0, err
	}
	if !utf16.IsSurrogate(r) {
		return r, 4, nil
	}
	if r < 0xD800 || r > 0xDBFF {
		return utf8.RuneError, 4, nil
	}
	if len(data) < 10 || data[4] != '\\' || data[5] != 'u' {
		return utf8.RuneError, 4, nil
	}
	low, err := hex4(data[6:10])
	if err != nil {
		return utf8.RuneError, 4, nil
	}
	decoded := utf16.DecodeRune(r, low)
	if decoded == utf8.RuneError {
		return utf8.RuneError, 4, nil
	}
	return decoded, 10, nil
}

func hex4(data []byte) (rune, error) {
	var r rune
	for _, b := range data {
		r <<= 4
		switch {
		case b >= '0' && b <= '9':
			r += rune(b - '0')
		case b >= 'a' && b <= 'f':
			r += rune(b-'a') + 10
		case b >= 'A' && b <= 'F':
			r += rune(b-'A') + 10
		default:
			return 0, fmt.Errorf("password contains invalid unicode escape byte %q", b)
		}
	}
	return r, nil
}

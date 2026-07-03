package graphclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func TestUpsertUserPasswordPatchesExistingUser(t *testing.T) {
	var requests []seenGraphRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, captureGraphRequest(t, r))
		switch len(requests) {
		case 1:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1.0/users/311551001%40nycu.edu.tw" {
				t.Fatalf("first request = %s %s", r.Method, r.URL.EscapedPath())
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"user-id"}`))
		case 2:
			if r.Method != http.MethodPatch || r.URL.EscapedPath() != "/v1.0/users/311551001%40nycu.edu.tw" {
				t.Fatalf("second request = %s %s", r.Method, r.URL.EscapedPath())
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %d: %s %s", len(requests), r.Method, r.URL.EscapedPath())
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, nil)

	err := client.UpsertUserPassword(context.Background(), User{
		UPN:         "311551001@nycu.edu.tw",
		DisplayName: "Student One",
		Mail:        "student@nycu.edu.tw",
	}, []byte("cleartext-password"))

	if err != nil {
		t.Fatalf("UpsertUserPassword returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	assertPasswordProfile(t, requests[1].body, "cleartext-password")
	if bytes.Contains(requests[1].body, []byte("displayName")) {
		t.Fatalf("patch body mutates displayName: %s", requests[1].body)
	}
}

func TestUpsertUserPasswordCreatesMissingUser(t *testing.T) {
	var requests []seenGraphRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, captureGraphRequest(t, r))
		switch len(requests) {
		case 1:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1.0/users/311551001%40nycu.edu.tw" {
				t.Fatalf("first request = %s %s", r.Method, r.URL.EscapedPath())
			}
			w.WriteHeader(http.StatusNotFound)
		case 2:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1.0/users" {
				t.Fatalf("second request = %s %s", r.Method, r.URL.EscapedPath())
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request %d: %s %s", len(requests), r.Method, r.URL.EscapedPath())
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, nil)

	err := client.UpsertUserPassword(context.Background(), User{
		UPN:         "311551001@nycu.edu.tw",
		DisplayName: "Student One",
		Mail:        "student@nycu.edu.tw",
	}, []byte("cleartext-password"))

	if err != nil {
		t.Fatalf("UpsertUserPassword returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	var body map[string]any
	if err := json.Unmarshal(requests[1].body, &body); err != nil {
		t.Fatalf("create body is invalid JSON: %v\n%s", err, requests[1].body)
	}
	if body["accountEnabled"] != true {
		t.Fatalf("accountEnabled = %#v, want true", body["accountEnabled"])
	}
	for key, want := range map[string]string{
		"displayName":       "Student One",
		"mailNickname":      "311551001",
		"userPrincipalName": "311551001@nycu.edu.tw",
		"mail":              "student@nycu.edu.tw",
	} {
		if body[key] != want {
			t.Fatalf("%s = %#v, want %q in body %s", key, body[key], want, requests[1].body)
		}
	}
	otherMails, ok := body["otherMails"].([]any)
	if !ok || len(otherMails) != 1 || otherMails[0] != "student@nycu.edu.tw" {
		t.Fatalf("otherMails = %#v, want [student@nycu.edu.tw]", body["otherMails"])
	}
	assertPasswordProfile(t, requests[1].body, "cleartext-password")
}

func TestUpsertUserPasswordSendsBearerToken(t *testing.T) {
	var requests []seenGraphRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, captureGraphRequest(t, r))
		if len(requests) == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, nil)

	err := client.UpsertUserPassword(context.Background(), User{UPN: "311551001@nycu.edu.tw"}, []byte("cleartext-password"))

	if err != nil {
		t.Fatalf("UpsertUserPassword returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	for i, req := range requests {
		if req.authorization != "Bearer test-token" {
			t.Fatalf("request %d Authorization = %q, want Bearer test-token", i+1, req.authorization)
		}
	}
}

func TestUpsertUserPasswordClassifiesGraphStatuses(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		network  bool
		wantType any
	}{
		{name: "bad request", status: http.StatusBadRequest, wantType: (*PermanentError)(nil)},
		{name: "forbidden", status: http.StatusForbidden, wantType: (*PermanentError)(nil)},
		{name: "rate limited", status: http.StatusTooManyRequests, wantType: (*TransientError)(nil)},
		{name: "service unavailable", status: http.StatusServiceUnavailable, wantType: (*TransientError)(nil)},
		{name: "network", network: true, wantType: (*TransientError)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newStatusClient(t, tt.status, tt.network)

			err := client.UpsertUserPassword(context.Background(), User{UPN: "311551001@nycu.edu.tw"}, []byte("cleartext-password"))

			if err == nil {
				t.Fatal("UpsertUserPassword returned nil error")
			}
			switch tt.wantType.(type) {
			case *PermanentError:
				var permanent *PermanentError
				if !errors.As(err, &permanent) {
					t.Fatalf("error = %T %[1]v, want *PermanentError", err)
				}
			case *TransientError:
				var transient *TransientError
				if !errors.As(err, &transient) {
					t.Fatalf("error = %T %[1]v, want *TransientError", err)
				}
			default:
				t.Fatalf("unsupported wantType %T", tt.wantType)
			}
		})
	}
}

func TestUpsertUserPasswordClearsRequestBodyBufferAfterAttempt(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, func(body []byte) {
		captured = body
	})

	err := client.UpsertUserPassword(context.Background(), User{UPN: "311551001@nycu.edu.tw"}, []byte("cleartext-password"))

	if err != nil {
		t.Fatalf("UpsertUserPassword returned error: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("AfterRequestBodyBuilt did not capture a body")
	}
	if bytes.Contains(captured, []byte("cleartext-password")) {
		t.Fatalf("request body buffer still contains password after attempt: %q", captured)
	}
}

func TestUpsertUserPasswordClearsRequestBodyBufferAfterGraphError(t *testing.T) {
	tests := []struct {
		name       string
		lookupCode int
		writeCode  int
	}{
		{name: "patch permanent error", lookupCode: http.StatusOK, writeCode: http.StatusBadRequest},
		{name: "patch transient error", lookupCode: http.StatusOK, writeCode: http.StatusServiceUnavailable},
		{name: "create permanent error", lookupCode: http.StatusNotFound, writeCode: http.StatusBadRequest},
		{name: "create transient error", lookupCode: http.StatusNotFound, writeCode: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []byte
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					w.WriteHeader(tt.lookupCode)
					return
				}
				w.WriteHeader(tt.writeCode)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, func(body []byte) {
				captured = body
			})

			err := client.UpsertUserPassword(context.Background(), User{
				UPN:         "311551001@nycu.edu.tw",
				DisplayName: "Student One",
				Mail:        "student@nycu.edu.tw",
			}, []byte("cleartext-password"))

			if err == nil {
				t.Fatal("UpsertUserPassword returned nil error")
			}
			if len(captured) == 0 {
				t.Fatal("AfterRequestBodyBuilt did not capture a body")
			}
			if bytes.Contains(captured, []byte("cleartext-password")) {
				t.Fatalf("request body buffer still contains password after graph error: %q", captured)
			}
		})
	}
}

type seenGraphRequest struct {
	authorization string
	body          []byte
}

func captureGraphRequest(t *testing.T, r *http.Request) seenGraphRequest {
	t.Helper()
	body := readAll(t, r)
	return seenGraphRequest{
		authorization: r.Header.Get("Authorization"),
		body:          body,
	}
}

func assertPasswordProfile(t *testing.T, body []byte, wantPassword string) {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is invalid JSON: %v\n%s", err, body)
	}
	profile, ok := decoded["passwordProfile"].(map[string]any)
	if !ok {
		t.Fatalf("passwordProfile missing or invalid in body %s", body)
	}
	if profile["password"] != wantPassword {
		t.Fatalf("passwordProfile.password = %#v, want %q", profile["password"], wantPassword)
	}
	if profile["forceChangePasswordNextSignIn"] != false {
		t.Fatalf("forceChangePasswordNextSignIn = %#v, want false", profile["forceChangePasswordNextSignIn"])
	}
}

func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return buf.Bytes()
}

func newTestClient(t *testing.T, baseURL string, afterBody func([]byte)) *HTTPClient {
	t.Helper()

	client, err := NewHTTPClient(fakeTokenCredential{}, Options{
		BaseURL:               baseURL,
		AfterRequestBodyBuilt: afterBody,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}
	return client
}

func newStatusClient(t *testing.T, status int, network bool) *HTTPClient {
	t.Helper()

	if network {
		client, err := NewHTTPClient(fakeTokenCredential{}, Options{
			BaseURL: "https://graph.invalid",
			HTTPClient: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("network unavailable")
				}),
			},
		})
		if err != nil {
			t.Fatalf("NewHTTPClient returned error: %v", err)
		}
		return client
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return newTestClient(t, server.URL, nil)
}

type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

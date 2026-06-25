package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type Hook struct {
	service        *migration.Service
	problemBaseURL string
}

func NewHook(service *migration.Service, problemBaseURL string) *Hook {
	return &Hook{
		service:        service,
		problemBaseURL: strings.TrimRight(problemBaseURL, "/"),
	}
}

func (h *Hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body passwordHookRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be valid json"))
		return
	}
	if detail := body.validate(); detail != "" {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), detail))
		return
	}

	_, err := h.service.Submit(r.Context(), migration.Request{
		CN:          body.CN,
		Password:    body.Password,
		DisplayName: body.DisplayName,
		Mail:        body.Mail,
	})
	if err != nil {
		if errors.Is(err, migration.ErrUnknownIdentity) || errors.Is(err, migration.ErrExternalIdentity) {
			h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), err.Error()))
			return
		}
		h.writeProblem(w, r, problem.Internal(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "failed to accept password sync request"))
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Hook) writeProblem(w http.ResponseWriter, _ *http.Request, p problem.Problem) {
	problem.Write(w, p)
}

type passwordHookRequest struct {
	CN          string `json:"cn"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Mail        string `json:"mail"`
}

func (r passwordHookRequest) validate() string {
	switch {
	case strings.TrimSpace(r.CN) == "":
		return "Field 'cn' is required"
	case r.Password == "":
		return "Field 'password' is required"
	case strings.TrimSpace(r.DisplayName) == "":
		return "Field 'displayName' is required"
	case strings.TrimSpace(r.Mail) == "":
		return "Field 'mail' is required"
	default:
		return ""
	}
}

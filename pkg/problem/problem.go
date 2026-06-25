package problem

import (
	"encoding/json"
	"net/http"
	"strings"
)

const DefaultBaseURL = "https://nycu.edu.tw/problems"

type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	TraceID  string `json:"traceId,omitempty"`
}

func New(problemType string, title string, status int, detail string, instance string, traceID string) Problem {
	return Problem{
		Type:     problemType,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: instance,
		TraceID:  traceID,
	}
}

func Validation(baseURL, instance, traceID, detail string) Problem {
	return New(typeURL(baseURL, "validation-error"), "Validation Error", http.StatusBadRequest, detail, instance, traceID)
}

func Unauthorized(baseURL, instance, traceID, detail string) Problem {
	return New(typeURL(baseURL, "unauthorized"), "Unauthorized", http.StatusUnauthorized, detail, instance, traceID)
}

func TooManyRequests(baseURL, instance, traceID, detail string) Problem {
	return New(typeURL(baseURL, "too-many-requests"), "Too Many Requests", http.StatusTooManyRequests, detail, instance, traceID)
}

func Internal(baseURL, instance, traceID, detail string) Problem {
	return New(typeURL(baseURL, "internal-error"), "Internal Server Error", http.StatusInternalServerError, detail, instance, traceID)
}

func typeURL(baseURL, slug string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return baseURL + "/" + slug
}

func Write(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

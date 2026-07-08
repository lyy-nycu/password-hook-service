package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type RecoveryOptions struct {
	Logger      *slog.Logger
	ProblemBase string
	Recorder    observability.Recorder
}

func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return RecoveryWithOptions(RecoveryOptions{Logger: log, ProblemBase: problem.DefaultBaseURL})
}

func RecoveryWithProblemBase(log *slog.Logger, problemBase string) func(http.Handler) http.Handler {
	return RecoveryWithOptions(RecoveryOptions{Logger: log, ProblemBase: problemBase})
}

func RecoveryWithOptions(options RecoveryOptions) func(http.Handler) http.Handler {
	if strings.TrimSpace(options.ProblemBase) == "" {
		options.ProblemBase = problem.DefaultBaseURL
	}
	if options.Recorder == nil {
		options.Recorder = observability.NoopRecorder{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if options.Logger != nil {
						options.Logger.Error("panic recovered", slog.Any("panic", recovered))
					}
					recordMiddlewareOutcome(r.Context(), options.Logger, options.Recorder, requestid.From(r.Context()), "recovery", http.StatusInternalServerError, "panic_recovered", "panic")
					problem.Write(w, problem.Internal(options.ProblemBase, r.URL.Path, requestid.From(r.Context()), "unexpected server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

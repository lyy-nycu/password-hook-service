package middleware

import (
	"log/slog"
	"net/http"

	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return RecoveryWithProblemBase(log, problem.DefaultBaseURL)
}

func RecoveryWithProblemBase(log *slog.Logger, problemBase string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if log != nil {
						log.Error("panic recovered", slog.Any("panic", recovered))
					}
					problem.Write(w, problem.Internal(problemBase, r.URL.Path, requestid.From(r.Context()), "unexpected server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

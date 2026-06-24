package middleware

import (
	"log/slog"
	"net/http"

	"github.com/nycu/password-hook-service/pkg/problem"
)

func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if log != nil {
						log.Error("panic recovered", slog.Any("panic", recovered))
					}
					problem.Write(w, problem.New(
						"https://nycu.edu.tw/problems/internal-error",
						"Internal Server Error",
						http.StatusInternalServerError,
						"unexpected server error",
						r.URL.Path,
						"",
					))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

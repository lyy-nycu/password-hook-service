package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nycu/password-hook-service/internal/buildinfo"
)

type Routes struct {
	Hook http.Handler
}

type Server struct {
	server *http.Server
}

func New(addr string, routes Routes, info buildinfo.Info) *Server {
	return &Server{
		server: &http.Server{
			Addr:    addr,
			Handler: NewMux(routes, info),
		},
	}
}

func NewMux(routes Routes, info buildinfo.Info) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
	if routes.Hook != nil {
		mux.Handle("POST /api/v1/hook/password", routes.Hook)
	}
	return mux
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		if err := s.server.Shutdown(context.Background()); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

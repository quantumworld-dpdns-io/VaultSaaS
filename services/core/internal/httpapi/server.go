// Package httpapi is the HTTP surface of the core service. It is served by the
// `api` Lambda behind a Function URL (AWS_IAM auth, so only the Cloudflare
// Worker can call it) and by cmd/local during development.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Version is set at build time with -ldflags "-X .../httpapi.Version=...".
var Version = "dev"

type Server struct {
	mux *http.ServeMux
	log *slog.Logger
}

type Deps struct {
	Log *slog.Logger
}

func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Server{mux: http.NewServeMux(), log: d.Log}
	s.mux.HandleFunc("GET /healthz", s.healthz)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "core", "version": Version})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError never echoes internal error details to the caller.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

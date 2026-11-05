// Package server exposes the REST API and the embedded web interface.
package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"admin/internal/config"
	"admin/internal/store"
	"admin/web"
)

type Server struct {
	cfg     *config.Config
	store   *store.Store
	started time.Time
}

func New(cfg *config.Config, st *store.Store) *Server {
	return &Server{cfg: cfg, store: st, started: time.Now()}
}

func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}

	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/services/{name}", s.handleService)
	api.Handle("GET /", http.FileServerFS(static))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok\n"))
	})
	root.Handle("/", s.basicAuth(api))
	return securityHeaders(root)
}

type statusResponse struct {
	Hostname   string                 `json:"hostname"`
	ServerTime time.Time              `json:"server_time"`
	StartedAt  time.Time              `json:"started_at"`
	Services   map[string]store.Entry `json:"services"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, statusResponse{
		Hostname:   host,
		ServerTime: time.Now(),
		StartedAt:  s.started,
		Services:   s.store.All(),
	})
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	e, ok := s.store.Get(r.PathValue("name"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown service"})
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	if s.cfg.Auth.Username == "" {
		return next
	}
	wantUser := sha256.Sum256([]byte(s.cfg.Auth.Username))
	wantPass := sha256.Sum256([]byte(s.cfg.Auth.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotUser := sha256.Sum256([]byte(u))
		gotPass := sha256.Sum256([]byte(p))
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// Package httpapi is the HTTP surface of the core service. It is served by the
// `api` Lambda behind a Function URL (AWS_IAM auth, so only the Cloudflare
// Worker can call it) and by cmd/local during development.
//
// Identity: the edge verifies the Supabase JWT and forwards the user as
// X-VaultOps-User-Id / X-VaultOps-User-Email, plus the selected organization
// as X-VaultOps-Org-Id. Membership and role are always re-checked here
// against the database; the client never asserts its own role.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/connections"
	"github.com/vaultops/core/internal/identity"
	"github.com/vaultops/core/internal/store"
)

// Version is set at build time with -ldflags "-X .../httpapi.Version=...".
var Version = "dev"

const (
	HeaderUserID    = "X-Vaultops-User-Id"
	HeaderUserEmail = "X-Vaultops-User-Email"
	HeaderOrgID     = "X-Vaultops-Org-Id"
)

type Server struct {
	mux   *http.ServeMux
	log   *slog.Logger
	store *store.Store
	conns *connections.Resolver
	siwe  *identity.SIWE
}

type Deps struct {
	Log   *slog.Logger
	Store *store.Store
	Conns *connections.Resolver
	SIWE  *identity.SIWE
}

func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Server{mux: http.NewServeMux(), log: d.Log, store: d.Store, conns: d.Conns, siwe: d.SIWE}
	s.mux.HandleFunc("GET /healthz", s.healthz)

	user := func(h http.Handler) http.Handler { return s.requireStore(s.requireUser(h)) }
	org := func(min authz.Role, h http.HandlerFunc) http.Handler { return user(s.requireRole(min, h)) }

	s.mux.Handle("GET /v1/me", user(http.HandlerFunc(s.me)))
	s.mux.Handle("POST /v1/orgs", user(http.HandlerFunc(s.createOrg)))
	s.mux.Handle("POST /v1/invitations/accept", user(http.HandlerFunc(s.acceptInvitation)))
	s.mux.Handle("POST /v1/me/wallets/nonce", user(http.HandlerFunc(s.walletNonce)))
	s.mux.Handle("POST /v1/me/wallets", user(http.HandlerFunc(s.linkWallet)))

	s.mux.Handle("GET /v1/org/members", org(authz.Viewer, s.listMembers))
	s.mux.Handle("POST /v1/org/invitations", org(authz.Admin, s.createInvitation))
	s.mux.Handle("PUT /v1/org/bitgo-connection", org(authz.Admin, s.putConnection))
	s.mux.Handle("GET /v1/wallets", org(authz.Viewer, s.listWallets))
	s.mux.Handle("GET /v1/transfers", org(authz.Viewer, s.listTransfers))
	s.mux.Handle("GET /v1/alerts", org(authz.Viewer, s.listAlerts))
	s.mux.Handle("GET /v1/audit", org(authz.Admin, s.listAudit))
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

// ---- middleware ------------------------------------------------------------

func (s *Server) requireStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.store == nil {
			writeError(w, http.StatusServiceUnavailable, "database_not_configured")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.Header.Get(HeaderUserID))
		if err != nil || id == uuid.Nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		p := authz.Principal{UserID: id, Email: strings.ToLower(strings.TrimSpace(r.Header.Get(HeaderUserEmail)))}
		next.ServeHTTP(w, r.WithContext(authz.WithPrincipal(r.Context(), p)))
	})
}

// requireRole resolves the selected org and the caller's role in it. A
// non-member and a non-existent org get the same 403, so org ids cannot be
// probed (BOLA/IDOR).
func (s *Server) requireRole(min authz.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := authz.PrincipalFrom(r.Context())
		org, err := uuid.Parse(r.Header.Get(HeaderOrgID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "organization_required")
			return
		}
		role, err := s.store.MemberRole(r.Context(), org, p.UserID)
		if err != nil {
			s.internal(w, "member role", err)
			return
		}
		if !authz.Role(role).AtLeast(min) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		sc := authz.Scope{Principal: p, Org: org, Role: authz.Role(role)}
		next.ServeHTTP(w, r.WithContext(authz.WithScope(r.Context(), sc)))
	})
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError never echoes internal error details to the caller.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal")
}

var errBadBody = errors.New("bad body")

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBadBody
	}
	return nil
}

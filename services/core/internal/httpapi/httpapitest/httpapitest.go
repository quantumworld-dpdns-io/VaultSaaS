// Package httpapitest wires a real API (Postgres + mock BitGo) for tests.
package httpapitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo/bitgomock"
	"github.com/vaultops/core/internal/connections"
	"github.com/vaultops/core/internal/crypto"
	"github.com/vaultops/core/internal/httpapi"
	"github.com/vaultops/core/internal/identity"
	"github.com/vaultops/core/internal/recon"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

const (
	BitGoToken = "tenant-a-bitgo-token"
	SIWEDomain = "app.vaultops.test"
)

type Env struct {
	T       *testing.T
	Store   *store.Store
	Owner   string // owner DB URL
	BitGo   *bitgomock.Server
	Handler http.Handler
}

type User struct {
	ID    uuid.UUID
	Email string
}

func New(t *testing.T) *Env {
	t.Helper()
	s, owner := storetest.New(t)
	m := bitgomock.New(BitGoToken)
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	key, _ := crypto.NewKey()
	kr, err := crypto.ParseKeyring("1:" + key)
	if err != nil {
		t.Fatal(err)
	}
	conns := &connections.Resolver{Store: s, Keyring: kr, BaseURL: srv.URL}
	h := httpapi.New(httpapi.Deps{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store: s,
		Conns: conns,
		SIWE:  &identity.SIWE{Domain: SIWEDomain},
		Recon: &recon.Engine{Store: s, BitGoFor: func(ctx context.Context, org uuid.UUID) (recon.BitGo, error) {
			return conns.ForOrg(ctx, org)
		}},
	})
	return &Env{T: t, Store: s, Owner: owner, BitGo: m, Handler: h}
}

func (e *Env) User(email string) User {
	return User{ID: storetest.CreateUser(e.T, e.Owner, email), Email: email}
}

// Call performs a request as u (zero User = anonymous) in org (may be "").
func (e *Env) Call(u User, org any, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	e.T.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if u.ID != uuid.Nil {
		req.Header.Set(httpapi.HeaderUserID, u.ID.String())
		req.Header.Set(httpapi.HeaderUserEmail, u.Email)
	}
	if org != nil && org != "" {
		req.Header.Set(httpapi.HeaderOrgID, toString(org))
	}
	rec := httptest.NewRecorder()
	e.Handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// CreateOrg creates an org as u and returns its id.
func (e *Env) CreateOrg(u User, name string) string {
	e.T.Helper()
	rec, out := e.Call(u, nil, http.MethodPost, "/v1/orgs", map[string]string{"name": name})
	if rec.Code != http.StatusCreated {
		e.T.Fatalf("create org: %d %s", rec.Code, rec.Body)
	}
	return out["id"].(string)
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case uuid.UUID:
		return x.String()
	}
	return ""
}

// CallRaw sends a raw body with an explicit content type.
func (e *Env) CallRaw(u User, org, method, path, contentType, body string) (*httptest.ResponseRecorder, map[string]any) {
	e.T.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(httpapi.HeaderUserID, u.ID.String())
	req.Header.Set(httpapi.HeaderUserEmail, u.Email)
	req.Header.Set(httpapi.HeaderOrgID, org)
	rec := httptest.NewRecorder()
	e.Handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

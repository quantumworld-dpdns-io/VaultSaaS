// Command mockauth stands in for Supabase Auth in local development and the
// Robot suites: it serves a JWKS and issues ES256 access tokens shaped like
// Supabase's (iss=<base>/auth/v1, aud=authenticated, role=authenticated).
// Like Supabase, it creates the auth.users row on first sign-in by email.
// Never deployed.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vaultops/core/internal/devserver"
)

func main() {
	addr := envOr("MOCK_AUTH_ADDR", "127.0.0.1:8095")
	issuer := "http://" + addr + "/auth/v1"
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	var db *pgxpool.Pool
	if url := os.Getenv("MOCK_AUTH_DB_URL"); url != "" {
		if db, err = pgxpool.New(context.Background(), url); err != nil {
			log.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/v1/.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "EC", "crv": "P-256", "kid": "mock-1", "alg": "ES256", "use": "sig",
			"x": b64(pad(key.X)), "y": b64(pad(key.Y)),
		}}})
	})
	// POST /__mock/token {"email": "..."} -> {"access_token": "...", "user": {"id": "..."}}
	mux.HandleFunc("POST /__mock/token", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || !strings.Contains(req.Email, "@") {
			http.Error(w, `{"error":"email required"}`, http.StatusBadRequest)
			return
		}
		email := strings.ToLower(strings.TrimSpace(req.Email))
		sub := deterministicUUID(email)
		if db != nil {
			if _, err := db.Exec(r.Context(), `INSERT INTO auth.users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, sub, email); err != nil {
				http.Error(w, `{"error":"db"}`, http.StatusInternalServerError)
				return
			}
		}
		now := time.Now()
		tok := sign(key, map[string]any{
			"iss": issuer, "aud": "authenticated", "role": "authenticated", "sub": sub, "email": email,
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		})
		writeJSON(w, map[string]any{"access_token": tok, "token_type": "bearer", "user": map[string]string{"id": sub, "email": email}})
	})
	log.Printf("mock auth listening on %s (issuer %s)", addr, issuer)
	log.Fatal(devserver.ListenAndServe(addr, mux))
}

func sign(key *ecdsa.PrivateKey, claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "ES256", "typ": "JWT", "kid": "mock-1"})
	c, _ := json.Marshal(claims)
	input := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		log.Fatal(err)
	}
	return input + "." + b64(append(pad(r), pad(s)...))
}

func pad(n *big.Int) []byte {
	b := make([]byte, 32)
	return n.FillBytes(b)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// deterministicUUID gives the same user id for the same email (UUIDv8-shaped).
func deterministicUUID(email string) string {
	s := sha256.Sum256([]byte("mockauth:" + email))
	s[6] = (s[6] & 0x0f) | 0x80
	s[8] = (s[8] & 0x3f) | 0x80
	x := base16(s[:16])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func base16(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2], out[i*2+1] = hexd[v>>4], hexd[v&0xf]
	}
	return string(out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

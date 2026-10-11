package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/identity"
	"github.com/vaultops/core/internal/store"
)

const nonceTTL = 5 * time.Minute

// walletNonce starts linking a wallet to the signed-in user (D4). There is no
// equivalent endpoint for signing in or signing up with a wallet.
func (s *Server) walletNonce(w http.ResponseWriter, r *http.Request) {
	if s.siwe == nil {
		writeError(w, http.StatusServiceUnavailable, "wallet_linking_not_configured")
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	nonce, err := identity.NewNonce()
	if err != nil {
		s.internal(w, "nonce", err)
		return
	}
	if err := s.store.AsUser(r.Context(), p.UserID, func(tx *store.Tx) error {
		return tx.CreateSiweNonce(nonce, nonceTTL)
	}); err != nil {
		s.internal(w, "store nonce", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nonce": nonce, "domain": s.siwe.Domain, "statement": identity.Statement, "expiresInSeconds": int(nonceTTL.Seconds()),
	})
}

func (s *Server) linkWallet(w http.ResponseWriter, r *http.Request) {
	if s.siwe == nil {
		writeError(w, http.StatusServiceUnavailable, "wallet_linking_not_configured")
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	var req struct {
		Message   string `json:"message"`
		Signature string `json:"signature"`
	}
	if decode(r, &req) != nil || len(req.Message) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	var address string
	err := s.store.AsUser(r.Context(), p.UserID, func(tx *store.Tx) error {
		addr, err := s.siwe.Verify(req.Message, req.Signature, tx.ConsumeSiweNonce)
		if err != nil {
			return err
		}
		owner, err := s.store.WalletOwner(r.Context(), "eip155", addr)
		if err != nil {
			return err
		}
		if owner == p.UserID {
			address = addr
			return nil // already linked to this user: idempotent
		}
		if err := tx.LinkWallet("eip155", addr); err != nil {
			return err
		}
		address = addr
		return nil
	})
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "wallet_linked_to_another_user")
	case errors.Is(err, identity.ErrNonce), errors.Is(err, identity.ErrExpired):
		writeError(w, http.StatusUnauthorized, "siwe_nonce_invalid")
	case errors.Is(err, identity.ErrSignature), errors.Is(err, identity.ErrDomain), errors.Is(err, identity.ErrMalformed),
		errors.Is(err, identity.ErrBadChecksum), errors.Is(err, identity.ErrWrongPurpose):
		writeError(w, http.StatusUnauthorized, "siwe_invalid")
	case err != nil:
		s.internal(w, "link wallet", err)
	default:
		s.log.Info("wallet linked", "user", p.UserID, "address", address)
		writeJSON(w, http.StatusCreated, map[string]any{"chain": "eip155", "address": address})
	}
}

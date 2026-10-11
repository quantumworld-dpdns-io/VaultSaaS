package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/store"
)

const invitationTTL = 7 * 24 * time.Hour

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	orgs, err := s.store.MyOrganizations(r.Context(), p.UserID)
	if err != nil {
		s.internal(w, "my orgs", err)
		return
	}
	var wallets []store.LinkedWallet
	if err := s.store.AsUser(r.Context(), p.UserID, func(tx *store.Tx) error {
		wallets, err = tx.ListLinkedWallets()
		return err
	}); err != nil {
		s.internal(w, "linked wallets", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"userId": p.UserID, "email": p.Email,
		"organizations": nonNil(orgs), "linkedWallets": nonNil(wallets),
	})
}

func (s *Server) createOrg(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	var req struct {
		Name string `json:"name"`
	}
	if decode(r, &req) != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_name")
		return
	}
	org, token, err := s.store.CreateOrganizationWithOwner(r.Context(), strings.TrimSpace(req.Name), p.UserID)
	if err != nil {
		s.internal(w, "create org", err)
		return
	}
	_ = s.store.InOrgAs(r.Context(), org.ID, p.UserID, func(tx *store.Tx) error {
		return tx.AppendAudit(org.ID, actor(p), "organization.created", "organization", org.ID.String(), map[string]any{"name": org.Name})
	})
	// The webhook token is shown exactly once; only its hash is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"id": org.ID, "name": org.Name, "role": authz.Owner, "webhookToken": token})
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	var req struct {
		Token string `json:"token"`
	}
	if decode(r, &req) != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid_token")
		return
	}
	if p.Email == "" {
		writeError(w, http.StatusForbidden, "verified_email_required")
		return
	}
	org, role, err := s.store.AcceptInvitation(r.Context(), req.Token, p.UserID, p.Email)
	switch {
	case errors.Is(err, store.ErrInvitationInvalid):
		writeError(w, http.StatusNotFound, "invitation_invalid")
		return
	case errors.Is(err, store.ErrInvitationEmail):
		writeError(w, http.StatusForbidden, "invitation_not_for_you")
		return
	case err != nil:
		s.internal(w, "accept invitation", err)
		return
	}
	_ = s.store.InOrgAs(r.Context(), org, p.UserID, func(tx *store.Tx) error {
		return tx.AppendAudit(org, actor(p), "member.joined", "user", p.UserID.String(), map[string]any{"role": role})
	})
	writeJSON(w, http.StatusOK, map[string]any{"organizationId": org, "role": role})
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var members []store.Member
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		members, err = tx.ListMembers()
		return err
	})
	if err != nil {
		s.internal(w, "list members", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": nonNil(members)})
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if decode(r, &req) != nil {
		writeError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	addr, err := mail.ParseAddress(req.Email)
	if err != nil || addr.Address != strings.TrimSpace(req.Email) {
		writeError(w, http.StatusBadRequest, "invalid_email")
		return
	}
	if !sc.Role.CanGrant(authz.Role(req.Role)) {
		writeError(w, http.StatusForbidden, "cannot_grant_role")
		return
	}
	var id, token string
	err = s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		iid, tok, err := tx.CreateInvitation(strings.ToLower(addr.Address), req.Role, sc.UserID, invitationTTL)
		if err != nil {
			return err
		}
		id, token = iid.String(), tok
		return tx.AppendAudit(sc.Org, actor(sc.Principal), "member.invited", "invitation", id,
			map[string]any{"email": strings.ToLower(addr.Address), "role": req.Role})
	})
	if err != nil {
		s.internal(w, "create invitation", err)
		return
	}
	// MVP: the inviter shares the link; email delivery comes later.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": token, "expiresAt": time.Now().Add(invitationTTL)})
}

func (s *Server) putConnection(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var req struct {
		Token          string     `json:"token"`
		EnterpriseID   string     `json:"enterpriseId"`
		TokenExpiresAt *time.Time `json:"tokenExpiresAt"`
	}
	if decode(r, &req) != nil || len(req.Token) < 8 || len(req.Token) > 512 || len(req.EnterpriseID) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	n, err := s.conns.Put(r.Context(), sc.Org, sc.UserID, req.Token, req.EnterpriseID, req.TokenExpiresAt)
	var be *bitgo.Error
	if errors.As(err, &be) {
		writeError(w, http.StatusUnprocessableEntity, "bitgo_token_rejected")
		return
	}
	if err != nil {
		s.internal(w, "put connection", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"walletsVisible": n})
}

func actor(p authz.Principal) string { return "user:" + p.UserID.String() }

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

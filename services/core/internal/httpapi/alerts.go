package httpapi

import (
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/store"
)

var (
	reCoin   = regexp.MustCompile(`^[a-z0-9:_-]{1,40}$`)
	rePosInt = regexp.MustCompile(`^[1-9][0-9]{0,77}$`)
)

func (s *Server) getAlertSettings(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var out store.AlertSettings
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		out, err = tx.AlertSettings()
		return err
	})
	if err != nil {
		s.internal(w, "alert settings", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putAlertSettings(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var in store.AlertSettings
	if decode(r, &in) != nil || in.DLQThreshold < 1 || in.DLQThreshold > 1000 || in.TokenWarnDays < 1 || in.TokenWarnDays > 90 || len(in.LargeTransfer) > 50 {
		writeError(w, http.StatusBadRequest, "invalid_settings")
		return
	}
	if in.LargeTransfer == nil {
		in.LargeTransfer = map[string]string{}
	}
	for coin, amt := range in.LargeTransfer {
		if !reCoin.MatchString(coin) || !rePosInt.MatchString(amt) {
			writeError(w, http.StatusBadRequest, "invalid_large_transfer_threshold")
			return
		}
	}
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		if err := tx.PutAlertSettings(in); err != nil {
			return err
		}
		return tx.AppendAudit(sc.Org, actor(sc.Principal), "alert_settings.updated", "organization", sc.Org.String(),
			map[string]any{"large_transfer": in.LargeTransfer, "dlq_threshold": in.DLQThreshold, "token_warn_days": in.TokenWarnDays})
	})
	if err != nil {
		s.internal(w, "put alert settings", err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) resolveAlert(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_alert_id")
		return
	}
	var ok bool
	err = s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		if ok, err = tx.ResolveAlert(id); err != nil || !ok {
			return err
		}
		return tx.AppendAudit(sc.Org, actor(sc.Principal), "alert.resolved", "security_alert", id.String(), nil)
	})
	if err != nil {
		s.internal(w, "resolve alert", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "alert_not_found_or_resolved") // includes other tenants' alerts (RLS)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": id})
}

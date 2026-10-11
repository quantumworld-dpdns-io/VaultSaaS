package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/connections"
	"github.com/vaultops/core/internal/rules"
	"github.com/vaultops/core/internal/store"
)

// listWallets refreshes the org's wallets from BitGo with the org's own token,
// stores the balances, and returns the stored view. If BitGo is unreachable
// the last stored balances are returned with stale=true.
func (s *Server) listWallets(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	stale := false
	bg, err := s.conns.ForOrg(r.Context(), sc.Org)
	if errors.Is(err, connections.ErrNotConnected) {
		writeError(w, http.StatusConflict, "bitgo_not_connected")
		return
	}
	if err != nil {
		s.internal(w, "resolve connection", err)
		return
	}
	fresh, err := bg.ListWallets(r.Context())
	if err != nil {
		var be *bitgo.Error
		if !errors.As(err, &be) || be.Retryable {
			s.log.Warn("bitgo unavailable; serving stored balances", "org", sc.Org, "err", err)
		}
		stale = true
	}
	var rows []store.WalletRow
	err = s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		for _, wl := range fresh {
			id, err := tx.UpsertWallet(wl)
			if err != nil {
				return err
			}
			if err := rules.OnWalletSynced(tx, sc.Org, id, wl); err != nil {
				return err
			}
		}
		var err error
		rows, err = tx.ListWallets()
		return err
	})
	if err != nil {
		s.internal(w, "list wallets", err)
		return
	}
	if stale && len(rows) == 0 {
		writeError(w, http.StatusBadGateway, "bitgo_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallets": nonNil(rows), "stale": stale})
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var rows []store.TransferRow
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListTransfers(limit(r, 100, 500))
		return err
	})
	if err != nil {
		s.internal(w, "list transfers", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": nonNil(rows)})
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var rows []store.AlertRow
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListAlerts(limit(r, 100, 500))
		return err
	})
	if err != nil {
		s.internal(w, "list alerts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": nonNil(rows)})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var rows []store.AuditRow
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListAudit(limit(r, 100, 1000))
		return err
	})
	if err != nil {
		s.internal(w, "list audit", err)
		return
	}
	broken, err := s.store.VerifyAuditChain(r.Context(), sc.Org)
	if err != nil {
		s.internal(w, "verify audit", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": nonNil(rows), "chainIntact": broken == 0, "firstBrokenId": broken})
}

func limit(r *http.Request, def, maxN int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, maxN)
}

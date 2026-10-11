package httpapi

import (
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/recon"
	"github.com/vaultops/core/internal/store"
)

const maxImportBytes = 2 << 20

// importExpectedPayments accepts a CSV (text/csv) and imports it all-or-nothing.
func (s *Server) importExpectedPayments(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		writeError(w, http.StatusUnsupportedMediaType, "text_csv_required")
		return
	}
	payments, lineErrs, err := recon.ParseExpectedPayments(io.LimitReader(r.Body, maxImportBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_csv", "detail": err.Error()})
		return
	}
	if len(lineErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_rows", "rows": lineErrs})
		return
	}
	imported, skipped := 0, 0
	err = s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		for _, p := range payments {
			ok, err := tx.UpsertExpectedPayment(p)
			if err != nil {
				return err
			}
			if ok {
				imported++
			} else {
				skipped++ // already settled; left untouched
			}
		}
		return tx.AppendAudit(sc.Org, actor(sc.Principal), "expected_payments.imported", "organization", sc.Org.String(),
			map[string]any{"imported": imported, "skipped_settled": skipped})
	})
	if err != nil {
		s.internal(w, "import expected payments", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skippedSettled": skipped})
}

func (s *Server) listExpectedPayments(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var rows []store.ExpectedPayment
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListExpectedPayments(limit(r, 200, 1000))
		return err
	})
	if err != nil {
		s.internal(w, "list expected payments", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expectedPayments": nonNil(rows)})
}

func (s *Server) runReconciliation(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	if s.recon == nil {
		writeError(w, http.StatusServiceUnavailable, "reconciliation_not_configured")
		return
	}
	sum, err := s.recon.Run(r.Context(), sc.Org, "manual")
	if err != nil {
		s.log.Warn("manual reconciliation failed", "org", sc.Org, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "reconciliation_failed", "runId": sum.RunID})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	var rows []store.RunRow
	err := s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListRuns(limit(r, 20, 200))
		return err
	})
	if err != nil {
		s.internal(w, "list runs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": nonNil(rows)})
}

// listRunItems: RLS limits items to the caller's org, so a foreign run id
// simply yields an empty list.
func (s *Server) listRunItems(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	run, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_run_id")
		return
	}
	var rows []store.ItemRow
	err = s.store.InOrgAs(r.Context(), sc.Org, sc.UserID, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListRunItems(run)
		return err
	})
	if err != nil {
		s.internal(w, "list run items", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(rows)})
}

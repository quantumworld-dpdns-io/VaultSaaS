package httpapi

import (
	"net/http"
	"time"

	"github.com/vaultops/core/internal/authz"
	"github.com/vaultops/core/internal/report"
)

const reportURLTTL = 5 * time.Minute

// dailyReport regenerates the org's report for ?date=YYYY-MM-DD (Asia/Taipei)
// and returns a short-lived R2 download URL, or the CSV itself when running
// against a local directory.
func (s *Server) dailyReport(w http.ResponseWriter, r *http.Request) {
	sc := authz.ScopeFrom(r.Context())
	if s.reports == nil {
		writeError(w, http.StatusServiceUnavailable, "reports_not_configured")
		return
	}
	day, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), report.Taipei)
	now := time.Now().In(report.Taipei)
	if err != nil || day.After(now) || now.Sub(day) > 400*24*time.Hour {
		writeError(w, http.StatusBadRequest, "invalid_date")
		return
	}
	key, body, err := report.Publish(r.Context(), s.store, s.reports, sc.Org, day)
	if err != nil {
		s.internal(w, "publish report", err)
		return
	}
	url, err := s.reports.PresignGet(r.Context(), key, reportURLTTL)
	if err != nil {
		s.internal(w, "presign report", err)
		return
	}
	if url != "" {
		writeJSON(w, http.StatusOK, map[string]any{"url": url, "expiresAt": time.Now().Add(reportURLTTL)})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vaultops-`+day.Format("2006-01-02")+`.csv"`)
	_, _ = w.Write(body)
}

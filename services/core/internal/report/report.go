// Package report builds the daily treasury report: the day's transfers and the
// reconciliation findings raised that day, as CSV (plan §5.2).
package report

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/objects"
	"github.com/vaultops/core/internal/store"
)

// Taipei is the reporting day boundary for Taiwan/APAC customers (D6).
var Taipei = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Taipei"); err == nil {
		return l
	}
	return time.FixedZone("Asia/Taipei", 8*3600)
}()

var numeric = regexp.MustCompile(`^-?[0-9]+$`)

// SafeCell neutralises spreadsheet formula injection: a cell that starts with
// = + - @ TAB or CR is prefixed with a quote, except plain integers (amounts).
func SafeCell(s string) string {
	if s == "" || numeric.MatchString(s) {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func Key(org uuid.UUID, day time.Time) string {
	return fmt.Sprintf("reports/%s/%s.csv", org, day.In(Taipei).Format("2006-01-02"))
}

// Build renders the report for one org and calendar day (Asia/Taipei).
func Build(ctx context.Context, s *store.Store, org uuid.UUID, day time.Time) ([]byte, error) {
	d := day.In(Taipei)
	from := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, Taipei)
	to := from.Add(24 * time.Hour)
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	write := func(rec ...string) {
		for i := range rec {
			rec[i] = SafeCell(rec[i])
		}
		_ = w.Write(rec)
	}
	write("record_type", "timestamp", "provider_transfer_id", "coin", "direction", "amount_base_units", "state", "finding", "reference")
	err := s.InOrg(ctx, org, func(tx *store.Tx) error {
		transfers, err := tx.TransfersBetween(from, to)
		if err != nil {
			return err
		}
		for _, t := range transfers {
			write("transfer", t.OccurredAt.UTC().Format(time.RFC3339), t.ProviderTransferID, t.Coin, t.Direction,
				t.Amount.String(), string(t.State), "", "")
		}
		items, err := tx.ItemsBetween(from, to)
		if err != nil {
			return err
		}
		for _, it := range items {
			write("finding", it.CreatedAt.UTC().Format(time.RFC3339), deref(it.ProviderTransferID), "", "", "", "",
				it.Kind, deref(it.Reference))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// Publish builds and stores the report, returning its key.
func Publish(ctx context.Context, s *store.Store, objs objects.Store, org uuid.UUID, day time.Time) (string, []byte, error) {
	body, err := Build(ctx, s, org, day)
	if err != nil {
		return "", nil, err
	}
	key := Key(org, day)
	return key, body, objs.Put(ctx, key, body)
}

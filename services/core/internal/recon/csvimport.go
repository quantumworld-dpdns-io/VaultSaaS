package recon

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/store"
)

const MaxImportRows = 5000

var (
	reAmount = regexp.MustCompile(`^[1-9][0-9]{0,77}$`) // positive base units, fits NUMERIC(78,0)
	reCoin   = regexp.MustCompile(`^[a-z0-9:_-]{1,40}$`)
	reAddr   = regexp.MustCompile(`^[A-Za-z0-9:_.-]{1,200}$`)
)

type LineError struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

// ParseExpectedPayments reads "reference,coin,address,amount[,expected_by]"
// (header row required). Amounts are integer base units. expected_by is
// RFC 3339 or YYYY-MM-DD (end of that day, Asia/Taipei). The whole file is
// rejected if any line is invalid.
func ParseExpectedPayments(r io.Reader) ([]store.ExpectedPayment, []LineError, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, nil, errors.New("empty or unreadable CSV")
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	for _, need := range []string{"reference", "coin", "address", "amount"} {
		if _, ok := col[need]; !ok {
			return nil, nil, fmt.Errorf("missing column %q", need)
		}
	}
	tz, _ := time.LoadLocation("Asia/Taipei")
	if tz == nil {
		tz = time.FixedZone("Asia/Taipei", 8*3600)
	}
	var out []store.ExpectedPayment
	var errs []LineError
	seen := map[string]bool{}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, LineError{line, "malformed CSV"})
			continue
		}
		if len(out)+len(errs) >= MaxImportRows {
			return nil, nil, fmt.Errorf("at most %d rows per import", MaxImportRows)
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		p := store.ExpectedPayment{Reference: get("reference"), Coin: strings.ToLower(get("coin")),
			Address: domain.NormalizeAddress(get("address")), Amount: get("amount")}
		switch {
		case p.Reference == "" || len(p.Reference) > 200:
			errs = append(errs, LineError{line, "reference is required (max 200 chars)"})
			continue
		case seen[p.Reference]:
			errs = append(errs, LineError{line, "duplicate reference in file"})
			continue
		case !reCoin.MatchString(p.Coin):
			errs = append(errs, LineError{line, "invalid coin"})
			continue
		case !reAddr.MatchString(p.Address):
			errs = append(errs, LineError{line, "invalid address"})
			continue
		case !reAmount.MatchString(p.Amount):
			errs = append(errs, LineError{line, "amount must be a positive integer in base units"})
			continue
		}
		if v := get("expected_by"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				d, derr := time.ParseInLocation("2006-01-02", v, tz)
				if derr != nil {
					errs = append(errs, LineError{line, "expected_by must be RFC 3339 or YYYY-MM-DD"})
					continue
				}
				t = d.Add(24*time.Hour - time.Second)
			}
			p.ExpectedBy = &t
		}
		seen[p.Reference] = true
		out = append(out, p)
	}
	return out, errs, nil
}

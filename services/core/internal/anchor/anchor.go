// Package anchor writes the head of every organization's audit hash chain to
// an object store outside the database — the R2 bucket with a bucket lock
// (plan §4) — and checks earlier anchors against the database.
//
// The in-database chain detects edits to single rows. An attacker with owner
// rights could recompute the whole chain; anchors written to locked storage
// cannot be rewritten, so a recomputed chain no longer matches them.
package anchor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/store"
)

// ObjectStore is the minimal object API (R2 via S3, or a local directory).
type ObjectStore interface {
	Put(ctx context.Context, key string, body []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
}

type Record struct {
	OrganizationID uuid.UUID `json:"organizationId"`
	LastID         int64     `json:"lastId"`
	HeadHash       string    `json:"headHash"`
	Entries        int64     `json:"entries"`
	ChainIntact    bool      `json:"chainIntact"`
	AnchoredAt     time.Time `json:"anchoredAt"`
}

type Anchorer struct {
	Store   *store.Store
	Objects ObjectStore
	Log     *slog.Logger
	Now     func() time.Time
}

type Result struct {
	Anchored int
	Problems []string // human-readable integrity problems (also raised as alerts)
}

func prefix(org uuid.UUID) string { return "anchors/" + org.String() + "/" }

// Run verifies every org's chain and its most recent previous anchor, then
// writes a new anchor. Keys are timestamped so locked objects are never overwritten.
func (a *Anchorer) Run(ctx context.Context) (Result, error) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	heads, err := a.Store.AuditHeads(ctx)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, h := range heads {
		broken, err := a.Store.VerifyAuditChain(ctx, h.OrganizationID)
		if err != nil {
			return res, err
		}
		if broken != 0 {
			res.Problems = append(res.Problems, a.alert(ctx, h.OrganizationID, "audit_chain_broken",
				fmt.Sprintf("audit log row %d does not match the hash chain", broken), map[string]any{"first_broken_id": broken}))
		}
		if msg, err := a.checkPrevious(ctx, h.OrganizationID); err != nil {
			return res, err
		} else if msg != "" {
			res.Problems = append(res.Problems, a.alert(ctx, h.OrganizationID, "audit_anchor_mismatch", msg, nil))
		}
		rec := Record{OrganizationID: h.OrganizationID, LastID: h.LastID, HeadHash: hex.EncodeToString(h.HeadHash),
			Entries: h.Entries, ChainIntact: broken == 0, AnchoredAt: now().UTC()}
		body, _ := json.MarshalIndent(rec, "", "  ")
		key := fmt.Sprintf("%s%s.json", prefix(h.OrganizationID), rec.AnchoredAt.Format("20060102T150405.000000000Z"))
		if err := a.Objects.Put(ctx, key, body); err != nil {
			return res, err
		}
		res.Anchored++
	}
	return res, nil
}

// checkPrevious compares the latest earlier anchor with the database row it names.
func (a *Anchorer) checkPrevious(ctx context.Context, org uuid.UUID) (string, error) {
	keys, err := a.Objects.List(ctx, prefix(org))
	if err != nil || len(keys) == 0 {
		return "", err
	}
	sort.Strings(keys)
	body, err := a.Objects.Get(ctx, keys[len(keys)-1])
	if err != nil {
		return "", err
	}
	var prev Record
	if err := json.Unmarshal(body, &prev); err != nil {
		return "anchor " + keys[len(keys)-1] + " is unreadable", nil
	}
	want, _ := hex.DecodeString(prev.HeadHash)
	var got []byte
	err = a.Store.InOrg(ctx, org, func(tx *store.Tx) error {
		var err error
		got, err = tx.AuditHash(prev.LastID)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Sprintf("anchored audit row %d no longer exists", prev.LastID), nil
	}
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, want) {
		return fmt.Sprintf("audit history was rewritten: row %d no longer matches the anchor of %s", prev.LastID, prev.AnchoredAt.Format(time.RFC3339)), nil
	}
	return "", nil
}

func (a *Anchorer) alert(ctx context.Context, org uuid.UUID, kind, msg string, data map[string]any) string {
	err := a.Store.InOrg(ctx, org, func(tx *store.Tx) error {
		return tx.RaiseAlert(org, kind, "critical", msg, data, kind)
	})
	if err != nil && a.Log != nil {
		a.Log.Error("raise alert", "err", err)
	}
	return org.String() + ": " + msg
}

// Dir is a local ObjectStore for development and tests.
type Dir struct{ Root string }

func (d Dir) path(key string) string {
	return filepath.Join(d.Root, filepath.FromSlash(strings.TrimPrefix(key, "/")))
}

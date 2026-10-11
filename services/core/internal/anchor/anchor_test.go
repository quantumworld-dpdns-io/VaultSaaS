package anchor_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vaultops/core/internal/anchor"
	"github.com/vaultops/core/internal/objects"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

func TestAnchorDetectsRecomputedHistory(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	u := storetest.CreateUser(t, owner, "a@example.com")
	org, _, err := s.CreateOrganizationWithOwner(ctx, "Acme", u)
	if err != nil {
		t.Fatal(err)
	}
	appendN := func(n int) {
		for i := 0; i < n; i++ {
			if err := s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
				return tx.AppendAudit(org.ID, "test", "test.event", "x", "1", map[string]any{"i": i})
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendN(3)
	objs := objects.Dir{Root: t.TempDir(), WriteOnce: true}
	a := &anchor.Anchorer{Store: s, Objects: objs}

	res, err := a.Run(ctx)
	if err != nil || res.Anchored != 1 || len(res.Problems) != 0 {
		t.Fatalf("first anchor: %+v %v", res, err)
	}
	appendN(2)
	if res, err = a.Run(ctx); err != nil || len(res.Problems) != 0 {
		t.Fatalf("honest growth must pass: %+v %v", res, err)
	}

	// An attacker with owner rights rewrites an early row AND recomputes every
	// hash after it, so the in-database chain verifies again.
	c := storetest.Owner(t, owner)
	_, err = c.Exec(ctx, `
		ALTER TABLE app.audit_logs DISABLE TRIGGER audit_no_update;
		DO $$ DECLARE r app.audit_logs%ROWTYPE; prev bytea; BEGIN
		  UPDATE app.audit_logs SET data = '{"i": "forged"}' WHERE id = (SELECT min(id) + 1 FROM app.audit_logs);
		  FOR r IN SELECT * FROM app.audit_logs ORDER BY id LOOP
		    UPDATE app.audit_logs SET prev_hash = prev, hash = app.audit_row_digest(r, prev) WHERE id = r.id;
		    prev := app.audit_row_digest(r, prev);
		  END LOOP; END $$;
		ALTER TABLE app.audit_logs ENABLE TRIGGER audit_no_update;`)
	if err != nil {
		t.Fatal(err)
	}
	if broken, _ := s.VerifyAuditChain(ctx, org.ID); broken != 0 {
		t.Fatalf("precondition: recomputed chain should verify internally, broken=%d", broken)
	}
	res, err = a.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "rewritten") {
		t.Fatalf("anchor must catch the rewrite: %+v", res)
	}
	var alerts int
	c.QueryRow(ctx, `SELECT count(*) FROM app.security_alerts WHERE kind = 'audit_anchor_mismatch' AND severity = 'critical'`).Scan(&alerts)
	if alerts != 1 {
		t.Fatalf("critical alert expected, got %d", alerts)
	}
}

func TestDirRefusesOverwrite(t *testing.T) {
	d := objects.Dir{Root: t.TempDir(), WriteOnce: true}
	ctx := context.Background()
	if err := d.Put(ctx, "anchors/x.json", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(ctx, "anchors/x.json", []byte("2")); err == nil {
		t.Fatal("anchors must be write-once")
	}
}

func TestDirListHandlesUncleanRoot(t *testing.T) {
	base := t.TempDir()
	d := objects.Dir{Root: base + "/sub/../anchors", WriteOnce: true}
	ctx := context.Background()
	if err := d.Put(ctx, "anchors/org/a.json", []byte("1")); err != nil {
		t.Fatal(err)
	}
	keys, err := d.List(ctx, "anchors/org/")
	if err != nil || len(keys) != 1 || keys[0] != "anchors/org/a.json" {
		t.Fatalf("list with a non-clean root: %v %v", keys, err)
	}
}

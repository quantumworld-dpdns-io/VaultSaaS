package ingest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/bitgo/bitgomock"
	"github.com/vaultops/core/internal/ingest"
	"github.com/vaultops/core/internal/store/storetest"
)

// End-to-end against Postgres: ten deliveries of the same webhook book one
// transfer; a later state change advances it.
func TestIngestAgainstPostgres(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	org, token, err := s.CreateOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	m := bitgomock.New("bg")
	srv := httptest.NewServer(m)
	defer srv.Close()
	client := bitgo.New(srv.URL, "bg")
	p := &ingest.Processor{Store: ingest.PG{S: s}, BitGoFor: func(context.Context, uuid.UUID) (ingest.BitGo, error) { return client, nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	tr, _ := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_eth_treasury", ValueString: "2500000000000000000", State: "unconfirmed"})
	body := func(state string) string {
		b, _ := json.Marshal(map[string]string{"type": "transfer", "wallet": tr.Wallet, "transfer": tr.ID, "coin": tr.Coin, "state": state})
		return string(b)
	}
	for i := 0; i < 10; i++ {
		if err := p.Handle(ctx, ingest.Message{V: 1, Provider: "bitgo", OrgToken: token, ReceivedAt: time.Now(), Body: body("unconfirmed")}, 1); err != nil {
			t.Fatal(err)
		}
	}
	m.SetTransferState(tr.Wallet, tr.ID, "confirmed")
	if err := p.Handle(ctx, ingest.Message{V: 1, Provider: "bitgo", OrgToken: token, ReceivedAt: time.Now(), Body: body("confirmed")}, 1); err != nil {
		t.Fatal(err)
	}

	oc, err := pgx.Connect(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer oc.Close(ctx)
	var n, version, events, attempts int
	var state, amount string
	oc.QueryRow(ctx, `SELECT count(*), max(state), max(amount::text), max(version) FROM app.transfers WHERE organization_id = $1`, org.ID).
		Scan(&n, &state, &amount, &version)
	oc.QueryRow(ctx, `SELECT count(*), max(attempts) FROM app.webhook_events WHERE organization_id = $1`, org.ID).Scan(&events, &attempts)
	if n != 1 || state != "confirmed" || amount != "2500000000000000000" || version != 2 {
		t.Fatalf("transfers=%d state=%s amount=%s version=%d", n, state, amount, version)
	}
	if events != 2 || attempts != 10 {
		t.Fatalf("webhook_events=%d max(attempts)=%d", events, attempts)
	}
}

package ingest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/bitgo/bitgomock"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/store"
)

// ---- in-memory fakes ----------------------------------------------------

type fakeStore struct {
	org       store.Organization
	token     string
	events    map[string]*store.WebhookEvent
	wallets   map[string]uuid.UUID
	transfers map[string]*domain.Transfer
	alerts    []string
	audit     []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		org: store.Organization{ID: uuid.New(), Name: "Acme"}, token: "tok",
		events: map[string]*store.WebhookEvent{}, wallets: map[string]uuid.UUID{}, transfers: map[string]*domain.Transfer{},
	}
}

func (f *fakeStore) OrganizationByWebhookToken(_ context.Context, t string) (store.Organization, error) {
	if t != f.token {
		return store.Organization{}, store.ErrNotFound
	}
	return f.org, nil
}
func (f *fakeStore) InOrg(_ context.Context, _ uuid.UUID, fn func(OrgTx) error) error { return fn(f) }

func (f *fakeStore) RecordWebhookEvent(key string, _ []byte, _ time.Time) (store.WebhookEvent, error) {
	if ev, ok := f.events[key]; ok {
		ev.Attempts++
		return *ev, nil
	}
	ev := &store.WebhookEvent{ID: uuid.New(), Status: "received", Attempts: 1}
	f.events[key] = ev
	return *ev, nil
}
func (f *fakeStore) MarkWebhookEvent(id uuid.UUID, status, _ string) error {
	for _, ev := range f.events {
		if ev.ID == id {
			ev.Status = status
		}
	}
	return nil
}
func (f *fakeStore) WalletByProviderID(id string) (*store.Wallet, error) {
	if w, ok := f.wallets[id]; ok {
		return &store.Wallet{ID: w, ProviderWalletID: id}, nil
	}
	return nil, nil
}
func (f *fakeStore) UpsertWallet(w bitgo.Wallet) (uuid.UUID, error) {
	if id, ok := f.wallets[w.ID]; ok {
		return id, nil
	}
	f.wallets[w.ID] = uuid.New()
	return f.wallets[w.ID], nil
}
func (f *fakeStore) TransferForUpdate(w uuid.UUID, id string) (*domain.Transfer, error) {
	if t, ok := f.transfers[w.String()+id]; ok {
		c := *t
		return &c, nil
	}
	return nil, nil
}
func (f *fakeStore) InsertTransfer(t *domain.Transfer) (uuid.UUID, error) {
	c := *t
	c.ID, c.Version = uuid.New(), 1
	f.transfers[t.WalletID.String()+t.ProviderTransferID] = &c
	return c.ID, nil
}
func (f *fakeStore) UpdateTransfer(t *domain.Transfer) (bool, error) {
	c := *t
	c.Version++
	f.transfers[t.WalletID.String()+t.ProviderTransferID] = &c
	return true, nil
}
func (f *fakeStore) AppendAudit(_ uuid.UUID, _, action, _, _ string, _ map[string]any) error {
	f.audit = append(f.audit, action)
	return nil
}
func (f *fakeStore) RaiseAlert(_ uuid.UUID, kind, _, _ string, _ map[string]any, _ string) error {
	f.alerts = append(f.alerts, kind)
	return nil
}

func setup(t *testing.T) (*Processor, *fakeStore, *bitgomock.Server) {
	t.Helper()
	m := bitgomock.New("bg")
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	fs := newFakeStore()
	return &Processor{Store: fs, BitGo: bitgo.New(srv.URL, "bg", bitgo.WithMaxRetries(0)), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, fs, m
}

func msg(t *testing.T, body map[string]any) Message {
	t.Helper()
	b, _ := json.Marshal(body)
	return Message{V: 1, Provider: "bitgo", OrgToken: "tok", ReceivedAt: time.Now(), Body: string(b)}
}

// ---- tests -----------------------------------------------------------------

// Core principle 2: state and amount come from BitGo, never from the body.
func TestWebhookPayloadIsNotTrusted(t *testing.T) {
	p, fs, m := setup(t)
	tr, _ := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", ValueString: "5000", State: "unconfirmed"})
	lie := msg(t, map[string]any{"type": "transfer", "wallet": tr.Wallet, "transfer": tr.ID, "coin": tr.Coin,
		"state": "confirmed", "valueString": "999999999"})
	if err := p.Handle(context.Background(), lie, 1); err != nil {
		t.Fatal(err)
	}
	if len(fs.transfers) != 1 {
		t.Fatalf("want 1 transfer, got %d", len(fs.transfers))
	}
	for _, got := range fs.transfers {
		if got.State != domain.StatePending || got.Amount.Cmp(big.NewInt(5000)) != 0 {
			t.Fatalf("booked from webhook body instead of BitGo: %+v", got)
		}
	}
	reqs := strings.Join(m.Requests, "\n")
	if !strings.Contains(reqs, "GET /api/v2/tbtc4/wallet/wlt_btc_ops/transfer/"+tr.ID) {
		t.Fatalf("worker did not re-fetch the transfer; requests:\n%s", reqs)
	}
}

func TestDuplicateDeliveriesBookOnce(t *testing.T) {
	p, fs, m := setup(t)
	tr, _ := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", ValueString: "1"})
	d := msg(t, map[string]any{"type": "transfer", "wallet": tr.Wallet, "transfer": tr.ID, "coin": tr.Coin})
	for i := 0; i < 10; i++ {
		if err := p.Handle(context.Background(), d, 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(fs.transfers) != 1 || len(fs.audit) != 1 {
		t.Fatalf("transfers=%d audit=%v", len(fs.transfers), fs.audit)
	}
}

func TestForgedTransferIsRejectedWithAlert(t *testing.T) {
	p, fs, _ := setup(t)
	forged := msg(t, map[string]any{"type": "transfer", "wallet": "wlt_btc_ops", "transfer": "does_not_exist", "coin": "tbtc4"})
	if err := p.Handle(context.Background(), forged, 1); err != nil {
		t.Fatal(err)
	}
	if len(fs.transfers) != 0 || len(fs.alerts) != 1 || fs.alerts[0] != "webhook_unverifiable" {
		t.Fatalf("transfers=%d alerts=%v", len(fs.transfers), fs.alerts)
	}
}

func TestBitGoOutageRetriesThenAlertsOnLastAttempt(t *testing.T) {
	p, fs, m := setup(t)
	tr, _ := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", ValueString: "1"})
	d := msg(t, map[string]any{"type": "transfer", "wallet": tr.Wallet, "transfer": tr.ID, "coin": tr.Coin})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__mock/fail", strings.NewReader(`{"Count":100}`)))
	for i := 1; i <= MaxReceives; i++ {
		if err := p.Handle(context.Background(), d, i); err == nil {
			t.Fatalf("attempt %d: expected retryable error", i)
		}
		if i < MaxReceives && len(fs.alerts) != 0 {
			t.Fatalf("alert raised too early at attempt %d", i)
		}
	}
	if len(fs.alerts) != 1 || fs.alerts[0] != "webhook_dead_lettered" {
		t.Fatalf("alerts %v", fs.alerts)
	}
}

func TestUnknownTokenAndNonTransferEventsAreDropped(t *testing.T) {
	p, fs, _ := setup(t)
	bad := msg(t, map[string]any{"type": "transfer"})
	bad.OrgToken = "unknown"
	if err := p.Handle(context.Background(), bad, 1); err != nil || len(fs.events) != 0 {
		t.Fatalf("unknown token must be dropped without recording: %v", err)
	}
	other := msg(t, map[string]any{"type": "block", "hash": "abc"})
	if err := p.Handle(context.Background(), other, 1); err != nil {
		t.Fatal(err)
	}
	for _, ev := range fs.events {
		if ev.Status != "ignored" {
			t.Fatalf("non-transfer event status %s", ev.Status)
		}
	}
}

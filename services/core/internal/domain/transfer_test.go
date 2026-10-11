package domain

import (
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
)

type memLedger struct {
	transfers map[string]*Transfer
	audit     []string
	alerts    []string
	staleNext bool
}

func newMem() *memLedger { return &memLedger{transfers: map[string]*Transfer{}} }

func (m *memLedger) TransferForUpdate(w uuid.UUID, id string) (*Transfer, error) {
	t, ok := m.transfers[w.String()+id]
	if !ok {
		return nil, nil
	}
	c := *t
	return &c, nil
}
func (m *memLedger) InsertTransfer(t *Transfer) (uuid.UUID, error) {
	c := *t
	c.ID, c.Version = uuid.New(), 1
	m.transfers[t.WalletID.String()+t.ProviderTransferID] = &c
	return c.ID, nil
}
func (m *memLedger) UpdateTransfer(t *Transfer) (bool, error) {
	cur := m.transfers[t.WalletID.String()+t.ProviderTransferID]
	if m.staleNext || cur.Version != t.Version {
		return false, nil
	}
	c := *t
	c.Version++
	m.transfers[t.WalletID.String()+t.ProviderTransferID] = &c
	return true, nil
}
func (m *memLedger) AppendAudit(_ uuid.UUID, _, action, _, _ string, _ map[string]any) error {
	m.audit = append(m.audit, action)
	return nil
}
func (m *memLedger) RaiseAlert(_ uuid.UUID, kind, _, _ string, _ map[string]any, _ string) error {
	m.alerts = append(m.alerts, kind)
	return nil
}

var org, wallet = uuid.New(), uuid.New()

func obs(t *testing.T, state, value string) *Transfer {
	t.Helper()
	tr, err := FromProvider(org, wallet, &bitgo.Transfer{ID: "tr1", Coin: "tbtc4", Type: "receive", ValueString: value, State: state, Confirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestStateMachine(t *testing.T) {
	cases := []struct {
		from, to State
		ok       bool
	}{
		{StatePending, StateConfirmed, true}, {StatePending, StateFailed, true},
		{StateConfirmed, StateFailed, true}, {StateConfirmed, StatePending, true},
		{StateFailed, StateConfirmed, false}, {StateFailed, StatePending, false},
		{StateConfirmed, StateConfirmed, true},
	}
	for _, c := range cases {
		if CanTransition(c.from, c.to) != c.ok {
			t.Errorf("%s -> %s: want %v", c.from, c.to, c.ok)
		}
	}
}

func TestMapProviderState(t *testing.T) {
	for in, want := range map[string]State{"unconfirmed": StatePending, "pendingApproval": StatePending, "confirmed": StateConfirmed, "removed": StateFailed} {
		if got, _ := MapProviderState(in); got != want {
			t.Errorf("%s: got %s", in, got)
		}
	}
	if _, err := MapProviderState("bogus"); err == nil {
		t.Error("unknown states must error")
	}
}

func TestAmountsAreExactBigIntegers(t *testing.T) {
	tr := obs(t, "confirmed", "115792089237316195423570985008687907853269984665640564039457584007913129639935") // 2^256-1
	want, _ := new(big.Int).SetString("115792089237316195423570985008687907853269984665640564039457584007913129639935", 10)
	if tr.Amount.Cmp(want) != 0 {
		t.Fatal("amount lost precision")
	}
	if _, err := FromProvider(org, wallet, &bitgo.Transfer{ID: "x", Type: "receive", ValueString: "1.5", State: "confirmed"}); err == nil {
		t.Fatal("fractional base units must be rejected")
	}
}

func TestApplyObservationIsIdempotent(t *testing.T) {
	l := newMem()
	for i := 0; i < 10; i++ {
		out, err := ApplyObservation(l, "test", obs(t, "confirmed", "1000"))
		if err != nil {
			t.Fatal(err)
		}
		if (i == 0) != (out == OutcomeBooked) {
			t.Fatalf("iteration %d: outcome %s", i, out)
		}
	}
	if len(l.transfers) != 1 || len(l.audit) != 1 {
		t.Fatalf("want 1 transfer + 1 audit entry, got %d / %v", len(l.transfers), l.audit)
	}
}

func TestPendingThenConfirmed(t *testing.T) {
	l := newMem()
	ApplyObservation(l, "test", obs(t, "unconfirmed", "1000"))
	out, err := ApplyObservation(l, "test", obs(t, "confirmed", "1000"))
	if err != nil || out != OutcomeUpdated {
		t.Fatalf("got %s %v", out, err)
	}
	for _, tr := range l.transfers {
		if tr.State != StateConfirmed || tr.Version != 2 {
			t.Fatalf("got %+v", tr)
		}
	}
	if len(l.alerts) != 0 {
		t.Fatalf("no alerts expected, got %v", l.alerts)
	}
}

func TestFailedIsTerminalAndAlerts(t *testing.T) {
	l := newMem()
	ApplyObservation(l, "test", obs(t, "failed", "1000"))
	out, err := ApplyObservation(l, "test", obs(t, "confirmed", "1000"))
	if err != nil || out != OutcomeRejected {
		t.Fatalf("got %s %v", out, err)
	}
	if len(l.alerts) != 1 || l.alerts[0] != "illegal_transition" {
		t.Fatalf("alerts %v", l.alerts)
	}
}

func TestAmountChangeIsRejectedNotRewritten(t *testing.T) {
	l := newMem()
	ApplyObservation(l, "test", obs(t, "unconfirmed", "1000"))
	out, err := ApplyObservation(l, "test", obs(t, "confirmed", "999"))
	if err != nil || out != OutcomeRejected {
		t.Fatalf("got %s %v", out, err)
	}
	for _, tr := range l.transfers {
		if tr.Amount.String() != "1000" {
			t.Fatal("amount was rewritten")
		}
	}
	if l.alerts[0] != "transfer_mutated" {
		t.Fatalf("alerts %v", l.alerts)
	}
}

func TestConfirmedReversalRaisesAlert(t *testing.T) {
	l := newMem()
	ApplyObservation(l, "test", obs(t, "confirmed", "1000"))
	if _, err := ApplyObservation(l, "test", obs(t, "removed", "1000")); err != nil {
		t.Fatal(err)
	}
	if len(l.alerts) != 1 || l.alerts[0] != "confirmed_transfer_reverted" {
		t.Fatalf("alerts %v", l.alerts)
	}
}

func TestOptimisticLockConflict(t *testing.T) {
	l := newMem()
	ApplyObservation(l, "test", obs(t, "unconfirmed", "1000"))
	l.staleNext = true
	if _, err := ApplyObservation(l, "test", obs(t, "confirmed", "1000")); err != ErrConcurrentUpdate {
		t.Fatalf("want ErrConcurrentUpdate, got %v", err)
	}
}

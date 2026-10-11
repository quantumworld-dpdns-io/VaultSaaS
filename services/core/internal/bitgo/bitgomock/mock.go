// Package bitgomock is an in-memory fake of the BitGo v2 endpoints VaultOps
// uses. It backs unit tests, cmd/mockbitgo and the Robot Framework suites
// until a real BitGo testnet account exists (decision D9).
//
// Test-control endpoints live under /__mock/ and are not part of BitGo.
package bitgomock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vaultops/core/internal/bitgo"
)

type Server struct {
	Token string

	mu        sync.Mutex
	wallets   []bitgo.Wallet
	transfers map[string][]bitgo.Transfer // wallet id -> newest first
	failNext  int                         // number of upcoming transfer reads that return 500
	approvals map[string]map[string]any
	seq       int
	Requests  []string // "METHOD path" log, for assertions
}

func New(token string) *Server {
	s := &Server{Token: token}
	s.Reset()
	return s
}

// Reset restores the seed data.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wallets = []bitgo.Wallet{
		{ID: "wlt_btc_ops", Coin: "tbtc4", Label: "BTC Operations", Type: "hot", MultisigType: "onchain",
			BalanceString: "150000000", ConfirmedBalanceString: "150000000", SpendableBalanceString: "150000000", Enterprise: "ent_demo"},
		{ID: "wlt_eth_treasury", Coin: "hteth", Label: "ETH Treasury", Type: "custodial", MultisigType: "tss",
			BalanceString: "2500000000000000000", ConfirmedBalanceString: "2500000000000000000", SpendableBalanceString: "2000000000000000000", Enterprise: "ent_demo"},
	}
	s.transfers = map[string][]bitgo.Transfer{}
	s.approvals = map[string]map[string]any{}
	s.failNext = 0
	s.seq = 0
	s.Requests = nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/__mock/") {
		s.control(w, r)
		return
	}
	s.mu.Lock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/wallets":
		s.listWallets(w, r)
	case r.Method == http.MethodGet && len(parts) == 5 && parts[3] == "wallet":
		s.getWallet(w, parts[2], parts[4])
	case r.Method == http.MethodGet && len(parts) == 6 && parts[5] == "transfer":
		s.listTransfers(w, r, parts[4])
	case r.Method == http.MethodGet && len(parts) == 7 && parts[5] == "transfer":
		s.getTransfer(w, parts[4], parts[6])
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/pendingapprovals":
		s.listApprovals(w)
	case r.Method == http.MethodPut && len(parts) == 4 && parts[2] == "pendingapprovals":
		s.decideApproval(w, r, parts[3])
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) listWallets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 25
	}
	start := 0
	if prev := r.URL.Query().Get("prevId"); prev != "" {
		for i, wl := range s.wallets {
			if wl.ID == prev {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(s.wallets))
	page := s.wallets[start:end]
	next := ""
	if end < len(s.wallets) {
		next = page[len(page)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallets": page, "nextBatchPrevId": next})
}

func (s *Server) getWallet(w http.ResponseWriter, coin, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, wl := range s.wallets {
		if wl.ID == id && wl.Coin == coin {
			writeJSON(w, http.StatusOK, wl)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "wallet not found"})
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request, walletID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumeFailure(w) {
		return
	}
	all := s.transfers[walletID]
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 25
	}
	start := 0
	if prev := r.URL.Query().Get("prevId"); prev != "" {
		for i, t := range all {
			if t.ID == prev {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(all))
	page := append([]bitgo.Transfer{}, all[start:end]...)
	next := ""
	if end < len(all) {
		next = page[len(page)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": page, "nextBatchPrevId": next})
}

func (s *Server) getTransfer(w http.ResponseWriter, walletID, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumeFailure(w) {
		return
	}
	for _, t := range s.transfers[walletID] {
		if t.ID == id {
			writeJSON(w, http.StatusOK, t)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "transfer not found"})
}

func (s *Server) consumeFailure(w http.ResponseWriter) bool {
	if s.failNext > 0 {
		s.failNext--
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "injected failure"})
		return true
	}
	return false
}

func (s *Server) listApprovals(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]map[string]any, 0, len(s.approvals))
	for _, a := range s.approvals {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i]["id"].(string) < list[j]["id"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"pendingApprovals": list})
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		State string `json:"state"`
		OTP   string `json:"otp"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.approvals[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "approval not found"})
		return
	}
	if body.State != "approved" && body.State != "rejected" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid state"})
		return
	}
	if a["state"] != "pending" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "approval already decided"})
		return
	}
	if body.OTP != "000000" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "needs unlock / invalid otp"})
		return
	}
	a["state"] = body.State
	writeJSON(w, http.StatusOK, a)
}

// ---- test control -------------------------------------------------------

// AddTransferRequest creates a transfer and optionally POSTs a BitGo-style
// webhook to NotifyURL.
type AddTransferRequest struct {
	ID          string `json:"id"`
	WalletID    string `json:"walletId"`
	Type        string `json:"type"`
	ValueString string `json:"valueString"`
	State       string `json:"state"`
	Address     string `json:"address"`
	NotifyURL   string `json:"notifyUrl"`
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/__mock/reset":
		s.Reset()
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	case "/__mock/transfers":
		var req AddTransferRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		t, err := s.AddTransfer(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.NotifyURL != "" {
			if err := SendWebhook(req.NotifyURL, t); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusCreated, t)
	case "/__mock/transfers/state":
		var req struct{ WalletID, ID, State string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !s.SetTransferState(req.WalletID, req.ID, req.State) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "transfer not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	case "/__mock/fail":
		var req struct{ Count int }
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.failNext = req.Count
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]int{"failNext": req.Count})
	case "/__mock/approvals":
		var a map[string]any
		_ = json.NewDecoder(r.Body).Decode(&a)
		s.mu.Lock()
		if _, ok := a["id"]; !ok {
			s.seq++
			a["id"] = fmt.Sprintf("pa_%04d", s.seq)
		}
		a["state"] = "pending"
		s.approvals[a["id"].(string)] = a
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, a)
	case "/__mock/requests":
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"requests": s.Requests})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown control endpoint"})
	}
}

func (s *Server) AddTransfer(req AddTransferRequest) (bitgo.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var wl *bitgo.Wallet
	for i := range s.wallets {
		if s.wallets[i].ID == req.WalletID {
			wl = &s.wallets[i]
		}
	}
	if wl == nil {
		return bitgo.Transfer{}, fmt.Errorf("unknown wallet %q", req.WalletID)
	}
	s.seq++
	if req.ID == "" {
		req.ID = fmt.Sprintf("tr_%06d", s.seq)
	}
	if req.Type == "" {
		req.Type = "receive"
	}
	if req.State == "" {
		req.State = "confirmed"
	}
	if req.Address == "" {
		req.Address = "addr_" + req.ID
	}
	t := bitgo.Transfer{
		ID: req.ID, Coin: wl.Coin, Wallet: wl.ID, TxID: fmt.Sprintf("tx%062d", s.seq),
		Height: 100000 + int64(s.seq), Date: time.Now().UTC().Truncate(time.Second), Confirmations: 6,
		Type: req.Type, ValueString: req.ValueString, State: req.State,
		Entries: []bitgo.TransferEntry{{Address: req.Address, ValueString: req.ValueString, Wallet: wl.ID}},
	}
	if t.State != "confirmed" {
		t.Confirmations = 0
	}
	s.transfers[wl.ID] = append([]bitgo.Transfer{t}, s.transfers[wl.ID]...)
	return t, nil
}

func (s *Server) SetTransferState(walletID, id, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.transfers[walletID] {
		if s.transfers[walletID][i].ID == id {
			s.transfers[walletID][i].State = state
			return true
		}
	}
	return false
}

// SendWebhook posts the shape BitGo uses for transfer webhooks.
func SendWebhook(notifyURL string, t bitgo.Transfer) error {
	body, _ := json.Marshal(map[string]any{
		"type": "transfer", "hash": t.TxID, "transfer": t.ID, "coin": t.Coin, "wallet": t.Wallet, "state": t.State,
	})
	resp, err := http.Post(notifyURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook receiver returned %d", resp.StatusCode)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

package bitgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Amounts are kept as the decimal strings BitGo returns (base units); they are
// parsed into math/big or NUMERIC, never float (plan §4).

type Wallet struct {
	ID                     string `json:"id"`
	Coin                   string `json:"coin"`
	Label                  string `json:"label"`
	Type                   string `json:"type"`
	MultisigType           string `json:"multisigType,omitempty"`
	BalanceString          string `json:"balanceString"`
	ConfirmedBalanceString string `json:"confirmedBalanceString"`
	SpendableBalanceString string `json:"spendableBalanceString"`
	Enterprise             string `json:"enterprise,omitempty"`
	// Admin carries the wallet policy ({"policy": {...}}); hashed to detect changes.
	Admin json.RawMessage `json:"admin,omitempty"`
}

type TransferEntry struct {
	Address     string `json:"address"`
	ValueString string `json:"valueString"`
	Wallet      string `json:"wallet,omitempty"`
}

type Transfer struct {
	ID            string          `json:"id"`
	Coin          string          `json:"coin"`
	Wallet        string          `json:"wallet"`
	TxID          string          `json:"txid"`
	Height        int64           `json:"height"`
	Date          time.Time       `json:"date"`
	Confirmations int64           `json:"confirmations"`
	Type          string          `json:"type"` // send | receive
	ValueString   string          `json:"valueString"`
	State         string          `json:"state"`
	Entries       []TransferEntry `json:"entries"`
}

type walletList struct {
	Wallets         []Wallet `json:"wallets"`
	NextBatchPrevID string   `json:"nextBatchPrevId"`
}

type TransferPage struct {
	Transfers       []Transfer `json:"transfers"`
	NextBatchPrevID string     `json:"nextBatchPrevId"`
}

// ListWallets returns every wallet visible to the token, following pagination.
func (c *Client) ListWallets(ctx context.Context) ([]Wallet, error) {
	var all []Wallet
	prev := ""
	for {
		q := url.Values{"limit": {"100"}}
		if prev != "" {
			q.Set("prevId", prev)
		}
		var page walletList
		if err := c.do(ctx, http.MethodGet, "/api/v2/wallets", q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Wallets...)
		if page.NextBatchPrevID == "" || len(page.Wallets) == 0 {
			return all, nil
		}
		prev = page.NextBatchPrevID
	}
}

func (c *Client) GetWallet(ctx context.Context, coin, walletID string) (*Wallet, error) {
	var w Wallet
	if err := c.do(ctx, http.MethodGet, "/api/v2/"+url.PathEscape(coin)+"/wallet/"+url.PathEscape(walletID), nil, nil, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

// GetTransfer fetches the authoritative state of a transfer. Webhooks are only
// hints (core principle 2); the worker always calls this before booking.
func (c *Client) GetTransfer(ctx context.Context, coin, walletID, transferID string) (*Transfer, error) {
	var t Transfer
	p := "/api/v2/" + url.PathEscape(coin) + "/wallet/" + url.PathEscape(walletID) + "/transfer/" + url.PathEscape(transferID)
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTransfers returns one page of transfers, newest first.
func (c *Client) ListTransfers(ctx context.Context, coin, walletID, prevID string, limit int) (*TransferPage, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if prevID != "" {
		q.Set("prevId", prevID)
	}
	var page TransferPage
	p := "/api/v2/" + url.PathEscape(coin) + "/wallet/" + url.PathEscape(walletID) + "/transfer"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

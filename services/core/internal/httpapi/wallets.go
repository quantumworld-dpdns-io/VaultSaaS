package httpapi

import (
	"context"
	"net/http"

	"github.com/vaultops/core/internal/bitgo"
)

// WalletSource is the read side of BitGo used by the wallet endpoints.
type WalletSource interface {
	ListWallets(ctx context.Context) ([]bitgo.Wallet, error)
}

type walletDTO struct {
	ID               string `json:"id"`
	Coin             string `json:"coin"`
	Label            string `json:"label"`
	Type             string `json:"type"`
	Balance          string `json:"balance"`
	ConfirmedBalance string `json:"confirmedBalance"`
	SpendableBalance string `json:"spendableBalance"`
}

func (s *Server) listWallets(w http.ResponseWriter, r *http.Request) {
	ws, err := s.wallets.ListWallets(r.Context())
	if err != nil {
		s.log.Error("list wallets", "err", err)
		writeError(w, http.StatusBadGateway, "bitgo_unavailable")
		return
	}
	out := make([]walletDTO, 0, len(ws))
	for _, wl := range ws {
		out = append(out, walletDTO{
			ID: wl.ID, Coin: wl.Coin, Label: wl.Label, Type: wl.Type,
			Balance: wl.BalanceString, ConfirmedBalance: wl.ConfirmedBalanceString, SpendableBalance: wl.SpendableBalanceString,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallets": out})
}

package activities

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.temporal.io/sdk/activity"

	"forge-crypto/temporal-worker/db"
	"forge-crypto/temporal-worker/workflows"
)

// Activities holds the configuration and clients the workflow's activities use.
// Registering a single struct instance lets the worker share an HTTP client and
// RPC endpoint across all activity invocations.
type Activities struct {
	PolicyURL             string
	MpcSignerURL          string
	EthereumRPC           string
	RequiredConfirmations int64
	httpClient            *http.Client
	db                    *sql.DB
	roundStore            *db.CeremonyRoundStore
}

// NewActivities builds an Activities with sane defaults.
func NewActivities(policyURL, mpcURL, ethRPC string, confirmations int64, database *sql.DB) *Activities {
	if confirmations <= 0 {
		confirmations = 3
	}

	var roundStore *db.CeremonyRoundStore
	if database != nil {
		roundStore = db.NewCeremonyRoundStore(database)
	}

	return &Activities{
		PolicyURL:             policyURL,
		MpcSignerURL:          mpcURL,
		EthereumRPC:           ethRPC,
		RequiredConfirmations: confirmations,
		httpClient:            &http.Client{Timeout: 15 * time.Second},
		db:                    database,
		roundStore:            roundStore,
	}
}

// postJSON is a small helper for the HTTP activities.
func (a *Activities) postJSON(ctx context.Context, url string, body, out interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CheckPolicy evaluates the transaction against the OPA policy-service.
func (a *Activities) CheckPolicy(ctx context.Context, req workflows.TransactionRequest) (*workflows.PolicyCheckResult, error) {
	input := map[string]interface{}{
		"customerId":   req.CustomerID,
		"customerTier": req.CustomerTier,
		"to":           req.To,
		"value":        req.Value,
		"chainId":      req.ChainID,
		"country":      req.Country,
	}
	var result workflows.PolicyCheckResult
	if err := a.postJSON(ctx, a.PolicyURL+"/evaluate", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SignTransaction asks the MPC signer to sign the transaction.
func (a *Activities) SignTransaction(ctx context.Context, req workflows.TransactionRequest) (*workflows.SignResult, error) {
	body := map[string]interface{}{
		"chainId":  req.ChainID,
		"to":       req.To,
		"data":     req.Data,
		"value":    req.Value,
		"gasLimit": req.GasLimit,
		"gasPrice": req.GasPrice,
		"nonce":    req.Nonce,
	}
	var result workflows.SignResult
	if err := a.postJSON(ctx, a.MpcSignerURL+"/sign", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BroadcastTransaction submits a raw signed transaction to the network.
func (a *Activities) BroadcastTransaction(ctx context.Context, signedTx string) (*workflows.BroadcastResult, error) {
	client, err := ethclient.DialContext(ctx, a.EthereumRPC)
	if err != nil {
		return nil, fmt.Errorf("dial RPC: %w", err)
	}
	defer client.Close()

	raw, err := hexutil.Decode(signedTx)
	if err != nil {
		return nil, fmt.Errorf("decode signed tx: %w", err)
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nil, fmt.Errorf("unmarshal signed tx: %w", err)
	}
	if err := client.SendTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("send tx: %w", err)
	}
	return &workflows.BroadcastResult{TxHash: tx.Hash().Hex()}, nil
}

// MonitorTransaction polls for the receipt until the configured number of
// confirmations is reached. It heartbeats so Temporal can detect a stalled
// activity, and respects the activity's context cancellation/timeout.
func (a *Activities) MonitorTransaction(ctx context.Context, txHash string) (*workflows.MonitorResult, error) {
	client, err := ethclient.DialContext(ctx, a.EthereumRPC)
	if err != nil {
		return nil, fmt.Errorf("dial RPC: %w", err)
	}
	defer client.Close()

	hash := common.HexToHash(txHash)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			activity.RecordHeartbeat(ctx, "polling for confirmations")

			receipt, err := client.TransactionReceipt(ctx, hash)
			if err != nil {
				// Not mined yet (or transient): keep polling.
				continue
			}
			head, err := client.BlockNumber(ctx)
			if err != nil {
				continue
			}
			confirmations := int64(head) - receipt.BlockNumber.Int64() + 1
			if confirmations >= a.RequiredConfirmations {
				return &workflows.MonitorResult{
					BlockNumber:   receipt.BlockNumber.Int64(),
					Confirmations: confirmations,
					Success:       receipt.Status == types.ReceiptStatusSuccessful,
				}, nil
			}
		}
	}
}

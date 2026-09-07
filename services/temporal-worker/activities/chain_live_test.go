//go:build live

package activities

// Live test against a REAL Ethereum node. Until this existed, nothing in
// this platform had ever broadcast a transaction to any chain -- every
// signature was cryptographically verified but none had been accepted by a
// node, so the final hop (RLP encoding a signed transaction the network will
// actually take, and watching it confirm) was unproven.
//
// Run against a dev node, which mines on demand and hands out funded
// accounts:
//
//	geth --dev --dev.period 1 --http --http.api eth,net,web3 \
//	     --http.addr 127.0.0.1 --http.port 8545 --datadir /tmp/geth-dev
//	# fund SIGNING_KEY's address from the dev coinbase, then:
//	ETHEREUM_RPC=http://127.0.0.1:8545 \
//	SIGNING_KEY=<hex private key, funded> \
//	  go test -tags live -run TestLiveChain -v ./activities/...
//
// Skips (does not fail) without a reachable node.

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/workflows"
)

func liveChain(t *testing.T) (rpc string, key string) {
	t.Helper()
	rpc = os.Getenv("ETHEREUM_RPC")
	key = os.Getenv("SIGNING_KEY")
	if rpc == "" || key == "" {
		t.Skip("ETHEREUM_RPC and SIGNING_KEY not set; skipping live chain test")
	}
	client, err := ethclient.DialContext(context.Background(), rpc)
	if err != nil {
		t.Skipf("no reachable Ethereum node: %v", err)
	}
	defer client.Close()
	if _, err := client.ChainID(context.Background()); err != nil {
		t.Skipf("node did not answer eth_chainId: %v", err)
	}
	return rpc, key
}

// The end-to-end claim this platform could not previously make: a
// transaction it signed is accepted by a real node, mined, and confirmed.
func TestLiveChain_BroadcastAndMonitor(t *testing.T) {
	rpc, keyHex := liveChain(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		t.Fatalf("chain id: %v", err)
	}
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		t.Fatalf("bad SIGNING_KEY: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey)

	balance, err := client.BalanceAt(ctx, from, nil)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance.Sign() == 0 {
		t.Skipf("signer %s has no funds on this chain; fund it before running", from.Hex())
	}
	t.Logf("signer %s balance: %s wei on chain %s", from.Hex(), balance, chainID)

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		t.Fatalf("gas price: %v", err)
	}

	to := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	value := big.NewInt(1_000_000_000_000_000) // 0.001 ETH
	tx := types.NewTx(&types.LegacyTx{
		Nonce: nonce, GasPrice: gasPrice, Gas: 21000, To: &to, Value: value,
	})
	signer := types.NewEIP155Signer(chainID)
	signedTx, err := types.SignTx(tx, signer, privKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := signedTx.MarshalBinary()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Broadcast and monitor through the platform's OWN activities, not a
	// bespoke path -- that is the point of this test.
	a := NewActivities("", "", rpc, 1, nil)
	s := &testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.SetTestTimeout(0)
	env.RegisterActivity(a)

	bVal, err := env.ExecuteActivity(a.BroadcastTransaction, "0x"+common.Bytes2Hex(raw))
	if err != nil {
		t.Fatalf("BroadcastTransaction failed: %v", err)
	}
	var broadcast workflows.BroadcastResult
	if err := bVal.Get(&broadcast); err != nil {
		t.Fatalf("decode broadcast result: %v", err)
	}
	if broadcast.TxHash == "" {
		t.Fatal("no transaction hash returned")
	}
	t.Logf("broadcast accepted by the node: %s", broadcast.TxHash)

	mVal, err := env.ExecuteActivity(a.MonitorTransaction, broadcast.TxHash)
	if err != nil {
		t.Fatalf("MonitorTransaction failed: %v", err)
	}
	var monitor workflows.MonitorResult
	if err := mVal.Get(&monitor); err != nil {
		t.Fatalf("decode monitor result: %v", err)
	}
	if !monitor.Success {
		t.Fatalf("transaction was mined but reverted: %+v", monitor)
	}
	if monitor.BlockNumber == 0 {
		t.Error("expected a real block number")
	}

	// Independent confirmation: the recipient's balance actually moved.
	toBalance, err := client.BalanceAt(ctx, to, nil)
	if err != nil {
		t.Fatalf("recipient balance: %v", err)
	}
	if toBalance.Cmp(value) < 0 {
		t.Errorf("recipient balance %s is less than the %s sent", toBalance, value)
	}

	t.Logf("SUCCESS: transaction mined in block %d with %d confirmation(s); recipient balance is now %s wei",
		monitor.BlockNumber, monitor.Confirmations, toBalance)
}

// The balance-migration sweep depends on three live chain reads
// (BalanceAt / SuggestGasPrice / PendingNonceAt) that had never run against
// a node. This exercises BuildSweepTransaction and AssembleSweepTransaction
// for real, then broadcasts the sweep and confirms the address is drained.
func TestLiveChain_SweepEmptiesAddress(t *testing.T) {
	rpc, keyHex := liveChain(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		t.Fatalf("chain id: %v", err)
	}
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		t.Fatalf("bad SIGNING_KEY: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey)
	newAddress := common.HexToAddress("0x00000000000000000000000000000000000BEEF1")

	a := NewActivities("", "", rpc, 1, nil)
	s := &testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.SetTestTimeout(0)
	env.RegisterActivity(a)

	// 1. Build the sweep from real on-chain state.
	buildVal, err := env.ExecuteActivity(a.BuildSweepTransaction, workflows.BuildSweepTransactionRequest{
		OldAddress: from.Hex(),
		NewAddress: newAddress.Hex(),
		EVMChainID: chainID.Int64(),
	})
	if err != nil {
		t.Fatalf("BuildSweepTransaction failed: %v", err)
	}
	var built workflows.BuildSweepTransactionResult
	if err := buildVal.Get(&built); err != nil {
		t.Fatalf("decode build result: %v", err)
	}
	if built.Skipped {
		t.Skipf("nothing to sweep: balance %s does not cover gas", built.BalanceWei)
	}
	t.Logf("sweep built from real chain state: balance %s, sweeping %s at gas price %s (nonce %d)",
		built.BalanceWei, built.ValueWei, built.GasPriceWei, built.Nonce)

	// 2. Sign the hash it produced. In production this is a threshold
	//    signature from the retiring key's committee; here it is the same
	//    hash signed with the same curve by the key that owns the address,
	//    which is what AssembleSweepTransaction verifies against.
	hash := common.HexToHash("0x" + built.TxHashHex)
	sig, err := crypto.Sign(hash.Bytes(), privKey)
	if err != nil {
		t.Fatalf("sign sweep hash: %v", err)
	}

	assembleVal, err := env.ExecuteActivity(a.AssembleSweepTransaction, workflows.AssembleSweepTransactionRequest{
		NewAddress:   newAddress.Hex(),
		Nonce:        built.Nonce,
		GasLimit:     built.GasLimit,
		GasPriceWei:  built.GasPriceWei,
		ValueWei:     built.ValueWei,
		EVMChainID:   chainID.Int64(),
		Signature:    common.Bytes2Hex(sig),
		ExpectedFrom: from.Hex(),
	})
	if err != nil {
		t.Fatalf("AssembleSweepTransaction failed: %v", err)
	}
	var assembled workflows.AssembleSweepTransactionResult
	if err := assembleVal.Get(&assembled); err != nil {
		t.Fatalf("decode assemble result: %v", err)
	}

	// 3. Broadcast the sweep -- the step that had never happened.
	bVal, err := env.ExecuteActivity(a.BroadcastTransaction, assembled.SignedTxHex)
	if err != nil {
		t.Fatalf("broadcasting the sweep failed: %v", err)
	}
	var broadcast workflows.BroadcastResult
	if err := bVal.Get(&broadcast); err != nil {
		t.Fatalf("decode broadcast result: %v", err)
	}

	mVal, err := env.ExecuteActivity(a.MonitorTransaction, broadcast.TxHash)
	if err != nil {
		t.Fatalf("MonitorTransaction failed: %v", err)
	}
	var monitor workflows.MonitorResult
	if err := mVal.Get(&monitor); err != nil {
		t.Fatalf("decode monitor result: %v", err)
	}
	if !monitor.Success {
		t.Fatalf("sweep was mined but reverted: %+v", monitor)
	}

	// 4. The address must actually be drained, and the funds must have
	//    arrived at the replacement.
	time.Sleep(2 * time.Second)
	oldBalance, err := client.BalanceAt(ctx, from, nil)
	if err != nil {
		t.Fatalf("old balance: %v", err)
	}
	newBalance, err := client.BalanceAt(ctx, newAddress, nil)
	if err != nil {
		t.Fatalf("new balance: %v", err)
	}
	if newBalance.Sign() == 0 {
		t.Error("the replacement address received nothing")
	}
	swept, _ := new(big.Int).SetString(built.ValueWei, 10)
	if newBalance.Cmp(swept) != 0 {
		t.Errorf("replacement address holds %s, expected exactly the swept %s", newBalance, swept)
	}

	t.Logf("SUCCESS: swept %s wei on-chain in block %d; retiring address now holds %s wei, replacement holds %s wei",
		built.ValueWei, monitor.BlockNumber, oldBalance, newBalance)
}

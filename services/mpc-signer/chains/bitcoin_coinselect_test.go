package chains

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
)

func utxo(txid string, vout int, amount int64, script string) UTXO {
	return UTXO{Txid: txid, Vout: vout, Amount: amount, Script: script, Confirmations: 6}
}

// Distinct txids, since a wallet's outputs are distinguished by them and the
// tie-break ordering depends on them.
func txidN(n byte) string {
	b := make([]byte, 32)
	b[31] = n
	return hex.EncodeToString(b)
}

func TestSelectsASingleOutputWhenOneCoversTheSpend(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	sel, err := SelectCoins(&CoinSelectionRequest{
		Network: "regtest",
		UTXOs: []UTXO{
			utxo(txidN(1), 0, 20_000, script),
			utxo(txidN(2), 0, 500_000, script),
			utxo(txidN(3), 0, 30_000, script),
		},
		Destination:   dest.EncodeAddress(),
		Amount:        100_000,
		FeeRate:       10,
		ChangeAddress: change.EncodeAddress(),
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(sel.Selected) != 1 {
		t.Fatalf("selected %d outputs, want 1 (largest-first should reach for the 500k)", len(sel.Selected))
	}
	if sel.Selected[0].Amount != 500_000 {
		t.Errorf("selected the %d sat output, want the largest (500000)", sel.Selected[0].Amount)
	}
	if sel.Fee != sel.VirtualSize*10 {
		t.Errorf("fee %d does not match %d vbytes at 10 sat/vB", sel.Fee, sel.VirtualSize)
	}
	if sel.TotalIn != sel.Amount+sel.Fee+sel.Change {
		t.Errorf("the numbers do not balance: in %d != amount %d + fee %d + change %d",
			sel.TotalIn, sel.Amount, sel.Fee, sel.Change)
	}
}

func TestAccumulatesUntilTheSpendIsCovered(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	sel, err := SelectCoins(&CoinSelectionRequest{
		Network: "regtest",
		UTXOs: []UTXO{
			utxo(txidN(1), 0, 40_000, script),
			utxo(txidN(2), 0, 40_000, script),
			utxo(txidN(3), 0, 40_000, script),
		},
		Destination:   dest.EncodeAddress(),
		Amount:        100_000,
		FeeRate:       5,
		ChangeAddress: change.EncodeAddress(),
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(sel.Selected) != 3 {
		t.Fatalf("selected %d outputs, want 3", len(sel.Selected))
	}
	if sel.TotalIn != 120_000 {
		t.Errorf("total in %d, want 120000", sel.TotalIn)
	}
}

// The estimate has to be an upper bound on the real thing. If it is not, the
// transaction pays less than the requested fee rate and confirms late --
// which is indistinguishable from network congestion and therefore very hard
// to notice.
//
// This is the test that makes the constants in this file trustworthy: it
// signs the selection for real and measures the transaction Bitcoin would
// actually charge for.
func TestTheFeeEstimateIsAnUpperBoundOnTheSignedTransaction(t *testing.T) {
	key := mustKey(t)
	legacyDest, _ := p2pkhFor(t, mustKey(t))
	segwitDest, _ := p2wpkhFor(t, mustKey(t))
	_, legacyScript := p2pkhFor(t, key)
	_, segwitScript := p2wpkhFor(t, key)
	changeAddr, _ := p2wpkhFor(t, key)

	cases := []struct {
		name    string
		utxos   []UTXO
		dest    btcutil.Address
		amount  int64
		feeRate int64
	}{
		{"one legacy input", []UTXO{utxo(txidN(1), 0, 500_000, legacyScript)}, legacyDest, 100_000, 10},
		{"one segwit input", []UTXO{utxo(txidN(1), 0, 500_000, segwitScript)}, segwitDest, 100_000, 10},
		{"three segwit inputs", []UTXO{
			utxo(txidN(1), 0, 40_000, segwitScript),
			utxo(txidN(2), 0, 40_000, segwitScript),
			utxo(txidN(3), 0, 40_000, segwitScript),
		}, segwitDest, 100_000, 3},
		{"mixed input types", []UTXO{
			utxo(txidN(1), 0, 60_000, legacyScript),
			utxo(txidN(2), 0, 60_000, segwitScript),
		}, legacyDest, 100_000, 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := SelectCoins(&CoinSelectionRequest{
				Network:       "regtest",
				UTXOs:         tc.utxos,
				Destination:   tc.dest.EncodeAddress(),
				Amount:        tc.amount,
				FeeRate:       tc.feeRate,
				ChangeAddress: changeAddr.EncodeAddress(),
			})
			if err != nil {
				t.Fatalf("select: %v", err)
			}

			plan, err := PlanBitcoinTransaction(sel.Request)
			if err != nil {
				t.Fatalf("the selection did not produce a plannable transaction: %v", err)
			}
			sigs := make([]BitcoinInputSignature, 0, len(plan.SigHashes))
			for _, digest := range plan.SigHashes {
				sigs = append(sigs, signLikeCommittee(t, key, digest))
			}
			raw, err := AssembleBitcoinTransaction(plan, sigs,
				hex.EncodeToString(key.PubKey().SerializeCompressed()))
			if err != nil {
				t.Fatalf("the selection did not produce a spendable transaction: %v", err)
			}

			tx := wire.NewMsgTx(wire.TxVersion)
			if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
				t.Fatalf("deserialize: %v", err)
			}
			// btcd's own weight calculation, so this is Bitcoin's arithmetic
			// rather than a second copy of ours.
			actual := blockchain.GetTransactionWeight(btcutil.NewTx(tx))
			actualVSize := (actual + 3) / 4

			if sel.VirtualSize < actualVSize {
				t.Errorf("estimated %d vbytes but the signed transaction is %d; the fee would be short",
					sel.VirtualSize, actualVSize)
			}
			// An estimate far above reality is its own bug: the customer
			// overpays every time. A DER signature is one or two bytes
			// shorter about half the time, per input.
			if slack := sel.VirtualSize - actualVSize; slack > int64(len(sel.Selected))*2+1 {
				t.Errorf("estimated %d vbytes for a %d vbyte transaction; %d vbytes of slack is too much",
					sel.VirtualSize, actualVSize, slack)
			}
		})
	}
}

// Change worth less than it costs to spend should not be created. The
// remainder goes to the miner, and the response has to say so -- this is the
// one case where the fee paid is more than the fee rate implies.
func TestDustChangeIsDroppedIntoTheFee(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	// Sized so that after the fee there is a positive remainder below the
	// 294-sat P2WPKH dust threshold.
	const feeRate = 1
	probe, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{utxo(txidN(1), 0, 1_000_000, script)},
		Destination:   dest.EncodeAddress(),
		Amount:        500_000,
		FeeRate:       feeRate,
		ChangeAddress: change.EncodeAddress(),
	})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	// Ask for everything except the fee and 100 sats.
	amount := 1_000_000 - probe.VirtualSize*feeRate - 100

	sel, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{utxo(txidN(1), 0, 1_000_000, script)},
		Destination:   dest.EncodeAddress(),
		Amount:        amount,
		FeeRate:       feeRate,
		ChangeAddress: change.EncodeAddress(),
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if sel.Change != 0 {
		t.Fatalf("created a %d sat change output, below the dust threshold", sel.Change)
	}
	if !sel.ChangeDroppedToFee {
		t.Error("dropped the change to fee without reporting it")
	}
	if len(sel.Request.Outputs) != 1 {
		t.Errorf("the transaction has %d outputs, want 1", len(sel.Request.Outputs))
	}
	if sel.TotalIn != sel.Amount+sel.Fee {
		t.Errorf("the numbers do not balance: in %d != amount %d + fee %d", sel.TotalIn, sel.Amount, sel.Fee)
	}
}

// "Insufficient funds" without saying the fee is part of the shortfall sends
// people looking for a bug in their balance arithmetic.
func TestInsufficientFundsExplainsTheFee(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	_, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{utxo(txidN(1), 0, 100_000, script)},
		Destination:   dest.EncodeAddress(),
		Amount:        100_000, // exactly the balance: the fee is what is missing
		FeeRate:       10,
		ChangeAddress: change.EncodeAddress(),
	})
	if err == nil {
		t.Fatal("selected a spend that cannot pay its own fee")
	}
	if !strings.Contains(err.Error(), "fee") {
		t.Errorf("the error does not mention the fee: %v", err)
	}
}

func TestUnconfirmedOutputsAreNotSpent(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	unconfirmed := UTXO{Txid: txidN(1), Vout: 0, Amount: 500_000, Script: script, Confirmations: 0}
	_, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{unconfirmed},
		Destination:   dest.EncodeAddress(),
		Amount:        100_000,
		FeeRate:       10,
		ChangeAddress: change.EncodeAddress(),
	})
	if err == nil {
		t.Fatal("spent an unconfirmed output")
	}
	// The distinction matters operationally: "you have no coins" and "your
	// coins are one block away" need different responses.
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("the error does not say the outputs are unconfirmed: %v", err)
	}
}

// An output type the platform cannot threshold-sign should not make the rest
// of the wallet unspendable.
func TestUnspendableOutputTypesAreSkippedNotFatal(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	// OP_RETURN: valid to hold in a wallet's view, impossible to spend.
	opReturn := utxo(txidN(9), 0, 900_000, "6a0548656c6c6f")

	sel, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{opReturn, utxo(txidN(1), 0, 500_000, script)},
		Destination:   dest.EncodeAddress(),
		Amount:        100_000,
		FeeRate:       10,
		ChangeAddress: change.EncodeAddress(),
	})
	if err != nil {
		t.Fatalf("an unspendable output made the whole wallet unspendable: %v", err)
	}
	if len(sel.Selected) != 1 || sel.Selected[0].Amount != 500_000 {
		t.Errorf("selected %+v, want only the spendable 500000-sat output", sel.Selected)
	}
}

// Segwit's whole economic argument: the same spend costs less. Worth a test
// because if the witness bytes were charged at full weight the discount
// would silently vanish and nobody would notice a fee that is merely higher.
func TestSegwitInputsAreCheaperThanLegacyOnes(t *testing.T) {
	key := mustKey(t)
	_, legacy := p2pkhFor(t, key)
	_, segwit := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	ask := func(script string) *CoinSelection {
		sel, err := SelectCoins(&CoinSelectionRequest{
			Network:       "regtest",
			UTXOs:         []UTXO{utxo(txidN(1), 0, 500_000, script)},
			Destination:   dest.EncodeAddress(),
			Amount:        100_000,
			FeeRate:       10,
			ChangeAddress: change.EncodeAddress(),
		})
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		return sel
	}

	if ask(segwit).Fee >= ask(legacy).Fee {
		t.Errorf("a segwit spend costs %d sats and a legacy one %d; the witness discount is not being applied",
			ask(segwit).Fee, ask(legacy).Fee)
	}
}

// The same wallet state must produce the same transaction. Without a
// deterministic tie-break, an idempotent retry could select a different set
// of coins and broadcast a second, different transaction spending the same
// money -- one of which would confirm.
func TestSelectionIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	// Equal amounts, so only the tie-break decides.
	a := utxo(txidN(1), 0, 60_000, script)
	b := utxo(txidN(2), 0, 60_000, script)
	c := utxo(txidN(3), 0, 60_000, script)

	ask := func(us ...UTXO) string {
		sel, err := SelectCoins(&CoinSelectionRequest{
			Network:       "regtest",
			UTXOs:         us,
			Destination:   dest.EncodeAddress(),
			Amount:        100_000,
			FeeRate:       5,
			ChangeAddress: change.EncodeAddress(),
		})
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		plan, err := PlanBitcoinTransaction(sel.Request)
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		return plan.UnsignedTxHex
	}

	first := ask(a, b, c)
	for _, order := range [][]UTXO{{c, b, a}, {b, a, c}, {c, a, b}} {
		if got := ask(order...); got != first {
			t.Fatal("the same wallet state produced two different transactions depending on input order")
		}
	}
}

func TestSelectionRejectsAnAddressFromAnotherNetwork(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	change, _ := p2wpkhFor(t, key)

	_, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{utxo(txidN(1), 0, 500_000, script)},
		Destination:   "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", // mainnet
		Amount:        100_000,
		FeeRate:       10,
		ChangeAddress: change.EncodeAddress(),
	})
	if err == nil {
		t.Fatal("accepted a mainnet destination on regtest")
	}
}

// A dust output is refused before a single coin is chosen.
//
// Found by the API drill, which asked to send 1 satoshi. The platform
// planned it, ran a threshold ceremony for every input, assembled a
// perfectly valid transaction, and only then had Bitcoin Core refuse it as
// dust -- an expensive way to discover a mistake that costs nothing to
// catch, and one the caller saw as a 503.
func TestASpendBelowDustIsRefusedBeforeAnyWork(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	segwitDest, _ := p2wpkhFor(t, mustKey(t))
	legacyDest, _ := p2pkhFor(t, mustKey(t))
	change, _ := p2wpkhFor(t, key)

	// The threshold depends on the destination's type: a legacy output
	// costs more to spend later, so more is required to make it worth
	// creating.
	cases := []struct {
		name   string
		dest   btcutil.Address
		amount int64
	}{
		{"one satoshi", segwitDest, 1},
		{"just below the segwit threshold", segwitDest, p2wpkhDust - 1},
		{"below the legacy threshold", legacyDest, p2pkhDust - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SelectCoins(&CoinSelectionRequest{
				Network:       "regtest",
				UTXOs:         []UTXO{utxo(txidN(1), 0, 500_000, script)},
				Destination:   tc.dest.EncodeAddress(),
				Amount:        tc.amount,
				FeeRate:       5,
				ChangeAddress: change.EncodeAddress(),
			})
			if err == nil {
				t.Fatalf("selected a spend of %d sats, which the network will not relay", tc.amount)
			}
			if !strings.Contains(err.Error(), "dust") {
				t.Errorf("the error does not say the amount is dust: %v", err)
			}
		})
	}

	// And the threshold itself is spendable, so the check is not off by one.
	if _, err := SelectCoins(&CoinSelectionRequest{
		Network:       "regtest",
		UTXOs:         []UTXO{utxo(txidN(1), 0, 500_000, script)},
		Destination:   segwitDest.EncodeAddress(),
		Amount:        p2wpkhDust,
		FeeRate:       5,
		ChangeAddress: change.EncodeAddress(),
	}); err != nil {
		t.Errorf("refused a spend of exactly the dust threshold: %v", err)
	}
}

// The same guard one level down, for a caller that builds its own inputs
// and outputs rather than going through selection.
func TestPlanRefusesADustOutput(t *testing.T) {
	key := mustKey(t)
	_, script := p2wpkhFor(t, key)
	dest, _ := p2wpkhFor(t, mustKey(t))

	_, err := PlanBitcoinTransaction(&BitcoinSigningRequest{
		Network: "regtest",
		Inputs: []BitcoinInput{{
			Txid:   txidN(1),
			Vout:   0,
			Amount: 100_000,
			Script: script,
		}},
		Outputs: []BitcoinOutput{{Address: dest.EncodeAddress(), Amount: 100}},
	})
	if err == nil {
		t.Fatal("planned a transaction with a dust output")
	}
	if !strings.Contains(err.Error(), "dust") {
		t.Errorf("the error does not say the output is dust: %v", err)
	}
}

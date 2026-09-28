# Bitcoin Integration Guide

OpenFireblocks provides institutional-grade key management and signing for Bitcoin transactions.

## Overview

- **Blockchain**: Bitcoin (BTC)
- **Curve**: secp256k1
- **Addresses**: P2PKH, P2SH, P2WPKH, P2WSH
- **Latency**: <100ms p95 for signing
- **Throughput**: 100+ signatures/second

## Setup

### 1. Create Bitcoin Key Pair

```javascript
const client = new OpenFireblocksClient(apiKey);

const ceremony = await client.createKey({
  blockchain: 'bitcoin',
  threshold: 4,  // 4-of-7 threshold signing
  total_parties: 7,
  name: 'Bitcoin Treasury Key'
});

console.log(`Key Pair: ${ceremony.key_pair_id}`);
console.log(`Address: ${ceremony.address}`);
```

### 2. Generate Bitcoin Address

The address is automatically derived from the public key:

```javascript
const key = await client.getKey(keyPairId);
const address = key.address;  // Bitcoin address (P2PKH format)
const publicKey = key.public_key;  // Hex-encoded public key
```

## Signing Bitcoin Transactions

### Transaction Structure

```javascript
const txData = {
  version: 2,
  inputs: [
    {
      txid: 'previous_transaction_id',
      vout: 0,
      scriptSig: '',  // Will be populated by signing
      sequence: 0xffffffff
    }
  ],
  outputs: [
    {
      value: 100000000,  // Satoshis (1 BTC = 100,000,000 sat)
      scriptPubKey: 'script_hex'
    }
  ],
  lockTime: 0
};

const txHex = serializeTransaction(txData);  // Create tx hex
```

### Sign Transaction

```javascript
const signing = await client.sign({
  key_pair_id: keyPairId,
  transaction: txHex,
  idempotency_key: `order_${orderId}`  // Prevent duplicates
});

// Wait for signature
const result = await client.waitForSignature(signing.id, {
  maxWaitTime: 60000  // 60 seconds
});

const signedTxHex = result.signed_transaction;
```

### UTXO Management

```javascript
// 1. Get unspent outputs (UTXO) from Bitcoin network
const utxos = await bitcoinNode.getUTXOs(address);

// 2. Select UTXOs for transaction
const selectedUTXOs = selectUTXOs(utxos, amountNeeded);

// 3. Create transaction inputs
const inputs = selectedUTXOs.map(utxo => ({
  txid: utxo.txid,
  vout: utxo.vout,
  scriptSig: ''
}));

// 4. Create transaction outputs
const outputs = [
  {
    value: amountToSend,
    scriptPubKey: encodeAddress(recipientAddress)
  },
  {
    value: change,
    scriptPubKey: encodeAddress(ourAddress)  // Change output
  }
];

// 5. Sign and broadcast
const txHex = serializeTransaction({ inputs, outputs });
const signed = await client.sign({ key_pair_id: keyPairId, transaction: txHex });
const txid = await bitcoinNode.broadcastTransaction(signed.signed_transaction);
```

## Best Practices

### Fee Management

```javascript
// Get current network fee rate (satoshis per byte)
const feeRate = await bitcoinNode.getFeeRate();  // e.g., 5 sat/byte

// Calculate transaction fee
const txSize = estimateTransactionSize(inputs.length, outputs.length);
const fee = txSize * feeRate;  // 250 bytes * 5 sat/byte = 1250 satoshis

// Adjust outputs to include fee
outputs[outputs.length - 1].value -= fee;
```

### Change Address

```javascript
// Always use a change address to prevent address reuse
// This protects privacy and prevents funds from being mixed

// Option 1: Use same address (simpler)
// Option 2: Derive new address from same key pair (more private)
// Option 3: Use segregated address (best practice)

const changeAddress = await deriveChangeAddress(keyPairId);
```

### Transaction Confirmation

```javascript
async function waitForConfirmation(txid, confirmations = 1) {
  let confirmed = 0;
  while (confirmed < confirmations) {
    const tx = await bitcoinNode.getTransaction(txid);
    confirmed = tx.confirmations || 0;
    if (confirmed < confirmations) {
      await sleep(10000);  // Wait 10 seconds
    }
  }
  return confirmed;
}
```

## Error Handling

```javascript
try {
  const signing = await client.sign({
    key_pair_id: keyPairId,
    transaction: txHex
  });
  
  const result = await client.waitForSignature(signing.id);
} catch (error) {
  if (error.code === 'SIGNING_TIMEOUT') {
    // Retry signing operation
    console.error('Signing timed out, retrying...');
  } else if (error.code === 'INVALID_TRANSACTION') {
    // Transaction format issue
    console.error('Invalid Bitcoin transaction format');
  } else {
    throw error;
  }
}
```

## Advanced Scenarios

### Multi-Sig Spending

```javascript
// Spend from 2-of-3 multisig address
const redeemScript = createRedeemScript([pk1, pk2, pk3], 2);

const scriptSig = createScriptSig([signature1, signature2], redeemScript);

const input = {
  txid: utxo.txid,
  vout: utxo.vout,
  scriptSig: scriptSig
};
```

### Segregated Witness (SegWit)

```javascript
// P2WPKH (Pay to Witness Pubkey Hash)
const witnessProgram = hash160(publicKey);
const scriptPubKey = createWitnessScript(witnessProgram);

// Signing includes witness field
const witnessData = createWitness([signature, publicKey]);
const signedTx = { ...unsignedTx, witness: witnessData };
```

## Testing

```javascript
// Use Bitcoin testnet for development
const ceremony = await client.createKey({
  blockchain: 'bitcoin-testnet',
  threshold: 2,
  total_parties: 3
});

// Get testnet faucet coins
const testAddress = await client.getKey(ceremony.key_pair_id);
await bitcoinTestnetFaucet.sendCoins(testAddress, 1.0);  // 1 test BTC
```

## Compliance & Auditing

```javascript
// All Bitcoin transactions are logged and auditable
const signing = await client.sign({
  key_pair_id: keyPairId,
  transaction: txHex,
  custom_fields: {
    merchant_id: '12345',
    order_id: 'order_abc',
    amount_usd: '50000',
    counterparty: 'exchange.example.com'
  }
});

// Retrieve audit trail
const audit = await client.getAuditLog(signing.id);
```

## Support & References

- **Bitcoin Core**: https://bitcoin.org/en/developer-documentation
- **Bitcoin Developer Reference**: https://developer.bitcoin.org/
- **BIP-32 (Hierarchical Deterministic Wallets)**: https://github.com/bitcoin/bips/blob/master/bip-0032.mediawiki
- **BIP-39 (Mnemonic Codes)**: https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki
- **Support**: support@openfireblocks.io

/**
 * OpenFireblocks JavaScript SDK Examples
 */

const OpenFireblocksClient = require('./client');

const client = new OpenFireblocksClient(process.env.OPENFIREBLOCKS_API_KEY);

// Example 1: Create a new key pair for Bitcoin
async function example1_createBitcoinKey() {
  console.log('Creating Bitcoin key pair...');
  
  const ceremony = await client.createKey({
    blockchain: 'bitcoin',
    threshold: 4,
    total_parties: 7,
    name: 'Bitcoin Treasury Key',
  });
  
  console.log(`DKG Ceremony initiated: ${ceremony.id}`);
  console.log(`Status: ${ceremony.status}`);
  console.log(`Key Pair ID: ${ceremony.key_pair_id}`);
}

// Example 2: Sign a Bitcoin transaction
async function example2_signBitcoinTx() {
  console.log('Signing Bitcoin transaction...');
  
  const keyId = 'key_uuid_here';
  const txHex = '02000000...'; // Bitcoin transaction
  
  const signing = await client.sign({
    key_pair_id: keyId,
    transaction: txHex,
  });
  
  console.log(`Signing initiated: ${signing.id}`);
  
  // Wait for signature
  const completed = await client.waitForSignature(signing.id);
  console.log(`Signed transaction: ${completed.signed_transaction}`);
  console.log(`Latency: ${completed.latency_ms}ms`);
}

// Example 3: Sign an Ethereum transaction
async function example3_signEthereumTx() {
  console.log('Signing Ethereum transaction...');
  
  const keyId = 'key_uuid_here';
  const txData = {
    to: '0x...',
    value: '1000000000000000000', // 1 ETH
    data: '0x...',
    gasLimit: 21000,
    gasPrice: '20000000000',
    nonce: 0,
  };
  
  const signing = await client.sign({
    key_pair_id: keyId,
    transaction: JSON.stringify(txData),
  });
  
  const result = await client.waitForSignature(signing.id, { maxWaitTime: 120000 });
  console.log(`Signed transaction: ${result.signed_transaction}`);
}

// Example 4: List all key pairs
async function example4_listKeys() {
  console.log('Listing all key pairs...');
  
  const response = await client.listKeys({
    limit: 100,
    blockchain: 'ethereum',
  });
  
  console.log(`Found ${response.total} key pairs`);
  response.data.forEach(key => {
    console.log(`- ${key.name} (${key.address})`);
  });
}

// Example 5: Register customer and submit KYC
async function example5_customerKYC() {
  console.log('Registering customer...');
  
  const customer = await client.registerCustomer({
    name: 'Acme Corporation',
    email: 'legal@acme.io',
    country: 'US',
  });
  
  console.log(`Customer registered: ${customer.id}`);
  console.log(`KYC Status: ${customer.kyc_status}`);
  
  // Submit KYC document
  const kyc = await client.submitKYC(
    customer.id,
    '/path/to/passport.pdf',
    'passport'
  );
  
  console.log(`KYC submitted: ${kyc.verification_id}`);
}

// Example 6: Idempotent signing (retry-safe)
async function example6_idempotentSigning() {
  const idempotencyKey = 'merchant_order_12345';
  
  try {
    const signing = await client.sign({
      key_pair_id: 'key_uuid',
      transaction: 'tx_hex',
      idempotency_key: idempotencyKey,
    });
    
    const result = await client.waitForSignature(signing.id);
    console.log(`Signed with idempotency key: ${idempotencyKey}`);
  } catch (error) {
    // Retry with same idempotency key - will return same result
    console.error(`First attempt failed: ${error.message}`);
  }
}

// Example 7: Health check
async function example7_healthCheck() {
  const health = await client.health();
  console.log(`Service status: ${health.status}`);
  console.log(`Uptime: ${health.uptime_seconds}s`);
}

// Run examples
(async () => {
  try {
    await example1_createBitcoinKey();
    // await example2_signBitcoinTx();
    // await example3_signEthereumTx();
    // await example4_listKeys();
    // await example5_customerKYC();
    // await example6_idempotentSigning();
    // await example7_healthCheck();
  } catch (error) {
    console.error(`Error: ${error.message}`);
    process.exit(1);
  }
})();

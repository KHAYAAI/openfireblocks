"""Examples of using the OpenFireblocks Python SDK."""

from openfireblocks import Client, CreateKeyPairRequest, SigningRequest


def example_create_bitcoin_key():
    """Example: Create a Bitcoin threshold key."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    # Create a 4-of-7 threshold ECDSA key for Bitcoin
    key_pair = client.create_key_pair(
        CreateKeyPairRequest(
            name="bitcoin-cold-wallet",
            blockchain="bitcoin",
            threshold=4,
            total_parties=7,
        )
    )

    print(f"Created key pair: {key_pair.id}")
    print(f"Address: {key_pair.address}")
    print(f"Status: {key_pair.status}")
    print(f"Public Key: {key_pair.public_key}")

    return key_pair


def example_list_keys():
    """Example: List all key pairs."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    keys = client.list_key_pairs()

    print(f"Found {len(keys)} key pairs:")
    for key in keys:
        print(f"  - {key.id} ({key.blockchain}): {key.status}")


def example_sign_bitcoin_transaction():
    """Example: Sign a Bitcoin transaction."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    # Start signing
    sig_request = client.sign(
        SigningRequest(
            key_pair_id="key-pair-id",
            transaction="020000000117cc4c39f2a4b11d3d9e7f8e4c3b2a1f...",  # Bitcoin tx hex
        )
    )

    print(f"Signing request ID: {sig_request.id}")
    print(f"Status: {sig_request.status}")

    # Wait for completion
    try:
        completed = client.wait_for_signing(
            sig_request.id,
            max_wait=300,  # Wait up to 5 minutes
            poll_interval=2.0,  # Poll every 2 seconds
        )

        if completed.status == "completed":
            print(f"Signed successfully!")
            print(f"Signature: {completed.signature}")
            print(f"Signed Transaction: {completed.signed_transaction}")
            print(f"Latency: {completed.latency_ms}ms")
        else:
            print(f"Signing failed: {completed.error}")

    except TimeoutError as e:
        print(f"Timeout: {e}")


def example_sign_ethereum_transaction():
    """Example: Sign an Ethereum transaction."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    # Create Ethereum key
    eth_key = client.create_key_pair(
        CreateKeyPairRequest(
            name="ethereum-hot-wallet",
            blockchain="ethereum",
            threshold=3,
            total_parties=5,
        )
    )

    print(f"Created Ethereum key: {eth_key.address}")

    # Sign transaction
    sig_request = client.sign(
        SigningRequest(
            key_pair_id=eth_key.id,
            transaction="02f86a0102848c51d60085...",  # Ethereum tx hex
        )
    )

    print(f"Signing request: {sig_request.id} - {sig_request.status}")

    # Check status
    status = client.get_signing_status(sig_request.id)
    print(f"Current status: {status.status}")


def example_health_check():
    """Example: Check API health."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    health = client.health()
    print(f"API Status: {health.status}")
    print(f"Version: {health.version}")
    print(f"Timestamp: {health.timestamp}")


def example_multi_chain_management():
    """Example: Manage keys across multiple blockchains."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    blockchains = ["bitcoin", "ethereum", "solana", "cosmos"]
    keys = {}

    # Create keys for each blockchain
    for blockchain in blockchains:
        key = client.create_key_pair(
            CreateKeyPairRequest(
                name=f"{blockchain}-threshold-key",
                blockchain=blockchain,
                threshold=4,
                total_parties=7,
            )
        )
        keys[blockchain] = key
        print(f"Created {blockchain} key: {key.address}")

    # List all keys
    all_keys = client.list_key_pairs()
    print(f"\nTotal keys: {len(all_keys)}")


def example_idempotent_signing():
    """Example: Idempotent signing with automatic retry."""
    client = Client(
        base_url="https://api.openfireblocks.io",
        api_key="your-api-key",
    )

    # Create a signing request with explicit idempotency key
    idempotency_key = "my-unique-request-id-12345"

    request = SigningRequest(
        key_pair_id="key-pair-id",
        transaction="02f86a0102848c51d60085...",
        idempotency_key=idempotency_key,
    )

    # Submit the request
    sig_response = client.sign(request)
    print(f"Submitted: {sig_response.id}")

    # If the request fails and we retry, the API returns the same result
    # (idempotency ensures no double-signing)
    sig_response_retry = client.sign(request)
    assert sig_response.id == sig_response_retry.id
    print(f"Retry returned same request: {sig_response_retry.id}")


if __name__ == "__main__":
    print("Example 1: Create Bitcoin Key")
    print("-" * 50)
    # example_create_bitcoin_key()

    print("\n\nExample 2: List Keys")
    print("-" * 50)
    # example_list_keys()

    print("\n\nExample 3: Sign Bitcoin Transaction")
    print("-" * 50)
    # example_sign_bitcoin_transaction()

    print("\n\nExample 4: Health Check")
    print("-" * 50)
    # example_health_check()

    print("\nSet API key and base URL before running examples")

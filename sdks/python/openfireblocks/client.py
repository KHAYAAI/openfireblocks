"""OpenFireblocks API client."""

import json
import time
from typing import List, Optional
from datetime import datetime
from urllib.request import Request, urlopen
from urllib.error import HTTPError
import uuid

from .types import (
    KeyPair,
    CreateKeyPairRequest,
    SigningRequest,
    SigningResponse,
    HealthResponse,
)


class Client:
    """OpenFireblocks API client for threshold signing and key management."""

    def __init__(self, base_url: str, api_key: str, timeout: int = 30):
        """Initialize the OpenFireblocks client.

        Args:
            base_url: Base URL for the API (e.g., https://api.openfireblocks.io)
            api_key: API key for authentication
            timeout: Request timeout in seconds
        """
        self.base_url = base_url.rstrip('/')
        self.api_key = api_key
        self.timeout = timeout

    def create_key_pair(self, req: CreateKeyPairRequest) -> KeyPair:
        """Create a new threshold key pair via DKG ceremony.

        Args:
            req: CreateKeyPairRequest with key pair parameters

        Returns:
            KeyPair with assigned ID and pending status

        Raises:
            ValueError: If parameters are invalid
            HTTPError: If API returns an error
        """
        req_data = {
            'name': req.name,
            'blockchain': req.blockchain,
            'threshold': req.threshold,
            'total_parties': req.total_parties,
        }

        resp = self._request('POST', '/keys', req_data)
        return self._parse_key_pair(resp)

    def get_key_pair(self, key_id: str) -> KeyPair:
        """Retrieve a key pair by ID.

        Args:
            key_id: ID of the key pair

        Returns:
            KeyPair details
        """
        resp = self._request('GET', f'/keys/{key_id}')
        return self._parse_key_pair(resp)

    def list_key_pairs(self) -> List[KeyPair]:
        """List all key pairs for the customer.

        Returns:
            List of KeyPair objects
        """
        resp = self._request('GET', '/keys')
        if isinstance(resp, list):
            return [self._parse_key_pair(kp) for kp in resp]
        return [self._parse_key_pair(resp)]

    def sign(self, req: SigningRequest) -> SigningResponse:
        """Submit a transaction for threshold signing.

        Automatically generates idempotency key if not provided for safe replay.

        Args:
            req: SigningRequest with key pair ID and transaction

        Returns:
            SigningResponse with status and signing request ID
        """
        if not req.idempotency_key:
            req.idempotency_key = str(uuid.uuid4())

        req_data = {
            'key_pair_id': req.key_pair_id,
            'transaction': req.transaction,
            'idempotency_key': req.idempotency_key,
        }

        resp = self._request('POST', '/sign', req_data)
        return self._parse_signing_response(resp)

    def get_signing_status(self, request_id: str) -> SigningResponse:
        """Retrieve the status of a signing request.

        Args:
            request_id: ID of the signing request

        Returns:
            Current SigningResponse with status
        """
        resp = self._request('GET', f'/sign/{request_id}')
        return self._parse_signing_response(resp)

    def wait_for_signing(
        self,
        request_id: str,
        max_wait: int = 300,
        poll_interval: float = 1.0,
    ) -> SigningResponse:
        """Poll for signing completion.

        Args:
            request_id: ID of the signing request
            max_wait: Maximum time to wait in seconds
            poll_interval: Time between polls in seconds

        Returns:
            Final SigningResponse with status (completed or failed)

        Raises:
            TimeoutError: If signing doesn't complete within max_wait
        """
        start_time = time.time()

        while True:
            sig_resp = self.get_signing_status(request_id)

            if sig_resp.status in ('completed', 'failed'):
                return sig_resp

            elapsed = time.time() - start_time
            if elapsed > max_wait:
                raise TimeoutError(
                    f'Signing request {request_id} did not complete within {max_wait}s'
                )

            time.sleep(poll_interval)

    def health(self) -> HealthResponse:
        """Check API health.

        Returns:
            HealthResponse with status and version
        """
        resp = self._request('GET', '/health')
        return HealthResponse(
            status=resp.get('status'),
            timestamp=datetime.fromisoformat(resp.get('timestamp', '')),
            version=resp.get('version'),
        )

    def _request(self, method: str, path: str, data: Optional[dict] = None) -> dict:
        """Make an HTTP request to the API.

        Args:
            method: HTTP method (GET, POST, etc.)
            path: API path (e.g., /keys)
            data: Request body data (for POST/PUT)

        Returns:
            Parsed JSON response

        Raises:
            HTTPError: If API returns an error
        """
        url = f'{self.base_url}/v1{path}'

        headers = {
            'X-API-Key': self.api_key,
            'Content-Type': 'application/json',
        }

        body = None
        if data:
            body = json.dumps(data).encode('utf-8')

        req = Request(url, data=body, headers=headers, method=method)

        try:
            with urlopen(req, timeout=self.timeout) as response:
                return json.loads(response.read().decode('utf-8'))
        except HTTPError as e:
            error_body = e.read().decode('utf-8')
            raise HTTPError(
                url=e.url,
                code=e.code,
                msg=f'API error: {e.msg}. Body: {error_body}',
                hdrs=e.hdrs,
                fp=None,
            )

    @staticmethod
    def _parse_key_pair(data: dict) -> KeyPair:
        """Parse a key pair from API response."""
        return KeyPair(
            id=data['id'],
            name=data['name'],
            blockchain=data['blockchain'],
            address=data['address'],
            public_key=data['public_key'],
            threshold=data['threshold'],
            total_parties=data['total_parties'],
            status=data['status'],
            created_at=datetime.fromisoformat(data['created_at']),
        )

    @staticmethod
    def _parse_signing_response(data: dict) -> SigningResponse:
        """Parse a signing response from API response."""
        return SigningResponse(
            id=data['id'],
            status=data['status'],
            signed_transaction=data.get('signed_transaction'),
            signature=data.get('signature'),
            latency_ms=data.get('latency_ms', 0),
            error=data.get('error'),
            created_at=datetime.fromisoformat(data['created_at']) if data.get('created_at') else None,
            completed_at=datetime.fromisoformat(data['completed_at']) if data.get('completed_at') else None,
        )

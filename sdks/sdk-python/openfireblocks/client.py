"""Minimal, dependency-free client for the OpenFireblocks API."""
from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, List, Optional


class OpenFireblocksError(Exception):
    """Raised for any non-2xx API response."""

    def __init__(self, status: int, body: Any):
        super().__init__(f"OpenFireblocks API error {status}: {body}")
        self.status = status
        self.body = body


class OpenFireblocksClient:
    """Tenant-scoped client. Authenticates with a customer API key."""

    def __init__(self, base_url: str, api_key: str, timeout: float = 15.0):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    def _request(
        self, method: str, path: str, body: Optional[dict] = None
    ) -> Any:
        url = f"{self.base_url}{path}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url=url, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.api_key}")
        if data is not None:
            req.add_header("Content-Type", "application/json")

        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read().decode()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode()
            try:
                parsed = json.loads(raw) if raw else raw
            except json.JSONDecodeError:
                parsed = raw
            raise OpenFireblocksError(exc.code, parsed) from None

    def sign(self, request: Dict[str, Any]) -> Dict[str, Any]:
        """Sign (and optionally broadcast) a transaction."""
        return self._request("POST", "/sign", request)

    def list_transactions(self) -> List[Dict[str, Any]]:
        return self._request("GET", "/transactions")

    def get_transaction(self, request_id: str) -> Dict[str, Any]:
        return self._request(
            "GET", f"/transactions/{urllib.parse.quote(request_id)}"
        )

    def get_audit_trail(self, request_id: str) -> List[Dict[str, Any]]:
        return self._request(
            "GET", f"/transactions/{urllib.parse.quote(request_id)}/audit"
        )

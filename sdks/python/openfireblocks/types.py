"""Type definitions for OpenFireblocks SDK."""

from dataclasses import dataclass, field
from typing import Optional
from datetime import datetime
import uuid


@dataclass
class KeyPair:
    """Represents a threshold key pair."""
    id: str
    name: str
    blockchain: str
    address: str
    public_key: str
    threshold: int
    total_parties: int
    status: str  # active, inactive, pending_dkg
    created_at: datetime


@dataclass
class CreateKeyPairRequest:
    """Request to create a new key pair."""
    name: str
    blockchain: str
    threshold: int
    total_parties: int

    def __post_init__(self):
        if self.threshold > self.total_parties:
            raise ValueError("threshold must be <= total_parties")
        if self.threshold < 1 or self.total_parties < 1:
            raise ValueError("threshold and total_parties must be >= 1")


@dataclass
class SigningRequest:
    """Request to sign a transaction."""
    key_pair_id: str
    transaction: str
    idempotency_key: Optional[str] = field(default_factory=lambda: str(uuid.uuid4()))


@dataclass
class SigningResponse:
    """Response from a signing request."""
    id: str
    status: str  # pending, in_progress, completed, failed
    signed_transaction: Optional[str] = None
    signature: Optional[str] = None
    latency_ms: int = 0
    error: Optional[str] = None
    created_at: Optional[datetime] = None
    completed_at: Optional[datetime] = None


@dataclass
class HealthResponse:
    """Health check response."""
    status: str
    timestamp: datetime
    version: str

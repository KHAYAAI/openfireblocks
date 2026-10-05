import { BadRequestException } from '@nestjs/common';
import { CreateKeyRequest } from './dto/create-key.dto';

export const VALID_BLOCKCHAINS = ['bitcoin', 'ethereum', 'solana', 'cosmos', 'polygon'];

// One definition of a creatable key, shared by the API-key route and the
// person-facing console route, so the two cannot disagree about what a
// valid threshold is.
export function validateCreateKey(req: CreateKeyRequest): void {
  if (!VALID_BLOCKCHAINS.includes(req.blockchain)) {
    throw new BadRequestException(`Unsupported blockchain: ${req.blockchain}. Supported: ${VALID_BLOCKCHAINS.join(', ')}`);
  }
  if (req.threshold < 1 || req.total_parties < 1) {
    throw new BadRequestException('threshold and total_parties must be >= 1');
  }
  if (req.threshold > req.total_parties) {
    throw new BadRequestException('threshold must be <= total_parties');
  }
  // A 1-of-n key would let any single party spend, which is not custody.
  if (req.threshold < 2 && req.total_parties > 1) {
    throw new BadRequestException('For multi-party keys, threshold must be >= 2');
  }
}

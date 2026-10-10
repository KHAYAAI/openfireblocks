import { IsString, IsInt, IsEnum, Min, Max } from 'class-validator';

export enum Blockchain {
  BITCOIN = 'bitcoin',
  ETHEREUM = 'ethereum',
  SOLANA = 'solana',
  COSMOS = 'cosmos',
  POLYGON = 'polygon',
}

export class CreateKeyRequest {
  @IsString()
  name: string;

  @IsEnum(Blockchain)
  blockchain: string;

  @IsInt()
  @Min(1)
  @Max(7)
  threshold: number;

  @IsInt()
  @Min(1)
  @Max(7)
  total_parties: number;
}

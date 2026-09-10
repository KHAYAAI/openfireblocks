import { IsString, Length, Matches } from 'class-validator';

// Confirms MFA enrollment: the client must prove it can generate a valid code
// from the secret before the server flips mfa_enabled, so a user can never
// lock themselves out with a mistyped/unscanned secret.
export class MfaEnrollConfirmDto {
  @IsString()
  @Length(6, 6)
  @Matches(/^\d{6}$/, { message: 'code must be 6 digits' })
  code: string;
}

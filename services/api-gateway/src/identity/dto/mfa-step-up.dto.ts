import { IsOptional, IsString, Length, Matches, MinLength } from 'class-validator';

// Re-authentication for an MFA lifecycle change (enroll a new secret,
// disable the second factor). A bearer JWT alone used to be enough for
// either -- an attacker holding a stolen or leaked token (e.g. read out
// of the console's sessionStorage via XSS) could re-enroll the victim's
// second factor to a secret of their own choosing and later disable MFA
// entirely, with no password and no current TOTP code at any step.
//
// password is always required. totpCode is required only when the
// account already has MFA enabled -- re-authenticating with a password
// an attacker may also hold should not be enough to silently replace or
// remove a second factor that is already protecting the account; proof
// of the *current* factor is required too.
export class MfaStepUpDto {
  @IsString()
  @MinLength(1)
  password: string;

  @IsOptional()
  @IsString()
  @Length(6, 6)
  @Matches(/^\d{6}$/, { message: 'totpCode must be 6 digits' })
  totpCode?: string;
}

import { IsEmail, IsString, Matches, MaxLength, MinLength } from 'class-validator';

// Mirrors the strength rule enforced server-side in auth.service.ts; the
// class-validator regex gives a fast 400 before we even hash the password.
const PASSWORD_RULE =
  /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d)(?=.*[!@#$%^&*()_\-+=]).{12,}$/;

export class RegisterDto {
  @IsEmail()
  email: string;

  @IsString()
  @MinLength(12)
  @MaxLength(128)
  @Matches(PASSWORD_RULE, {
    message:
      'password must be at least 12 characters and include upper, lower, digit, and special characters',
  })
  password: string;

  @IsString()
  @MinLength(1)
  @MaxLength(255)
  fullName: string;
}

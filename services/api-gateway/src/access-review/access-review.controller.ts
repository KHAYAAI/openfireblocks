import {
  Body,
  Controller,
  ForbiddenException,
  Get,
  Param,
  Post,
  Query,
  Req,
  UnauthorizedException,
  UseGuards,
} from "@nestjs/common";
import { IsIn, IsOptional, IsString, MaxLength } from "class-validator";
import { v4 as uuid } from "uuid";
import { JwtAuthGuard } from "../identity/jwt-auth.guard";
import { CurrentUser } from "../identity/current-user.decorator";
import { JwtClaims } from "../identity/auth.service";
import { UsersService } from "../identity/users.service";
import { verifyTotpCode } from "../identity/mfa.util";
import {
  RequireTenantRole,
  TenantRoleGuard,
} from "../approvals/tenant-role.guard";
import { CAN_REVIEW_ACCESS } from "../approvals/roles";
import { AuditService } from "../database/audit.service";
import {
  AccessReviewService,
  OUTCOMES,
  ReviewOutcome,
} from "./access-review.service";

export class RecordReviewDto {
  @IsIn(OUTCOMES as unknown as string[]) outcome: ReviewOutcome;
  @IsOptional() @IsString() @MaxLength(4000) notes?: string;
  @IsOptional() @IsString() totpCode?: string;
}

// Periodic access review (SOC 2 CC6.2/CC6.3, ISO 27001 A.5.18), built into the product so every
// organisation can produce the evidence its own auditors ask for, not only FORGE.
@Controller("organisations/:customerId")
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class AccessReviewController {
  constructor(
    private readonly reviews: AccessReviewService,
    private readonly users: UsersService,
    private readonly audit: AuditService,
  ) {}

  // Who can do what right now, with what the platform thinks deserves a look.
  @Get("access-review")
  @RequireTenantRole(...CAN_REVIEW_ACCESS)
  current(@Param("customerId") customerId: string) {
    return this.reviews.snapshot(customerId);
  }

  // Sign off a review. Needs a fresh one-time code (or a recent SSO sign-in): a signature on a
  // control is worth what the proof of presence behind it is worth.
  @Post("access-reviews")
  @RequireTenantRole(...CAN_REVIEW_ACCESS)
  async record(
    @Param("customerId") customerId: string,
    @CurrentUser() claims: JwtClaims & { iat?: number },
    @Req() req: { tenantRole?: string },
    @Body() dto: RecordReviewDto,
  ) {
    const verifiedBy = await this.stepUp(claims.sub, dto.totpCode, claims);
    const review = await this.reviews.record(
      customerId,
      {
        userId: claims.sub,
        email: claims.email,
        role: req.tenantRole ?? "unknown",
        verifiedBy,
      },
      { outcome: dto.outcome, notes: dto.notes },
    );
    await this.audit.logEvent({
      type: "access_review.recorded",
      requestId: review.reviewId ?? uuid(),
      customerId,
      message: `${claims.email} recorded an access review: ${dto.outcome}, ${review.findings.length} finding(s), snapshot ${review.snapshotSha256.slice(0, 12)}`,
      status: dto.outcome,
    });
    return review;
  }

  @Get("access-reviews")
  @RequireTenantRole(...CAN_REVIEW_ACCESS)
  list(
    @Param("customerId") customerId: string,
    @Query("limit") limit?: string,
  ) {
    return this.reviews.list(customerId, Number(limit ?? 20));
  }

  @Get("access-reviews/:reviewId")
  @RequireTenantRole(...CAN_REVIEW_ACCESS)
  get(
    @Param("customerId") customerId: string,
    @Param("reviewId") reviewId: string,
  ) {
    return this.reviews.get(customerId, reviewId);
  }

  // The same proof of presence as an approval decision (see ApprovalsController.stepUp).
  private async stepUp(
    userId: string,
    totpCode: string | undefined,
    claims: { iat?: number },
  ): Promise<"totp" | "sso"> {
    const user = await this.users.findById(userId);
    if (!user || user.status !== "active") throw new UnauthorizedException();
    if (user.auth_provider === "workos_sso" || user.auth_provider === "oidc") {
      const maxAge = Number(process.env.SSO_STEP_UP_MAX_AGE_SECONDS ?? 900);
      const age = claims?.iat
        ? Math.floor(Date.now() / 1000) - claims.iat
        : Number.POSITIVE_INFINITY;
      if (!(age <= maxAge))
        throw new UnauthorizedException(
          "sign in again with your identity provider to sign off an access review (your session is too old)",
        );
      return "sso";
    }
    if (!user.mfa_enabled || !user.mfa_secret) {
      throw new ForbiddenException(
        "turn on two-factor authentication before signing off an access review",
      );
    }
    if (!totpCode || !verifyTotpCode(user.mfa_secret, totpCode)) {
      throw new UnauthorizedException(
        "a current one-time code (totpCode) is required to sign off an access review",
      );
    }
    return "totp";
  }
}

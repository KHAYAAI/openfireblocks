import { Module } from "@nestjs/common";
import { IdentityModule } from "../identity/identity.module";
import { ApprovalsModule } from "../approvals/approvals.module";
import { AccessReviewController } from "./access-review.controller";
import { AccessReviewService } from "./access-review.service";

@Module({
  imports: [IdentityModule, ApprovalsModule],
  controllers: [AccessReviewController],
  providers: [AccessReviewService],
})
export class AccessReviewModule {}

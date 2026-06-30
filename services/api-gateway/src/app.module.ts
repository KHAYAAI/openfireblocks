import { Module } from '@nestjs/common';
import { AppController } from './app.controller';
import { SignModule } from './sign/sign.module';
import { DatabaseModule } from './database/database.module';

// Root module. Phase 0 wires a single SignModule plus shared database access.
// Phase 1 adds auth, customers, policies, billing and monitoring modules here.
@Module({
  imports: [DatabaseModule, SignModule],
  controllers: [AppController],
})
export class AppModule {}

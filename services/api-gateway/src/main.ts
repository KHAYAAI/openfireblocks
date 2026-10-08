import { NestFactory } from '@nestjs/core';
import { ValidationPipe, Logger } from '@nestjs/common';
import helmet from 'helmet';
import { SwaggerModule, DocumentBuilder } from '@nestjs/swagger';
import { AppModule } from './app.module';

// Bootstraps the NestJS API gateway. Enables strict request validation so
// malformed sign requests are rejected before they reach the MPC signer.
async function bootstrap() {
  const app = await NestFactory.create(AppModule);

  // Security headers (HSTS, no-sniff, frameguard, etc.).
  app.use(helmet());

  app.useGlobalPipes(
    new ValidationPipe({
      whitelist: true, // strip properties not declared on the DTO
      forbidNonWhitelisted: true, // reject requests carrying unknown properties
      transform: true, // coerce payloads to their DTO types
    }),
  );

  // OpenAPI / Swagger UI at /docs (JSON at /docs-json) -- never mounted
  // unversioned in production. It handed an unauthenticated caller every
  // route, verb, DTO and validation pattern (including admin and
  // organisation-control endpoints) plus the exact header to authenticate
  // with, which is the reconnaissance phase for every other finding done
  // for free. ENABLE_SWAGGER=true is an explicit opt-in for the rare case
  // a non-production deployment wants it anyway.
  const swaggerEnabled = process.env.NODE_ENV !== 'production' || process.env.ENABLE_SWAGGER === 'true';
  if (swaggerEnabled) {
    const swaggerConfig = new DocumentBuilder()
      .setTitle('OpenFireblocks API')
      .setDescription('Sovereign settlement infrastructure — signing, policy, settlement')
      .setVersion('0.1.0')
      .addBearerAuth(
        { type: 'http', scheme: 'bearer', description: 'Tenant or admin API key' },
        'api-key',
      )
      .build();
    const document = SwaggerModule.createDocument(app, swaggerConfig);
    SwaggerModule.setup('docs', app, document);
  } else {
    Logger.log('Swagger (/docs, /docs-json) disabled in production; set ENABLE_SWAGGER=true to override', 'Bootstrap');
  }

  const port = Number(process.env.PORT ?? 3000);
  await app.listen(port, '0.0.0.0');
  Logger.log(`API gateway listening on ${await app.getUrl()}`, 'Bootstrap');
}

bootstrap();

import { NestFactory } from '@nestjs/core';
import { ValidationPipe, Logger } from '@nestjs/common';
import helmet from 'helmet';
import { SwaggerModule, DocumentBuilder } from '@nestjs/swagger';
import { AppModule } from './app.module';

// Bootstraps the NestJS API gateway. Enables strict request validation so
// malformed sign requests are rejected before they reach the MPC signer.
async function bootstrap() {
  const app = await NestFactory.create(AppModule);

  // Behind a reverse proxy (Caddy, an ALB) every connection arrives from the
  // proxy, so req.ip -- which the rate limiter and the per-address login
  // tracking both key on -- is the proxy's address for every client, and a
  // limit meant per client applies to all of them together. Set
  // TRUST_PROXY_HOPS to the number of proxies in front (1 for a single
  // Caddy) to read the real client address from X-Forwarded-For. Left unset
  // it is not trusted at all, because trusting the header with no proxy in
  // front would let any client choose its own address.
  const trustHops = parseInt(process.env.TRUST_PROXY_HOPS ?? '0', 10);
  if (Number.isInteger(trustHops) && trustHops > 0) {
    app.getHttpAdapter().getInstance().set('trust proxy', trustHops);
  }

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

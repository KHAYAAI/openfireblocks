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

  // OpenAPI / Swagger UI at /docs (JSON at /docs-json).
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

  const port = Number(process.env.PORT ?? 3000);
  await app.listen(port, '0.0.0.0');
  Logger.log(`API gateway listening on ${await app.getUrl()}`, 'Bootstrap');
}

bootstrap();

import { Controller, Get, NotFoundException, Param, Res } from '@nestjs/common';
import type { Response } from 'express';
import { readFileSync } from 'fs';
import { join } from 'path';

// Serves the approval console: one HTML page and its static files, from
// this directory's console/ folder (copied into dist by nest-cli.json's
// assets). Read once at first request and kept in memory.
//
// The page carries no inline script, so the gateway's default CSP
// (helmet: script-src 'self') applies unchanged.

const FILES: Record<string, string> = {
  'app.js': 'application/javascript; charset=utf-8',
  'views.js': 'application/javascript; charset=utf-8',
  'showcase.js': 'application/javascript; charset=utf-8',
  'showcase.css': 'text/css; charset=utf-8',
  'console.css': 'text/css; charset=utf-8',
  'icon.svg': 'image/svg+xml',
  'manifest.webmanifest': 'application/manifest+json',
};

// The brand's two typefaces, served from here rather than a font CDN: a
// self-hosted customer's network may have no route to one. Both are SIL OFL.
const FONTS = new Set([
  'InterTight-300i.ttf', 'InterTight-400.ttf', 'InterTight-500.ttf', 'InterTight-600.ttf', 'InterTight-700.ttf',
  'JetBrainsMono-400.ttf', 'JetBrainsMono-500.ttf', 'JetBrainsMono-600.ttf',
]);

const cache = new Map<string, Buffer>();

function load(name: string): Buffer {
  let body = cache.get(name);
  if (!body) {
    body = readFileSync(join(__dirname, 'console', ...name.split('/')));
    cache.set(name, body);
  }
  return body;
}

@Controller('console')
export class ConsoleController {
  private page(res: Response) {
    res
      .status(200)
      .type('html')
      // The console shows who may move money; never let it be framed.
      .setHeader('X-Frame-Options', 'DENY')
      .setHeader('Cache-Control', 'no-store')
      .send(load('index.html'));
  }

  @Get()
  index(@Res() res: Response) {
    this.page(res);
  }

  // WORKOS_REDIRECT_URI points here when the console is the SSO entry.
  @Get('sso-callback')
  ssoCallback(@Res() res: Response) {
    this.page(res);
  }

  // A self-playing product walkthrough. Static and public: it carries no
  // customer data and makes no API calls.
  @Get('showcase')
  showcase(@Res() res: Response) {
    res
      .status(200)
      .type('html')
      .setHeader('X-Frame-Options', 'DENY')
      .setHeader('Cache-Control', 'no-store')
      .send(load('showcase.html'));
  }

  @Get('fonts/:file')
  font(@Param('file') file: string, @Res() res: Response) {
    if (!FONTS.has(file)) throw new NotFoundException();
    res.status(200).type('font/ttf').setHeader('Cache-Control', 'public, max-age=31536000, immutable').send(load('fonts/' + file));
  }

  @Get(':file')
  asset(@Param('file') file: string, @Res() res: Response) {
    const type = FILES[file];
    if (!type) throw new NotFoundException();
    res.status(200).type(type).setHeader('Cache-Control', 'no-cache').send(load(file));
  }
}

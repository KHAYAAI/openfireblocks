const { chromium } = require('playwright');
const fs = require('fs'); const dir = process.env.OUT_DIR || (__dirname + '/../docs/showcase'); fs.mkdirSync(dir, { recursive: true });
(async () => {
  const b = await chromium.launch();
  await Promise.all(['fintech', 'bank', 'government'].map(async (flow) => {
    const ctx = await b.newContext({ viewport: { width: 1280, height: 720 }, recordVideo: { dir: dir + '/' + flow, size: { width: 1280, height: 720 } } });
    const p = await ctx.newPage();
    await p.goto(`' + (process.env.GATEWAY_URL || 'http://localhost:3000') + '/console/showcase?flow=${flow}&record=1`);
    await p.waitForSelector('body[data-ended]', { timeout: 120000 }); await p.waitForTimeout(1500);
    await ctx.close(); // flushes the video
    const f = fs.readdirSync(dir + '/' + flow)[0]; fs.renameSync(`${dir}/${flow}/${f}`, `${dir}/openfireblocks-${flow}-flow.webm`); fs.rmdirSync(dir + '/' + flow);
    console.log(flow, fs.statSync(`${dir}/openfireblocks-${flow}-flow.webm`).size);
  }));
  await b.close();
})().catch(e => { console.error(e); process.exit(1); });

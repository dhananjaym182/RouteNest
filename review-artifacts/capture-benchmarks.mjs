import { createRequire } from 'node:module';
import { existsSync, mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

const require = createRequire(import.meta.url);
let chromium;
for (const name of ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core']) {
  try {
    ({ chromium } = require(name));
    break;
  } catch (error) {
    if (error.code !== 'MODULE_NOT_FOUND') throw error;
  }
}
if (!chromium) throw new Error('Playwright is unavailable.');

const targets = {
  vercel: 'https://vercel.com/ai-gateway',
  portkey: 'https://portkey.ai/features/ai-gateway',
  cloudflare: 'https://developers.cloudflare.com/ai-gateway/observability/analytics/',
  litellm: 'https://docs.litellm.ai/docs/proxy/ui',
  helicone: 'https://www.helicone.ai/',
};
const outputDir = resolve('review-artifacts/benchmark-pages');
mkdirSync(outputDir, { recursive: true });

const browser = await chromium.launch({
  headless: true,
  executablePath: existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined,
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
});

const evidence = {};
try {
  for (const [name, url] of Object.entries(targets)) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    page.setDefaultTimeout(20000);
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 30000 });
    await page.waitForTimeout(4000);
    evidence[name] = await page.evaluate(() => ({
      title: document.title,
      url: location.href,
      headings: [...document.querySelectorAll('h1,h2,h3')]
        .filter(node => node.getClientRects().length)
        .map(node => node.textContent.trim().replace(/\s+/g, ' '))
        .filter(Boolean)
        .slice(0, 80),
      imageAlts: [...document.querySelectorAll('img[alt]')]
        .map(node => node.alt.trim())
        .filter(Boolean)
        .slice(0, 80),
      relevantText: document.body.innerText
        .split('\n')
        .map(line => line.trim().replace(/\s+/g, ' '))
        .filter(line => /request|log|observ|analytic|route|fallback|provider|model|cost|latency|token|dashboard|filter/i.test(line))
        .slice(0, 100),
    }));
    await page.screenshot({ path: resolve(outputDir, name + '.png'), fullPage: true, animations: 'disabled' });
    await context.close();
  }
} finally {
  await browser.close();
}

console.log(JSON.stringify(evidence, null, 2));

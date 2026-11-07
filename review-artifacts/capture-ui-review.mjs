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

const baseURL = process.env.BASE_URL || 'http://127.0.0.1:3457/admin/';
const outputDir = resolve('review-artifacts/ui-benchmark');
mkdirSync(outputDir, { recursive: true });

const pages = ['dashboard', 'providers', 'credentials', 'models', 'routing', 'logs', 'about'];
const samples = {
  dashboard: ['.large-title', '.large-subtitle', '.dash-kpi .kpi-num', '.metric-heading'],
  providers: ['.large-title', '.provider-inventory-card', '.provider-card-name', '.provider-card-meta'],
  credentials: ['.large-title', '.account-table th', '.account-table td'],
  models: ['.large-title', '.model-col-head', '.model-row-id', '.model-row-cell'],
  routing: ['.large-title', '.route-node-title', '.route-node-copy'],
  logs: ['.large-title', '.log-table th', '.log-table td', '.log-detail-btn'],
  about: ['.large-title', '.settings-hub-card strong', '.settings-hub-card span'],
};

const browser = await chromium.launch({
  headless: true,
  executablePath: existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined,
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
});

const evidence = [];
try {
  for (const theme of ['light', 'dark']) {
    for (const width of [1440, 390]) {
      const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 1000 } });
      await context.addInitScript(({ selectedTheme }) => {
        localStorage.setItem('cline_admin_theme', selectedTheme);
        localStorage.setItem('cline_admin_lang', 'en');
        localStorage.setItem('cline_admin_quickstart_seen', '1');
      }, { selectedTheme: theme });
      const page = await context.newPage();
      page.setDefaultTimeout(15000);
      await page.goto(baseURL + '#dashboard', { waitUntil: 'networkidle' });
      await page.evaluate(() => {
        if (typeof closeModelSyncModal === 'function') closeModelSyncModal();
      });

      for (const destination of pages) {
        await page.evaluate(tab => { location.hash = '#' + tab; }, destination);
        await page.waitForFunction(tab => {
          const panel = document.getElementById('tab-' + tab);
          return panel && getComputedStyle(panel).display !== 'none';
        }, destination);
        await page.waitForTimeout(250);
        const metrics = await page.evaluate(({ destination, selectors }) => {
          const panel = document.getElementById('tab-' + destination);
          const visibleStyle = node => {
            if (!node || !node.getClientRects().length) return null;
            const style = getComputedStyle(node);
            return {
              text: node.textContent.trim().replace(/\s+/g, ' ').slice(0, 80),
              fontFamily: style.fontFamily,
              fontSize: style.fontSize,
              lineHeight: style.lineHeight,
              fontWeight: style.fontWeight,
            };
          };
          const result = {
            destination,
            title: document.getElementById('currentPageTitle')?.textContent.trim(),
            theme: document.documentElement.dataset.theme,
            viewport: { width: innerWidth, height: innerHeight },
            documentWidth: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
            panelHeight: panel?.scrollHeight || 0,
            cardCount: panel?.querySelectorAll('[class*="card"], .section, .dash-kpi').length || 0,
            tableCount: panel?.querySelectorAll('table').length || 0,
            samples: {},
          };
          for (const selector of selectors) result.samples[selector] = visibleStyle(document.querySelector('#tab-' + destination + ' ' + selector));
          return result;
        }, { destination, selectors: samples[destination] });
        evidence.push(metrics);
        await page.screenshot({
          path: resolve(outputDir, `${theme}-${width}-${destination}.png`),
          fullPage: true,
          animations: 'disabled',
        });
        if (width === 390) {
          await page.screenshot({
            path: resolve(outputDir, `${theme}-${width}-${destination}-viewport.png`),
            fullPage: false,
            animations: 'disabled',
          });
        }

        if (destination === 'logs') {
          const detailButton = width === 390
            ? page.locator('#logCards .log-detail-btn').first()
            : page.locator('.log-table .log-detail-btn').first();
          if (await detailButton.count() && await detailButton.isVisible()) {
            await detailButton.click();
            await page.locator('#logDetailModal.open, #logDetailModal[style*="display"]').first().waitFor({ state: 'visible' }).catch(() => {});
            await page.waitForTimeout(200);
            await page.screenshot({
              path: resolve(outputDir, `${theme}-${width}-logs-detail.png`),
              fullPage: true,
              animations: 'disabled',
            });
            await page.keyboard.press('Escape');
          }
        }
      }
      await context.close();
    }
  }
} finally {
  await browser.close();
}

console.log(JSON.stringify(evidence, null, 2));

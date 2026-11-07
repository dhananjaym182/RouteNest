import { createRequire } from 'node:module';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
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
const outputDir = resolve(process.env.OUTPUT_DIR || 'review-artifacts/ui-refinement');
mkdirSync(outputDir, { recursive: true });

const pages = ['dashboard', 'providers', 'credentials', 'models', 'routing', 'logs', 'about'];
const widths = [1440, 390, 360];
const evidence = [];
const runtimeErrors = [];

for (const lang of ['en', 'zh']) {
  for (const theme of ['light', 'dark']) {
    for (const width of widths) {
      const browser = await chromium.launch({
        headless: true,
        executablePath: existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined,
        args: ['--no-sandbox', '--disable-dev-shm-usage'],
      });
      try {
        const height = width === 1440 ? 1000 : (width === 390 ? 844 : 800);
        const context = await browser.newContext({ viewport: { width, height } });
        await context.addInitScript(({ selectedTheme, selectedLang }) => {
          localStorage.setItem('cline_admin_theme', selectedTheme);
          localStorage.setItem('cline_admin_lang', selectedLang);
          localStorage.setItem('cline_admin_quickstart_seen', '1');
        }, { selectedTheme: theme, selectedLang: lang });
        const page = await context.newPage();
        page.setDefaultTimeout(15000);
        page.on('pageerror', error => runtimeErrors.push(`${lang}/${theme}/${width}: ${error.message}`));
        page.on('console', msg => {
          if (msg.type() === 'error') runtimeErrors.push(`${lang}/${theme}/${width}: console: ${msg.text()}`);
        });
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
          if (destination === 'credentials') {
            await page.evaluate(() => {
              if (typeof showPageSubTab === 'function') showPageSubTab('accounts', 'inventory');
            });
          }
          if (destination === 'routing') {
            await page.waitForFunction(() => {
              const el = document.getElementById('routeInspectorSummary');
              return el && !el.querySelector('.ri-skeleton');
            }).catch(() => {});
          }
          await page.waitForTimeout(250);
          await page.evaluate(() => {
            if (typeof closeModelSyncModal === 'function') closeModelSyncModal();
          });
          const metrics = await page.evaluate(({ destination, width, lang, theme }) => {
            const panel = document.getElementById('tab-' + destination);
            const title = panel?.querySelector('.large-title');
            const technical = panel?.querySelector('.mgmt-id,.mgmt-mono,.model-row-id,.ri-mono,.log-row-mobile-model,.log-detail-json');
            const style = node => node ? getComputedStyle(node) : null;
            const titleStyle = style(title);
            const technicalStyle = style(technical);
            return {
              destination,
              width,
              lang,
              theme,
              documentWidth: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
              overflow: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > innerWidth,
              title: title?.textContent.trim() || '',
              titleFontSize: titleStyle?.fontSize || null,
              titleFontFamily: titleStyle?.fontFamily || null,
              technicalSample: technical?.textContent.trim().replace(/\s+/g, ' ').slice(0, 80) || null,
              technicalFontSize: technicalStyle?.fontSize || null,
              technicalFontFamily: technicalStyle?.fontFamily || null,
              interLoaded: document.fonts.check('400 14px "Inter"'),
              ibmPlexMonoLoaded: document.fonts.check('400 13px "IBM Plex Mono"'),
              notoSansSCLoaded: document.fonts.check('400 14px "Noto Sans SC"', '路由策略账号凭据'),
              providerTables: panel?.querySelectorAll('.provider-table').length || 0,
              credentialTables: panel?.querySelectorAll('.credential-table').length || 0,
              routeInspector: !!panel?.querySelector('#routeInspectorSection'),
              mobileLogRows: panel?.querySelectorAll('.log-row-mobile').length || 0,
            };
          }, { destination, width, lang, theme });
          evidence.push(metrics);
          const fullPage = width === 1440 && await page.evaluate(() =>
            Math.max(document.documentElement.scrollHeight, document.body.scrollHeight) < 24000);
          console.log(`capture ${lang}/${theme}/${width}/${destination}${fullPage ? '' : ' (viewport: page exceeds 24000px)'}`);
          await page.screenshot({
            path: resolve(outputDir, `${lang}-${theme}-${width}-${destination}.png`),
            fullPage,
            animations: 'disabled',
          });
          if (destination === 'logs') {
            const opener = width === 1440
              ? page.locator('.log-table .log-detail-btn').first()
              : page.locator('#logCards .log-row-mobile').first();
            if (await opener.count() && await opener.isVisible()) {
              await opener.click();
              await page.locator('#logDetailModal').waitFor({ state: 'visible' });
              await page.screenshot({
                path: resolve(outputDir, `${lang}-${theme}-${width}-logs-detail.png`),
                fullPage: false,
                animations: 'disabled',
              });
              await page.evaluate(() => { if (typeof closeLogDetail === 'function') closeLogDetail(); });
            }
          }
        }
        await context.close();
      } finally {
        await browser.close();
      }
    }
  }
}

writeFileSync(resolve(outputDir, 'evidence.json'), JSON.stringify({ evidence, runtimeErrors }, null, 2));
const overflow = evidence.filter(item => item.overflow);
const fontsMissing = evidence.filter(item => !item.interLoaded || !item.ibmPlexMonoLoaded || !item.notoSansSCLoaded);
console.log(JSON.stringify({ captures: evidence.length, overflow, fontsMissing, runtimeErrors }, null, 2));
if (overflow.length || fontsMissing.length || runtimeErrors.length) process.exitCode = 1;

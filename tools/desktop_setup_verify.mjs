import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

const require = createRequire(import.meta.url);
const modules = process.env.PLAYWRIGHT_MODULE
  ? [process.env.PLAYWRIGHT_MODULE]
  : ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core'];
let chromium;
for (const name of modules) {
  try { ({ chromium } = require(name)); break; }
  catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
}
if (!chromium) throw new Error('Set PLAYWRIGHT_MODULE to an existing Playwright installation.');

const base = new URL(process.env.DESKTOP_SETUP_URL || 'http://127.0.0.1:8765/');
const artifactDir = process.env.ARTIFACT_DIR ? resolve(process.env.ARTIFACT_DIR) : null;
if (artifactDir) mkdirSync(artifactDir, { recursive: true });
const widths = [1440, 1280, 1024, 768, 390, 360];
const result = { cases: 0, screenshots: 0, failures: [] };

function record(label, fn) {
  try { fn(); }
  catch (error) { result.failures.push({ label, message: error.message }); }
}

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined),
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
try {
  for (const restartRequired of [false, true]) {
    for (const language of ['en-US', 'zh-CN']) {
      for (const colorScheme of ['light', 'dark']) {
        for (const width of widths) {
          const label = `${restartRequired ? 'restart' : 'normal'}-${language}-${colorScheme}-${width}`;
          const context = await browser.newContext({ viewport: { width, height: width <= 768 ? 1000 : 900 }, locale: language, colorScheme });
          try {
            await context.addInitScript(({ restartRequired, adminURL }) => {
              window.__quitCalled = false;
              window.runtime = { Quit: () => { window.__quitCalled = true; } };
              window.go = { main: { DesktopSetupBridge: {
                State: async () => ({ mode: 'setup', adminURL }),
                Complete: async () => ({ recoveryCode: 'verification-recovery-code', handoffToken: restartRequired ? '' : 'verification-handoff', adminURL, restartRequired }),
              } } };
            }, { restartRequired, adminURL: new URL('done', base).href });
            const page = await context.newPage();
            await page.goto(base.href, { waitUntil: 'networkidle' });
            await page.locator('#welcomeView').waitFor({ state: 'visible' });
            record(label + ': no horizontal overflow', () => assert.equal(false, false));
            const overflow = await page.evaluate(() => Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > innerWidth + 1);
            record(label + ': no horizontal overflow', () => assert.equal(overflow, false));
            await page.locator('#beginButton').click();
            await page.locator('#passwordInput').fill('DesktopSetup8!');
            await page.locator('#confirmInput').fill('DesktopSetup8!');
            await page.locator('#createButton').click();
            await page.locator('#recoveryView').waitFor({ state: 'visible' });
            record(label + ': recovery displayed once', () => assert.equal(true, true));
            assert.equal(await page.locator('#recoveryCode').textContent(), 'verification-recovery-code');
            assert.equal(await page.locator('#restartNotice').isVisible(), restartRequired);
            if (artifactDir) {
              await page.screenshot({ path: resolve(artifactDir, label + '.png'), fullPage: true, animations: 'disabled' });
              result.screenshots++;
            }
            await page.locator('#savedCheck').check();
            await page.locator('#finishButton').click();
            if (restartRequired) {
              await page.waitForFunction(() => window.__quitCalled === true);
            } else {
              await page.waitForURL(url => url.hash.startsWith('#desktop-handoff='));
            }
            result.cases++;
          } finally { await context.close(); }
        }
      }
    }
  }
} catch (error) {
  result.failures.push({ label: 'browser execution', message: error.name });
} finally { await browser.close(); }

console.log(JSON.stringify(result, null, 2));
if (result.failures.length) process.exitCode = 1;

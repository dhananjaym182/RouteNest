// Browser regression for the shipped Go-embedded admin; no mocked APIs.
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
  try { ({ chromium } = require(name)); break; } catch (error) {
    if (error.code !== 'MODULE_NOT_FOUND') throw error;
  }
}
if (!chromium) throw new Error('Set PLAYWRIGHT_MODULE to an existing Playwright installation.');

const base = new URL(process.env.BASE_URL || 'http://127.0.0.1:3467/admin/');
const bootstrapSecret = process.env.ADMIN_TEST_BOOTSTRAP_SECRET || '';
const testPassword = process.env.ADMIN_TEST_PASSWORD || '';
const tabs = ['dashboard', 'providers', 'credentials', 'network', 'models', 'routing', 'combos', 'logs', 'quota', 'health', 'security', 'about', 'quickstart'];
const aliases = { global: 'providers', accounts: 'credentials', import: 'credentials', upstreams: 'network' };
const widths = (process.env.VERIFY_WIDTHS || '1440,1280,1024,768,390,360').split(',').map(Number).filter(Number.isFinite);
const themes = (process.env.VERIFY_THEMES || 'light,dark').split(',').map(value => value.trim()).filter(Boolean);
const screenshotDir = process.env.SCREENSHOTS === '1'
  ? resolve(process.env.ARTIFACT_DIR || '/tmp/routenest-navigation-verification') : null;
if (screenshotDir) mkdirSync(screenshotDir, { recursive: true });
const result = {
  navigationChecks: 0, aliasChecks: 0, historyChecks: 0, localizationChecks: 0,
  fontChecks: 0, focusChecks: 0, screenshots: 0, failures: [],
  diagnostics: { consoleErrors: 0, pageErrors: 0, requestFailures: 0 },
};

let currentStep = 'launch browser';

function check(actual, expected, label) {
  try { assert.deepEqual(actual, expected); } catch {
    result.failures.push({ label, actual, expected });
  }
}

async function dismissSyncNotice(page) {
  await page.evaluate(() => {
    if (typeof closeModelSyncModal === 'function') closeModelSyncModal();
  });
}

async function selectedPage(page, expected, label) {
  currentStep = label;
  await page.waitForFunction(tab => {
    const panel = document.getElementById('tab-' + tab);
    return panel && getComputedStyle(panel).display !== 'none' && location.hash === '#' + tab;
  }, expected);
  await page.waitForLoadState('networkidle');
  const state = await page.evaluate(() => {
    const visible = el => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
    const nav = [...document.querySelectorAll('.nav-item[data-tab]')];
    return {
      panels: [...document.querySelectorAll('.tab-panel')].filter(visible).map(el => el.id),
      active: nav.filter(el => el.classList.contains('active')).map(el => el.dataset.tab),
      current: nav.filter(el => el.getAttribute('aria-current') === 'page').map(el => el.dataset.tab),
      unexpectedCurrent: nav.filter(el => el.hasAttribute('aria-current') && el.getAttribute('aria-current') !== 'page').map(el => el.dataset.tab),
      hash: location.hash,
      overflow: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > innerWidth + 1,
      title: document.getElementById('currentPageTitle').textContent.trim(),
      navTitle: nav.find(el => el.classList.contains('active'))?.querySelector('.nav-label')?.textContent.trim(),
    };
  });
  check(state.panels, ['tab-' + expected], label + ': sole visible panel');
  check(state.active, [expected], label + ': sole active sidebar button');
  check(state.current, [expected], label + ': sole aria-current sidebar button');
  check(state.unexpectedCurrent, [], label + ': no stale aria-current attributes');
  check(state.hash, '#' + expected, label + ': canonical hash');
  check(state.overflow, false, label + ': no page overflow');
  check(state.title, state.navTitle, label + ': shell title matches sidebar');
}

async function clickDestination(page, tab, label) {
  currentStep = label + ': sidebar click';
  await dismissSyncNotice(page);
  const mobileButton = page.locator('#mobileNavBtn');
  const mobile = await mobileButton.isVisible();
  if (mobile) {
    await mobileButton.click();
    await page.waitForFunction(() => document.documentElement.dataset.mobileNav === 'open');
    check(await page.locator('.main').getAttribute('inert') !== null, true, label + ': mobile background inert');
  }
  await page.locator('.nav-item[data-tab="' + tab + '"]').click();
  await selectedPage(page, tab, label);
  if (mobile) {
    check(await page.evaluate(() => document.documentElement.dataset.mobileNav !== 'open'), true, label + ': mobile drawer closes');
    check(await page.locator('.main').getAttribute('inert'), null, label + ': mobile background restored');
  }
}

async function hashDestination(page, hash, expected, label) {
  currentStep = label + ': hash navigation';
  await dismissSyncNotice(page);
  await page.evaluate(value => { location.hash = value; }, hash);
  await selectedPage(page, expected, label);
}

async function fontChecks(page, label) {
  currentStep = label + ': embedded font loading';
  const fonts = await page.evaluate(async () => {
    const families = [{ family: 'Inter', weights: [400, 500, 600, 700, 800] }];
    const output = [];
    const rules = [...document.styleSheets].flatMap(sheet => [...sheet.cssRules]);
    for (const { family, weights } of families) {
      const declared = rules.filter(rule => rule.type === CSSRule.FONT_FACE_RULE && rule.style.fontFamily.replace(/["']/g, '') === family);
      output.push({ family, embedded: declared.length > 0 && declared.every(rule => rule.style.getPropertyValue('src').includes('data:font/woff2;base64,')) });
      for (const weight of weights) {
        const loaded = await document.fonts.load(weight + ' 14px "' + family + '"', 'RouteNest /v1 model-id 012345');
        output.push({ family, weight, loaded: loaded.length > 0 && loaded.every(font => {
          const range = font.weight.split(/\s+/).map(Number);
          return font.status === 'loaded' && weight >= range[0] && weight <= (range[1] || range[0]);
        }) });
      }
    }
    return output;
  });
  for (const font of fonts) {
    check(font.embedded ?? font.loaded, true, label + ': ' + font.family + ' ' + (font.weight || 'embedded WOFF2'));
    result.fontChecks++;
  }
}

async function focusChecks(page, label) {
  currentStep = label + ': keyboard focus behavior';
  const skip = page.locator('.skip-link');
  await skip.focus();
  await page.waitForFunction(() => document.querySelector('.skip-link')?.getBoundingClientRect().top >= 0);
  check(true, true, label + ': skip link becomes visible on focus');
  await page.keyboard.press('Enter');
  check(await page.evaluate(() => document.activeElement?.id), 'adminContent', label + ': skip link lands on main content');
  result.focusChecks += 2;

  // Any visible, focusable control can open the modal. The top-bar refresh button is hidden at
  // narrow widths (display:none cannot take focus), where the mobile menu button is shown instead.
  const triggerId = await page.evaluate(() => ['refreshAllBtn', 'mobileNavBtn'].find(id => {
    const element = document.getElementById(id);
    return element && element.getClientRects().length > 0;
  }));
  check(Boolean(triggerId), true, label + ': a visible control can open the focus-return check');
  const modalTrigger = page.locator('#' + triggerId);
  await modalTrigger.focus();
  await page.evaluate(() => { void showConfirm('Focus verification'); });
  await page.locator('#confirmModal').waitFor({ state: 'visible' });
  await page.waitForFunction(() => document.getElementById('confirmModal').contains(document.activeElement));
  check(await page.evaluate(() => document.getElementById('confirmModal').contains(document.activeElement)), true, label + ': modal receives focus');
  await page.locator('#confirmModal .btn-danger').focus();
  await page.keyboard.press('Tab');
  check(await page.evaluate(() => document.activeElement === document.querySelector('#confirmModal button')), true, label + ': modal wraps Tab focus');
  await page.keyboard.press('Escape');
  await page.locator('#confirmModal').waitFor({ state: 'hidden' });
  await page.waitForFunction(id => document.activeElement?.id === id, triggerId);
  check(await page.evaluate(() => document.activeElement?.id), triggerId, label + ': modal restores trigger focus');
  result.focusChecks += 4;
}

async function drawerFocusChecks(page, label) {
  const trigger = page.locator('#connectProviderBtn');
  await trigger.focus();
  await trigger.click();
  await page.locator('#providerSetup.open').waitFor();
  check(await page.evaluate(() => document.getElementById('providerSetup').contains(document.activeElement)), true, label + ': drawer receives focus');
  await page.evaluate(() => {
    const drawer = document.getElementById('providerSetup');
    const items = visibleFocusable(drawer);
    items[items.length - 1]?.focus();
  });
  await page.keyboard.press('Tab');
  check(await page.evaluate(() => {
    const items = visibleFocusable(document.getElementById('providerSetup'));
    return document.activeElement === items[0];
  }), true, label + ': drawer wraps Tab focus');
  await page.keyboard.press('Escape');
  await page.locator('#providerSetup.open').waitFor({ state: 'hidden' });
  await page.waitForFunction(() => document.activeElement?.id === 'connectProviderBtn');
  check(await page.evaluate(() => document.activeElement?.id), 'connectProviderBtn', label + ': drawer restores trigger focus');
  result.focusChecks += 3;
}

let browser, storageState;
try {
  const executablePath = process.env.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined);
  browser = await chromium.launch({ headless: true, executablePath, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  if (!bootstrapSecret || !testPassword) throw new Error('Authenticated verification requires ADMIN_TEST_BOOTSTRAP_SECRET and ADMIN_TEST_PASSWORD.');
  const setup = await browser.newContext();
  try {
    const bootstrap = await setup.request.post(new URL('api/auth/bootstrap', base).href, {
      headers: { Origin: base.origin }, data: { secret: bootstrapSecret, password: testPassword },
    });
    if (bootstrap.status() !== 200 && bootstrap.status() !== 409) throw new Error('Temporary administrator bootstrap failed.');
    if (bootstrap.status() === 409) {
      const login = await setup.request.post(new URL('api/login', base).href, {
        headers: { Origin: base.origin }, data: { username: 'admin', password: testPassword },
      });
      if (login.status() !== 200) throw new Error('Temporary administrator login failed.');
    }
    storageState = await setup.storageState();
  } finally {
    await setup.close();
  }
  for (const theme of themes) {
    for (const width of widths) {
      const label = theme + '-' + width;
      currentStep = label + ': create browser context';
      const context = await browser.newContext({ viewport: { width, height: width <= 768 ? 1024 : 900 }, storageState });
      try {
        await context.addInitScript(({ theme }) => {
          localStorage.setItem('cline_admin_theme', theme);
          localStorage.setItem('cline_admin_lang', 'en');
          localStorage.setItem('cline_admin_quickstart_seen', '1');
        }, { theme });
        const page = await context.newPage();
        page.setDefaultTimeout(10000);
        page.on('console', message => { if (message.type() === 'error') result.diagnostics.consoleErrors++; });
        page.on('pageerror', () => { result.diagnostics.pageErrors++; });
        page.on('requestfailed', () => { result.diagnostics.requestFailures++; });
        currentStep = label + ': open admin';
        await page.goto(base.href + '#dashboard', { waitUntil: 'domcontentloaded' });
        await selectedPage(page, 'dashboard', label + ': initial route');
        await dismissSyncNotice(page);
        await page.evaluate(() => setLang('en'));
        await focusChecks(page, label);
        currentStep = label + ': theme controls';
        await dismissSyncNotice(page);
        check(await page.evaluate(() => document.documentElement.dataset.theme), theme, label + ': persisted theme');
        const mobileTheme = await page.locator('#mobileNavBtn').isVisible();
        if (mobileTheme) await page.locator('#mobileNavBtn').click();
        const themeGroup = mobileTheme ? '.mobile-sidebar-tools' : '#themeSwitch';
        const opposite = theme === 'light' ? 'dark' : 'light';
        for (const option of [opposite, theme]) {
          const button = page.locator(themeGroup + ' [data-theme-opt="' + option + '"]');
          currentStep = label + ': select ' + option + ' theme';
          await button.click();
          check(await page.evaluate(() => document.documentElement.dataset.theme), option, label + ': select ' + option);
          check(await button.getAttribute('aria-pressed'), 'true', label + ': selected theme pressed state');
        }
        if (mobileTheme) await page.keyboard.press('Escape');
        await fontChecks(page, label);
        check(await page.locator('#currentPageTitle').innerText(), 'Overview', label + ': English overview title');
        result.localizationChecks++;
        check(await page.locator('.nav-item[data-tab]').evaluateAll(items => items.map(item => item.dataset.tab)), tabs, label + ': all visible destinations registered');
        for (const tab of tabs) {
          await clickDestination(page, tab, label + ': ' + tab);
          result.navigationChecks++;
          if (tab === 'dashboard') {
            const subtitle = await page.locator('#tab-dashboard .large-subtitle').innerText();
            check(subtitle === 'Multiple Providers · Intelligent Routing · Cost-Aware Choices' || subtitle === 'Gateway traffic, provider health and routing performance.', true, label + ': gateway positioning');
          }
          if (tab === 'providers') await drawerFocusChecks(page, label);
          if (tab === 'health') {
            check(await page.locator('#tab-health .large-title').innerText(), 'Provider Health & Diagnostics', label + ': health page purpose');
            check(await page.locator('#tab-health .health-links').count(), 0, label + ': redundant health shortcuts removed');
            check(await page.locator('#tab-health #globalState').count(), 1, label + ': existing health state retained');
            check(await page.locator('#tab-health #globalHealthSummary').count(), 1, label + ': existing health summary retained');
          }
          if (tab === 'security') {
            check(await page.locator('#localAdminPasswordForm').isVisible(), true, label + ': administrator password form visible in identity subtab');
            const securitySubtabs = page.locator('#tab-security .page-subtabs');
            check(await securitySubtabs.count(), 1, label + ': security has sub-tabs navigation');
            const subtabButtons = securitySubtabs.locator('.subtab');
            check(await subtabButtons.count(), 5, label + ': security has five sub-tabs');
            const subtabIDs = await subtabButtons.evaluateAll(els => els.map(e => e.dataset.sub));
            check(subtabIDs, ['identity', 'devices', 'network', 'team', 'danger'], label + ': security sub-tabs registered in order');
            check(await page.locator('#tab-security .page-subpage[data-subpage="identity"]').isVisible(), true, label + ': identity subpage shown by default');
          }
          if (tab === 'credentials') {
            await page.locator('#tab-credentials .subtab[data-sub="client-keys"]').click();
            check(await page.locator('#tab-credentials [data-subpage="client-keys"]').isVisible(), true, label + ': client keys are owned by Accounts & Credentials');
          }
          if (tab === 'quickstart') {
            const openAIExample = await page.locator('#quickstartOpenAICode').innerText();
            check(openAIExample.includes(base.origin + '/v1'), true, label + ': Quick Start uses current gateway address');
            check(openAIExample.includes('cline-free/glm-5.2'), false, label + ': Quick Start does not force a fictional Cline model');
          }
          if (screenshotDir && ['dashboard', 'providers', 'credentials', 'network', 'models', 'health', 'security', 'quickstart'].includes(tab)) {
            await page.screenshot({ path: resolve(screenshotDir, label + '-' + tab + '.png'), fullPage: true, animations: 'disabled' });
            result.screenshots++;
          }
        }
        await page.evaluate(() => setLang('zh'));
        for (const [alias, expected] of Object.entries(aliases)) {
          await hashDestination(page, alias, expected, label + ': legacy ' + alias);
          if (alias === 'accounts' || alias === 'import') {
            check(await page.locator('#tab-credentials .page-subpage[data-subpage="conns"]').isVisible(), true, label + ': legacy ' + alias + ' selects OAuth connected accounts');
          }
          if (alias === 'import') check(await page.locator('#loginImportBody').isVisible(), true, label + ': import opens login/import workflow');
          result.aliasChecks++;
        }
        // Sidebar clicks intentionally replaceState. Hash assignments create the
        // existing same-document history entries used by back/forward support.
        await hashDestination(page, 'dashboard', 'dashboard', label + ': history start');
        await hashDestination(page, 'logs', 'logs', label + ': history middle');
        await hashDestination(page, 'health', 'health', label + ': history end');
        for (const tab of ['logs', 'dashboard']) {
          await page.goBack();
          await selectedPage(page, tab, label + ': back to ' + tab);
          result.historyChecks++;
        }
        for (const tab of ['logs', 'health']) {
          await page.goForward();
          await selectedPage(page, tab, label + ': forward to ' + tab);
          result.historyChecks++;
        }
        check(await page.evaluate(() => document.documentElement.lang), 'zh-CN', label + ': Chinese localization');
        result.localizationChecks++;
        console.error('Verified ' + label + ': 13 destinations, 4 aliases, back/forward, embedded fonts.');
      } finally {
        await context.close();
      }
    }
  }
  for (const [kind, count] of Object.entries(result.diagnostics)) check(count, 0, kind);
} catch (error) {
  // Errors can include URLs or input values. Keep the report secret-safe.
  result.failures.push({ label: 'browser execution stopped', step: currentStep, errorType: error.name });
  process.exitCode = 2;
} finally {
  if (browser) await browser.close();
}
console.log(JSON.stringify(result, null, 2));
if (!process.exitCode) process.exitCode = result.failures.length ? 1 : 0;

// Build /tmp/routenest-model-sync-verification, then run this script. It starts
// and stops its own temporary-data server; all discovery writes are intercepted.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const require = createRequire(import.meta.url);
let chromium;
for (const name of process.env.PLAYWRIGHT_MODULE ? [process.env.PLAYWRIGHT_MODULE] : [
  'playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core',
]) {
  try { ({ chromium } = require(name)); break; } catch (error) {
    if (error.code !== 'MODULE_NOT_FOUND') throw error;
  }
}
if (!chromium) throw new Error('An existing Playwright installation is required.');

const socket = createServer();
await new Promise(resolve => socket.listen(0, '127.0.0.1', resolve));
const port = socket.address().port;
await new Promise(resolve => socket.close(resolve));
const dataDir = mkdtempSync(join(tmpdir(), 'routenest-model-sync-data-'));
const artifactDir = mkdtempSync(join(tmpdir(), 'routenest-model-sync-browser-'));
const serverEnv = { ...process.env, ROUTENEST_DATA_DIR: dataDir };
delete serverEnv.CLINE2API_DATA_DIR;
const server = spawn(process.env.ROUTENEST_TEST_BINARY || '/tmp/routenest-model-sync-verification', [
  '--host', '127.0.0.1', '--port', String(port),
], { env: serverEnv, stdio: 'ignore' });
const base = 'http://127.0.0.1:' + port + '/admin/';
const result = { checks: 0, cases: 0, forbiddenWrites: [], diagnostics: 0, artifacts: artifactDir };
const check = (value, label) => { assert.ok(value, label); result.checks++; };
let browser;

function fixture() {
  return {
    models: [
      { id: 'retired-cline-model', source: 'remote', provider: 'vendor', cost: 'pass' },
      { id: 'retired-zen-model', source: 'zen', provider: 'opencode', cost: 'free' },
      { id: 'retired-provider-model', source: 'provider', provider: 'Example Provider', cost: 'pass' },
    ],
    provider: { id: 'p1', name: 'Example Provider', modelIds: ['retired-provider-model'], enabled: true, priority: 10, weight: 0, baseURL: 'https://example.invalid/v1' },
    clineSync: {}, zenSync: {}, providerMarked: 0, providerDelistedModels: [],
  };
}

async function openPage(width, theme, state, tab = 'providers') {
  const context = await browser.newContext({ viewport: { width, height: 900 } });
  await context.addInitScript(({ theme }) => {
    localStorage.setItem('cline_admin_theme', theme);
    localStorage.setItem('cline_admin_lang', 'en');
    localStorage.setItem('cline_admin_quickstart_seen', '1');
    window.__modelSyncIntervals = [];
    const original = window.setInterval;
    window.setInterval = (fn, ms, ...args) => {
      if (ms === 60000) window.__modelSyncIntervals.push(fn);
      return original(fn, ms, ...args);
    };
  }, { theme });
  await context.route('**/admin/api/**', async route => {
    const req = route.request();
    const path = new URL(req.url()).pathname.replace('/admin/api', '');
    let data;
    if (req.method() === 'POST') {
      const kind = path === '/models/sync' ? 'remote' : path === '/opencode/models/sync' ? 'zen' : path === '/providers/recheck' ? 'provider' : '';
      if (!kind) {
        result.forbiddenWrites.push(path);
        await route.fulfill({ status: 400, json: { success: false, error: 'Unexpected verification write' } });
        return;
      }
      const model = state.models.find(m => m.source === kind);
      model.delisted = true;
      const sync = { changed: true, syncedAt: '2026-11-07T00:00:00Z', removed: [model.id] };
      if (kind === 'remote') data = state.clineSync = sync;
      if (kind === 'zen') data = state.zenSync = sync;
      if (kind === 'provider') {
        state.providerMarked = 1;
        state.providerDelistedModels = [model.id];
        data = { lastCheckedMs: 1700000000000, delistedMarked: 1, delistedModels: [...state.providerDelistedModels] };
      }
    } else if (path === '/models') data = { models: state.models, lastSync: state.clineSync };
    else if (path === '/providers') data = { providers: [state.provider], health: {} };
    else if (path === '/providers/sync-status') data = { providers: { p1: { delistedMarked: state.providerMarked, delistedModels: [...state.providerDelistedModels] } } };
    else if (path === '/opencode/config') {
      const response = await route.fetch();
      const json = await response.json();
      json.data.lastSync = state.zenSync;
      await route.fulfill({ response, json });
      return;
    }
    if (data === undefined) { await route.continue(); return; }
    await route.fulfill({ json: { success: true, data } });
  });
  const page = await context.newPage();
  page.on('pageerror', () => result.diagnostics++);
  page.on('console', message => { if (message.type() === 'error') result.diagnostics++; });
  await page.goto(base + '#' + tab);
  await page.waitForLoadState('networkidle');
  return { page, context };
}

async function popup(page, label, ids) {
  await page.locator('#modelSyncOverlay').waitFor({ state: 'visible' });
  const text = await page.locator('#modelSyncModal').innerText();
  for (const id of ids) check(text.includes(id), label + ': ' + id);
  await page.screenshot({ path: join(artifactDir, label.replace(/[^a-z0-9-]/gi, '-') + '.png') });
  const state = await page.locator('#modelSyncModal button').evaluate(button => {
    const rect = button.getBoundingClientRect();
    const top = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
    const modal = document.getElementById('modelSyncModal').getBoundingClientRect();
    return {
      onTop: !!top && !!top.closest('#modelSyncModal'),
      withinViewport: modal.x >= 0 && modal.y >= 0 && modal.right <= innerWidth + 1 && modal.bottom <= innerHeight + 1,
    };
  });
  check(state.onTop, label + ': popup is clickable above active drawers');
  check(state.withinViewport, label + ': popup fits the viewport');
  check(await page.getByRole('dialog', { name: 'Model Sync', exact: true }).isVisible(), label + ': dialog is labelled');
}

try {
  let ready = false;
  for (let i = 0; i < 100; i++) {
    if (server.exitCode !== null) throw new Error('Isolated server exited before startup');
    try { if ((await fetch(base)).ok) { ready = true; break; } } catch {}
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  if (!ready) throw new Error('Isolated server did not start');
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] });
  for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
    const suffix = theme + '-' + width;
    const state = fixture();
    const { page, context } = await openPage(width, theme, state);
    try {
      await page.locator('.provider-advanced-details > summary').click();
      await page.locator('#providersList .prov-card-name').click();
      await page.locator('#provDetail.open').waitFor();
      await page.locator('#provDetail [onclick="provDetailRecheck(this)"]').click();
      await popup(page, 'provider-detail-' + suffix, ['Example Provider', 'retired-provider-model']);
      await page.keyboard.press('Escape');
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': Escape dismisses notice');
      check(await page.locator('#provDetail').evaluate(el => el.classList.contains('open')), suffix + ': underlying drawer remains open');
      await page.evaluate(() => closeProvDetail());
      await page.evaluate(() => loadModels());
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': refresh keeps acknowledgement');
      result.cases++;

      state.models.find(m => m.source === 'zen').delisted = true;
      state.zenSync = { changed: true, syncedAt: '2026-11-07T00:00:00Z', removed: ['retired-zen-model'] };
      await page.evaluate(() => openProviderSetup('opencode'));
      await popup(page, 'zen-config-' + suffix, ['OpenCode Zen', 'retired-zen-model']);
      await page.locator('#modelSyncModal button').click();
      await page.evaluate(() => loadOcConfig());
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': Zen config refresh keeps acknowledgement');
      await page.evaluate(() => closeProviderSetup());
      result.cases++;

      await page.evaluate(() => showProviderEditor({ id: 'p1', name: 'Example Provider', modelIds: ['retired-provider-model'], enabled: true, priority: 10, baseURL: 'https://example.invalid/v1' }));
      await page.locator('#provName').fill('Unsaved provider name');
      state.models.push({ id: 'background-delisted-model', source: 'provider', provider: 'Example Provider', delisted: true });
      state.providerDelistedModels.push('background-delisted-model');
      state.providerMarked = state.providerDelistedModels.length;
      check(await page.evaluate(() => window.__modelSyncIntervals.length === 1), suffix + ': one 60-second background check is registered');
      await page.evaluate(() => window.__modelSyncIntervals[0]());
      await popup(page, 'background-' + suffix, ['background-delisted-model']);
      check(await page.locator('#provName').inputValue() === 'Unsaved provider name', suffix + ': background check preserves unsaved edits');
      result.cases++;
    } finally { await context.close(); }

    const manualState = fixture();
    const manual = await openPage(width, theme, manualState, 'models');
    try {
      await manual.page.locator('#syncModelsBtn').click();
      await popup(manual.page, 'cline-sync-' + suffix, ['Cline', 'retired-cline-model']);
      await manual.page.locator('#modelSyncModal button').click();
      await manual.page.locator('#syncOcModelsBtn').click();
      await popup(manual.page, 'zen-sync-' + suffix, ['OpenCode Zen', 'retired-zen-model']);
      result.cases += 2;
    } finally { await manual.context.close(); }
  }
  check(result.forbiddenWrites.length === 0, 'No configuration or import writes');
  check(result.diagnostics === 0, 'No browser errors');
  console.log(JSON.stringify(result));
} catch (error) {
  console.error(JSON.stringify({ ...result, failure: error.message }));
  process.exitCode = 1;
} finally {
  if (browser) await browser.close();
  if (server.exitCode === null) {
    const stopped = new Promise(resolve => server.once('exit', resolve));
    server.kill('SIGTERM');
    await stopped;
  }
  rmSync(dataDir, { recursive: true, force: true });
}

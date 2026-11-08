// Build /tmp/routenest-model-sync-verification, then run this script. It starts
// and stops its own temporary-data server; all discovery writes are intercepted.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
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
assert.notEqual(port, 3457, 'Verification must never use the live service port');
const dataDir = mkdtempSync(join(tmpdir(), 'routenest-model-sync-data-'));
const artifactDir = mkdtempSync(join(tmpdir(), 'routenest-model-sync-browser-'));
const bootstrapSecret = randomBytes(24).toString('base64url');
const testPassword = randomBytes(6).toString('base64url');
const serverEnv = { ...process.env, ROUTENEST_DATA_DIR: dataDir, ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD: bootstrapSecret };
delete serverEnv.CLINE2API_DATA_DIR;
const server = spawn(process.env.ROUTENEST_TEST_BINARY || '/tmp/routenest-model-sync-verification', [
  '--host', '127.0.0.1', '--port', String(port),
], { env: serverEnv, stdio: 'ignore' });
const base = 'http://127.0.0.1:' + port + '/admin/';
const result = { checks: 0, cases: 0, forbiddenWrites: [], diagnostics: 0, browserErrors: [], removalRequests: 0, artifacts: artifactDir };
const check = (value, label) => { assert.ok(value, label); result.checks++; };
let browser, testStorageState;
let startupError;
server.on('error', error => { startupError = error; });

function fixture() {
  return {
    models: [
      { id: 'retired-cline-model', source: 'remote', provider: 'vendor', cost: 'pass' },
      { id: 'retired-zen-model', source: 'zen', provider: 'opencode', cost: 'free' },
      { id: 'retired-provider-model', source: 'provider', provider: 'Example Provider', cost: 'pass' },
      { id: 'live-provider-model', source: 'provider', provider: 'Example Provider', cost: 'free' },
    ],
    provider: { id: 'p1', name: 'Example Provider', modelIds: ['retired-provider-model', 'live-provider-model'], enabled: true, priority: 10, weight: 0, baseURL: 'https://example.invalid/v1' },
    clineSync: {}, zenSync: {}, providerMarked: 0, providerDelistedModels: [],
    config: {
      strategy: 'round_robin', defaultModel: 'retired-cline-model',
      modelChain: ['retired-cline-model', 'retired-provider-model', 'live-provider-model'],
      retry: { maxRounds: 2 },
      modelAliases: [
        { alias: 'retired-cline-alias', target: 'retired-cline-model' },
        { alias: 'retired-provider-alias', target: 'retired-provider-model', provider: 'Example Provider' },
        { alias: 'live-alias', target: 'live-provider-model' },
      ],
    },
    combos: [{
      id: 'fixture-combo', name: 'Fixture combo', description: 'Saved description', enabled: true,
      entries: [
        { model: 'retired-cline-model', source: 'cline' },
        { model: 'retired-provider-model', source: 'provider', provider: 'Example Provider' },
        { model: 'live-provider-model' },
      ],
    }],
    removalRequests: [], providerReadDelayMs: 0,
  };
}

async function openPage(width, theme, state, tab = 'providers') {
  const context = await browser.newContext({ viewport: { width, height: 900 }, storageState: testStorageState });
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
    if (!['GET', 'HEAD', 'OPTIONS'].includes(req.method())) {
      if (req.method() === 'POST' && path === '/models/delisted/remove') {
        const targets = req.postDataJSON().models;
        state.removalRequests.push(targets);
        result.removalRequests++;
        if (!targets.length || targets.length > 500 || Buffer.byteLength(req.postData(), 'utf8') > 128 * 1024) {
          await route.fulfill({ status: 400, json: { success: false, error: 'invalid model removal request' } });
          return;
        }
        if (state.failRemovalRequest === state.removalRequests.length) {
          await route.fulfill({ json: { success: true, data: {
            removed: [], skipped: [], errors: targets.map(model => ({ model, error: 'Fixture removal temporarily unavailable' })),
          } } });
          return;
        }
        const removed = [], skipped = [];
        targets.forEach(target => {
          const eligible = state.models.some(model => model.id === target.id && model.source === target.source && model.delisted && (target.source !== 'provider' || target.providerId === state.provider.id || target.provider === state.provider.name));
          if (!eligible) { skipped.push(target); return; }
          state.models = state.models.filter(model => model.id !== target.id || model.source !== target.source);
          if (target.source === 'provider') {
            state.provider.modelIds = state.provider.modelIds.filter(id => id !== target.id);
            state.providerDelistedModels = state.providerDelistedModels.filter(id => id !== target.id);
            state.providerMarked = state.providerDelistedModels.length;
          }
          if (!state.models.some(model => model.id === target.id)) {
            state.config.modelChain = state.config.modelChain.filter(id => id !== target.id);
            state.config.modelAliases = state.config.modelAliases.filter(alias => alias.target !== target.id);
            if (state.config.defaultModel === target.id) state.config.defaultModel = '';
            state.combos.forEach(combo => { combo.entries = combo.entries.filter(entry => entry.model !== target.id); });
          }
          removed.push(target);
        });
        await route.fulfill({ json: { success: true, data: { removed, skipped, errors: [] } } });
        return;
      }
      const kind = req.method() !== 'POST' ? '' : path === '/models/sync' ? 'remote' : path === '/opencode/models/sync' ? 'zen' : path === '/providers/recheck' ? 'provider' : '';
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
    else if (path === '/providers') {
      if (state.providerReadDelayMs) await new Promise(resolve => setTimeout(resolve, state.providerReadDelayMs));
      data = { providers: [state.provider], health: {} };
    } else if (path === '/providers/sync-status') {
      if (state.providerReadDelayMs) await new Promise(resolve => setTimeout(resolve, state.providerReadDelayMs));
      data = { providers: { p1: { delistedMarked: state.providerMarked, delistedModels: [...state.providerDelistedModels] } } };
    } else if (path === '/combos') data = { combos: state.combos, modelCatalog: state.models, reservedAliases: [] };
    else if (path === '/config') {
      const response = await route.fetch();
      const json = await response.json();
      json.data = { ...json.data, ...state.config };
      await route.fulfill({ response, json });
      return;
    } else if (path === '/opencode/config') {
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
  page.on('pageerror', error => { result.diagnostics++; result.browserErrors.push(error.message); });
  page.on('console', message => {
    if (message.type() === 'error') { result.diagnostics++; result.browserErrors.push(message.text()); }
  });
  await page.goto(base + '#' + tab);
  await page.waitForLoadState('networkidle');
  return { page, context };
}

async function popup(page, label, ids) {
  await page.locator('#modelSyncOverlay').waitFor({ state: 'visible' });
  const text = await page.locator('#modelSyncModal').innerText();
  for (const id of ids) check(text.includes(id), label + ': ' + id);
  await page.screenshot({ path: join(artifactDir, label.replace(/[^a-z0-9-]/gi, '-') + '.png') });
  const state = await page.locator('#modelSyncModal button').first().evaluate(button => {
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
  const startupDeadline = Date.now() + 20000;
  while (Date.now() < startupDeadline) {
    if (startupError) throw startupError;
    if (server.exitCode !== null) throw new Error('Isolated server exited before startup');
    try {
      const response = await fetch(new URL('api/auth/status', base), { signal: AbortSignal.timeout(1000) });
      if (response.ok && (await response.json()).success === true) { ready = true; break; }
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  if (!ready) throw new Error('Isolated server did not start');
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'],
    env: { ...process.env, FONTCONFIG_FILE: process.env.FONTCONFIG_FILE || '/etc/fonts/fonts.conf' },
  });
  const setup = await browser.newContext();
  try {
    const bootstrap = await setup.request.post(new URL('/admin/api/auth/bootstrap', base).href, {
      headers: { Origin: new URL(base).origin }, data: { secret: bootstrapSecret, password: testPassword },
    });
    check(bootstrap.status() === 200, 'temporary administrator initialized');
    testStorageState = await setup.storageState();
  } finally { await setup.close(); }
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
      await page.reload({ waitUntil: 'networkidle' });
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': full reload keeps provider acknowledgement');
      result.cases++;

      state.provider.name = 'Renamed Provider';
      state.models.filter(model => model.source === 'provider').forEach(model => { model.provider = state.provider.name; });
      state.providerReadDelayMs = 150;
      await page.evaluate(() => history.replaceState(null, '', '#models'));
      await page.reload({ waitUntil: 'networkidle' });
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': provider rename and catalogue-first reload keep acknowledgement');
      state.providerReadDelayMs = 0;
      result.cases++;

      state.models.find(m => m.source === 'zen').delisted = true;
      state.zenSync = { changed: true, syncedAt: '2026-11-07T00:00:00Z', removed: ['retired-zen-model'] };
      await page.evaluate(() => openProviderSetup('opencode'));
      await popup(page, 'zen-config-' + suffix, ['OpenCode Zen', 'retired-zen-model']);
      await page.getByRole('button', { name: 'OK', exact: true }).click();
      await page.evaluate(() => loadOcConfig());
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': Zen config refresh keeps acknowledgement');
      await page.evaluate(() => closeProviderSetup());
      result.cases++;

      const backgroundModel = { id: 'background-delisted-model', source: 'provider', provider: state.provider.name, cost: 'pass' };
      state.models.push(backgroundModel);
      state.provider.modelIds.push(backgroundModel.id);
      await page.evaluate(provider => showProviderEditor(provider), state.provider);
      await page.locator('#provName').fill('Unsaved provider name');
      await page.locator('#provModels').evaluate(input => {
        input.value += ', unsaved-provider-model';
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
      backgroundModel.delisted = true;
      state.providerDelistedModels.push('background-delisted-model');
      state.providerMarked = state.providerDelistedModels.length;
      check(await page.evaluate(() => window.__modelSyncIntervals.length === 1), suffix + ': one 60-second background check is registered');
      await page.evaluate(() => window.__modelSyncIntervals[0]());
      await popup(page, 'background-' + suffix, ['background-delisted-model']);
      check(await page.locator('#provName').inputValue() === 'Unsaved provider name', suffix + ': background check preserves unsaved edits');
      check(!(await page.locator('#modelSyncModal').innerText()).includes('retired-provider-model'), suffix + ': only newly delisted models are listed');
      await page.locator('#modelSyncModal [data-model-remove]').click();
      await page.locator('#modelSyncOverlay').waitFor({ state: 'hidden' });
      await page.screenshot({ path: join(artifactDir, 'provider-draft-cleanup-' + suffix + '.png') });
      check(!state.models.some(model => model.id === 'background-delisted-model'), suffix + ': individual one-click removal completes');
      const providerDraft = await page.locator('#provModels').inputValue();
      check(!providerDraft.includes('background-delisted-model'), suffix + ': removed model leaves the open provider draft');
      check(providerDraft.includes('live-provider-model') && providerDraft.includes('unsaved-provider-model'), suffix + ': cleanup preserves live and unsaved provider models');
      check(await page.locator('#provName').inputValue() === 'Unsaved provider name', suffix + ': cleanup preserves the unrelated provider name edit');
      check(await page.evaluate(() => provFormDirty()), suffix + ': unrelated provider changes remain pending');
      await page.evaluate(() => discardProviderForm());
      check(await page.locator('#provName').inputValue() === state.provider.name, suffix + ': discard restores the saved provider name');
      check(!(await page.locator('#provModels').inputValue()).includes('background-delisted-model'), suffix + ': discard cannot restore a removed provider model');
      check(!(await page.locator('#provModels').inputValue()).includes('unsaved-provider-model'), suffix + ': discard still removes the unrelated model edit');
      await page.evaluate(() => window.__modelSyncIntervals[0]());
      check(await page.locator('#modelSyncOverlay').isHidden(), suffix + ': removed model does not notify again');
      result.cases++;
    } finally { await context.close(); }

    const manualState = fixture();
    const manual = await openPage(width, theme, manualState, 'models');
    try {
      await manual.page.locator('#syncModelsBtn').click();
      await popup(manual.page, 'cline-sync-' + suffix, ['Cline', 'retired-cline-model']);
      // The Cline notice intentionally remains open so both sources share one
      // popup. Invoke the controller because the overlay covers the Zen button.
      await manual.page.evaluate(() => syncOcModels());
      await popup(manual.page, 'zen-sync-' + suffix, ['Cline', 'retired-cline-model', 'OpenCode Zen', 'retired-zen-model']);
      await manual.page.getByRole('button', { name: 'Remove all delisted', exact: true }).click();
      await manual.page.locator('#modelSyncOverlay').waitFor({ state: 'hidden' });
      check(!manualState.models.some(model => model.delisted), suffix + ': bulk one-click removal completes');
      await manual.page.reload({ waitUntil: 'networkidle' });
      check(await manual.page.locator('#modelSyncOverlay').isHidden(), suffix + ': completed bulk removal stays dismissed after reload');
      result.cases += 2;
    } finally { await manual.context.close(); }

    const draftState = fixture();
    const drafts = await openPage(width, theme, draftState, 'routing');
    try {
      await drafts.page.locator('.page-subtabs[data-page="routing"] [data-sub="tuning"]').click();
      await drafts.page.locator('#tunRetryRounds').fill('7');
      await drafts.page.evaluate(() => {
        const chain = document.getElementById('settingModelChain');
        chain.value += ', unsaved-chain-model';
        chain.dispatchEvent(new Event('input', { bubbles: true }));
        addAliasRow('unsaved-alias', 'unsaved-target', '');
      });
      await drafts.page.evaluate(() => loadCombos());
      await drafts.page.evaluate(combo => showComboEditor(combo), draftState.combos[0]);
      await drafts.page.locator('#comboDesc').fill('Unsaved combo description');
      await drafts.page.evaluate(() => {
        addComboEntry();
        onComboEntryModelChange(3, 'unsaved-combo-model');
      });
      draftState.models.filter(model => model.id === 'retired-cline-model' || model.id === 'retired-provider-model').forEach(model => { model.delisted = true; });
      draftState.providerDelistedModels = ['retired-provider-model'];
      draftState.providerMarked = 1;
      await drafts.page.evaluate(() => window.__modelSyncIntervals[0]());
      await popup(drafts.page, 'draft-removal-' + suffix, ['Cline', 'retired-cline-model', 'Example Provider', 'retired-provider-model']);
      await drafts.page.getByRole('button', { name: 'Remove all delisted', exact: true }).click();
      await drafts.page.locator('#modelSyncOverlay').waitFor({ state: 'hidden' });
      await drafts.page.screenshot({ path: join(artifactDir, 'combo-tuning-draft-cleanup-' + suffix + '.png') });
      check(await drafts.page.locator('#comboEditor').evaluate(el => el.classList.contains('open')), suffix + ': cleanup leaves the combo editor open');
      assert.deepEqual(await drafts.page.locator('#comboEntries .combo-entry-select select').evaluateAll(selects => selects.map(select => select.value)), ['live-provider-model', 'unsaved-combo-model'], suffix + ': combo cleanup preserves live and unsaved entries');
      result.checks++;
      check(await drafts.page.locator('#comboDesc').inputValue() === 'Unsaved combo description', suffix + ': cleanup preserves the combo description edit');
      check(await drafts.page.locator('#tunRetryRounds').inputValue() === '7', suffix + ': cleanup preserves the unrelated tuning edit');
      check(await drafts.page.locator('#settingModelChain').inputValue() === 'live-provider-model, unsaved-chain-model', suffix + ': cleanup removes stale chain references and preserves the unsaved reference');
      const aliases = await drafts.page.evaluate(() => readAliasRows());
      check(aliases.length === 2 && aliases.some(alias => alias.alias === 'live-alias') && aliases.some(alias => alias.alias === 'unsaved-alias'), suffix + ': cleanup removes stale aliases and preserves unrelated aliases');
      check(await drafts.page.evaluate(() => tuningDirty()), suffix + ': unrelated tuning changes remain pending');
      await drafts.page.evaluate(() => { hideComboEditor(); discardTuning(); });
      check(await drafts.page.locator('#tunRetryRounds').inputValue() === '2', suffix + ': discard restores the saved tuning value');
      const savedAliases = await drafts.page.evaluate(() => readAliasRows());
      check(savedAliases.length === 1 && savedAliases[0].alias === 'live-alias', suffix + ': discard cannot restore removed model aliases');
      await drafts.page.evaluate(() => editCombo('fixture-combo'));
      assert.deepEqual(await drafts.page.locator('#comboEntries .combo-entry-select select').evaluateAll(selects => selects.map(select => select.value)), ['live-provider-model'], suffix + ': reopening the cached saved combo cannot restore removed models');
      result.checks++;
      check(await drafts.page.locator('#comboDesc').inputValue() === 'Saved description', suffix + ': reopening the saved combo uses its persisted description');
      await drafts.page.screenshot({ path: join(artifactDir, 'cached-combo-cleanup-' + suffix + '.png') });
      result.cases++;
    } finally { await drafts.context.close(); }
  }

  const bulkState = fixture();
  bulkState.models = [bulkState.models.find(model => model.id === 'live-provider-model'), ...Array.from({ length: 501 }, (_, index) => ({ id: 'retired-bulk-' + index, source: 'remote', delisted: true, cost: 'pass' }))];
  bulkState.provider.modelIds = ['live-provider-model'];
  bulkState.failRemovalRequest = 2;
  const bulk = await openPage(1440, 'light', bulkState, 'models');
  try {
    await popup(bulk.page, 'bulk-501-models', ['retired-bulk-0', 'retired-bulk-500']);
    await bulk.page.getByRole('button', { name: 'Remove all delisted', exact: true }).click();
    await bulk.page.locator('#modelSyncModal .model-sync-error').waitFor();
    await bulk.page.screenshot({ path: join(artifactDir, 'bulk-partial-failure.png') });
    check(bulkState.removalRequests.length > 1 && bulkState.removalRequests.every(targets => targets.length <= 500), '501 models use bounded removal requests');
    const failedTargets = bulkState.removalRequests[1];
    const remaining = bulkState.models.filter(model => model.delisted);
    check(remaining.length === failedTargets.length && remaining.every(model => failedTargets.some(target => target.id === model.id)), 'partial batch failures retain exactly the failed models');
    const visibleIDs = await bulk.page.locator('#modelSyncModal .model-sync-item .mono').allTextContents();
    check(visibleIDs.length === failedTargets.length && visibleIDs.includes(failedTargets[0].id) && !visibleIDs.includes('retired-bulk-0'), 'partial failure notice retains failed targets and removes completed targets');
    const retry = bulk.page.getByRole('button', { name: 'Remove all delisted', exact: true });
    check(await bulk.page.locator('#modelSyncModal .sync-body').evaluate(body => body.getBoundingClientRect().height > 0), 'partial batch errors keep the failed model list visible');
    check(await retry.evaluate(button => {
      const rect = button.getBoundingClientRect();
      const top = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
      return rect.x >= 0 && rect.y >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight && !!top && !!top.closest('#modelSyncModal');
    }), 'partial batch errors keep the retry button visible and clickable');
    await retry.click();
    await bulk.page.locator('#modelSyncOverlay').waitFor({ state: 'hidden' });
    check(!bulkState.models.some(model => model.delisted), 'retry removes the remaining failed targets');
    await bulk.page.reload({ waitUntil: 'networkidle' });
    check(await bulk.page.locator('#modelSyncOverlay').isHidden(), '501-model cleanup stays acknowledged after reload');
    result.cases += 2;
  } finally { await bulk.context.close(); }
  check(result.forbiddenWrites.length === 0, 'No configuration or import writes');
  check(result.diagnostics === 0, 'No browser errors');
  console.log(JSON.stringify(result));
} catch (error) {
  result.failure = error.message;
  console.error(JSON.stringify({ ...result, failure: error.message }));
  process.exitCode = 1;
} finally {
  if (browser) await browser.close();
  if (server.pid && server.exitCode === null) {
    const stopped = new Promise(resolve => server.once('exit', resolve));
    server.kill('SIGTERM');
    await stopped;
  }
  writeFileSync(join(artifactDir, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  rmSync(dataDir, { recursive: true, force: true });
}

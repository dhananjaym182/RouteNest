// Run against /tmp/routenest-provider-model-isolation-verification. Every
// persisted file and upstream request belongs to this disposable local fixture.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer as createHTTPServer } from 'node:http';
import { createRequire } from 'node:module';
import { createServer as createTCPServer } from 'node:net';
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
const binary = process.env.ROUTENEST_TEST_BINARY || '/tmp/routenest-provider-model-isolation-verification';
if (!existsSync(binary)) throw new Error('The isolated verification binary is required: ' + binary);

const providerID = 'antseed_primary';
const sharedID = 'deepseek-4-1';
const rawAtID = 'vendor/shared@reasoning';
const longID = 'vendor/' + 'long-model-family-'.repeat(4) + 'shared@reasoning';
const modelIDs = [sharedID, rawAtID, longID];
const result = {
  checks: 0, cases: 0, forbiddenWrites: [], browserErrors: [],
  upstreamRequests: [], savePayloads: [], serverTestResults: [],
  modelNameProjections: [], clipboardCopies: [], pageLayouts: [],
};
const check = (value, label) => { assert.ok(value, label); result.checks++; };
const identities = entries => entries.map(entry => ({
  model: entry.model, source: entry.source || '', provider: entry.provider || '', disabled: !!entry.disabled,
}));
const equal = (actual, expected, label) => { assert.deepEqual(actual, expected, label); result.checks++; };
const report = () => JSON.stringify({
  ...result, upstreamRequests: result.upstreamRequests.length,
  savePayloads: result.savePayloads.length, serverTestResults: result.serverTestResults.length,
  modelNameProjections: result.modelNameProjections.length,
  clipboardCopies: result.clipboardCopies.length, pageLayouts: result.pageLayouts.length,
});
const expected = [
  { model: sharedID, source: 'zen', provider: '', disabled: false },
  { model: sharedID, source: 'provider', provider: providerID, disabled: false },
  { model: rawAtID, source: 'provider', provider: providerID, disabled: false },
  { model: rawAtID, source: 'zen', provider: '', disabled: false },
];
const listen = server => new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(0, '127.0.0.1', () => { server.removeListener('error', reject); resolve(server.address().port); });
});
const upstream = createHTTPServer(async (request, response) => {
  try {
    const path = new URL(request.url, 'http://127.0.0.1').pathname;
    let text = '';
    for await (const chunk of request) text += chunk;
    const body = text ? JSON.parse(text) : {};
    result.upstreamRequests.push({ method: request.method, path, model: body.model || '' });
    response.setHeader('Content-Type', 'application/json');
    if (request.method === 'GET' && path.endsWith('/models')) {
      response.end(JSON.stringify({ data: modelIDs.map(id => ({ id, pricing: { prompt: '0', completion: '0' } })) }));
    } else if (request.method === 'POST' && path.endsWith('/chat/completions') && body.model && modelIDs.includes(body.model)) {
      if (path.startsWith('/opencode/')) {
        response.writeHead(400);
        response.end(JSON.stringify({ error: {
          code: 'fixture_probe_failure', type: 'invalid_request_error',
          message: 'Fixture OpenCode probe rejected; this deliberately long failure verifies row wrapping and provider identity.',
        } }));
      } else if (path.startsWith('/antseed/')) {
        response.end(JSON.stringify({
          id: 'fixture-completion', object: 'chat.completion', model: body.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'Fixture AntSeed answer' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 2, completion_tokens: 3, total_tokens: 5 },
        }));
      } else throw new Error('Unexpected fixture upstream: ' + path);
    } else throw new Error('Unexpected fixture request: ' + request.method + ' ' + path);
  } catch (error) {
    result.forbiddenWrites.push('upstream: ' + error.message);
    if (!response.headersSent) response.writeHead(400, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ error: { message: error.message } }));
  }
});
const upstreamPort = await listen(upstream);
assert.notEqual(upstreamPort, 3457, 'Fixture upstream must never use the live port');
const socket = createTCPServer();
const port = await listen(socket);
await new Promise(resolve => socket.close(resolve));
assert.notEqual(port, 3457, 'Verification must never use the live service port');
const dataDir = mkdtempSync(join(tmpdir(), 'routenest-combo-isolation-data-'));
const artifactDir = mkdtempSync(join(tmpdir(), 'routenest-combo-isolation-browser-'));
result.artifacts = artifactDir;
const seed = (name, value) => writeFileSync(join(dataDir, name), JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
seed('.routenest-config.json', {
  strategy: 'round_robin', dispatchMode: 'enforce', onlyFree: false, combos: [],
  providerModelPrefixes: { cl: 'cline', op: 'opencode', as: providerID },
});
seed('.routenest-providers.json', [{
  id: providerID, name: 'AntSeed', baseURL: 'http://127.0.0.1:' + upstreamPort + '/antseed/v1',
  apiKey: 'fixture-loopback-key', modelIds: modelIDs, enabled: true, priority: 10,
  free: true, timeoutSec: 3, proxyUrl: 'direct', createdAt: new Date().toISOString(),
}]);
seed('.routenest-accounts.json', {
  accounts: [], keys: [], currentIdx: 0, modelCatalogInitialized: true,
  // Zen's ID-based sync preserves explicit administrator tier overrides. This
  // keeps arbitrary shared and literal @ fixture IDs eligible for free routing.
  models: modelIDs.map(id => ({
    id, provider: 'opencode', source: 'zen', cost: 'free', status: 'active', custom: false,
    tierSignal: 'manual', tierDecidedAt: Math.floor(Date.now() / 1000),
  })),
});
seed('.routenest-zen.json', {
  enabled: true, key: 'fixture-loopback-key', baseURL: 'http://127.0.0.1:' + upstreamPort + '/opencode/v1',
  retries: 0, failover: false, maxConcurrency: 2, sessionAutoHarvest: false,
  proxies: [], proxyStrategy: 'round_robin', compaction: { auto: false },
});
const bootstrapSecret = randomBytes(24).toString('base64url');
const testPassword = randomBytes(6).toString('base64url');
const serverEnv = { ...process.env, ROUTENEST_DATA_DIR: dataDir, ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD: bootstrapSecret };
delete serverEnv.CLINE2API_DATA_DIR;
const server = spawn(binary, ['--host', '127.0.0.1', '--port', String(port)], { env: serverEnv, stdio: 'ignore' });
let startupError, browser, storageState;
server.on('error', error => { startupError = error; });
const base = 'http://127.0.0.1:' + port + '/admin/';
const apiURL = path => new URL('api/' + path, base).href;

async function getCombos(page) {
  const response = await page.request.get(apiURL('combos'));
  check(response.status() === 200, 'real combo catalogue is readable');
  return (await response.json()).data;
}

async function save(page, button, label) {
  const [response] = await Promise.all([
    page.waitForResponse(response => response.url() === apiURL('combos/save') && response.request().method() === 'POST'),
    page.locator(button).click(),
  ]);
  const body = await response.json();
  if (response.status() !== 200) result.failedSaveResponse = { status: response.status(), body };
  check(response.status() === 200, label + ': real combo save succeeds' + (body.error ? ': ' + body.error : ''));
  check(body.success === true && !!body.data.combo, label + ': real server returns the saved combo');
  await page.waitForLoadState('networkidle');
  return body.data.combo;
}

async function shot(page, name, panel) {
  await page.screenshot({ path: join(artifactDir, name + '.png'), animations: 'disabled' });
  const layout = await page.locator(panel).evaluate(element => {
    const rect = element.getBoundingClientRect();
    return {
      contained: rect.x >= -1 && rect.y >= -1 && rect.right <= innerWidth + 1 && rect.bottom <= innerHeight + 1,
      noHorizontalOverflow: element.scrollWidth <= element.clientWidth + 1,
      pageFits: document.documentElement.scrollWidth <= innerWidth + 1,
    };
  });
  check(layout.contained, name + ': panel fits the viewport');
  check(layout.noHorizontalOverflow && layout.pageFits, name + ': no horizontal overflow');
}

async function pageShot(page, name, panel) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: join(artifactDir, name + '.png'), animations: 'disabled', fullPage: true });
  const layout = await page.locator(panel).evaluate(element => {
    const rect = element.getBoundingClientRect();
    return {
      horizontallyContained: rect.x >= -1 && rect.right <= innerWidth + 1,
      noHorizontalOverflow: element.scrollWidth <= element.clientWidth + 1,
      pageFits: document.documentElement.scrollWidth <= innerWidth + 1,
    };
  });
  result.pageLayouts.push({ name, ...layout });
  check(layout.horizontallyContained, name + ': page panel fits the viewport width');
  check(layout.noHorizontalOverflow && layout.pageFits, name + ': no page horizontal overflow');
}

function providerNameButton(page, container, qualifiedID) {
  return container.locator('.model-provider-name').filter({ has: page.getByText(qualifiedID, { exact: true }) });
}

async function checkClipboard(page, button, qualifiedID, label) {
  check(await button.count() === 1, label + ': one copy button for ' + qualifiedID);
  await page.evaluate(value => navigator.clipboard.writeText(value), 'clipboard before ' + label);
  await button.click();
  try {
    await page.waitForFunction(async expectedName => (await navigator.clipboard.readText()) === expectedName, qualifiedID, { timeout: 5000 });
  } catch (error) {
    const actual = await page.evaluate(() => navigator.clipboard.readText());
    throw new Error(label + ': expected clipboard ' + qualifiedID + ', received ' + actual, { cause: error });
  }
  const copied = await page.evaluate(() => navigator.clipboard.readText());
  equal(copied, qualifiedID, label + ': real clipboard preserves the exact qualified model name');
  result.clipboardCopies.push({ label, qualifiedID, copied });
}

async function checkNameWrapping(button, label) {
  const layout = await button.locator('code').evaluate(element => {
    const range = document.createRange();
    range.selectNodeContents(element);
    const lines = new Set(Array.from(range.getClientRects(), rect => Math.round(rect.top)));
    const button = element.closest('button');
    return { lines: lines.size, noOverflow: button.scrollWidth <= button.clientWidth + 1 };
  });
  check(layout.lines > 1, label + ': long qualified model name wraps');
  check(layout.noOverflow, label + ': wrapped name fits its copy button');
}

async function checkModelNames(page, suffix) {
  await page.goto(base + '#models', { waitUntil: 'networkidle' });
  await page.locator('#tab-models').waitFor({ state: 'visible' });
  await page.locator('#modelCatalogTab').click();
  await page.locator('#modelsList .model-provider-name').first().waitFor({ state: 'attached' });
  const response = await page.request.get(apiURL('model-offerings'));
  check(response.status() === 200, suffix + ': real model-offerings projection is readable');
  const offerings = (await response.json()).data.offerings;
  const fixtureOfferings = offerings.filter(offering => modelIDs.includes(offering.modelId));
  result.modelNameProjections.push({ suffix, offerings: fixtureOfferings });
  for (const id of modelIDs) for (const provider of [
    { id: providerID, prefix: 'as' }, { id: 'opencode', prefix: 'op' },
  ]) {
    const matches = fixtureOfferings.filter(offering => offering.modelId === id && offering.providerId === provider.id);
    check(matches.length === 1, suffix + ': one API offering for ' + provider.prefix + '/' + id);
    equal(matches[0].qualifiedId, provider.prefix + '/' + id, suffix + ': API exposes the qualified provider model name');
    equal(matches[0].upstreamModelId, id, suffix + ': API preserves the raw upstream model ID');
    check(!!matches[0].providerName && matches[0].providerName !== matches[0].providerId, suffix + ': API exposes a readable provider name');
  }

  const catalogRows = page.locator('#modelsList .model-row');
  const sharedRow = catalogRows.filter({ has: page.getByText(sharedID, { exact: true }) });
  const sharedGroup = page.locator('#modelsList .model-group-src').filter({ has: page.getByText(sharedID, { exact: true }) });
  const groupHead = sharedGroup.locator('.model-group-head');
  if (await groupHead.evaluate(element => element.classList.contains('expanded'))) await groupHead.locator('.model-group-label').click();
  check(await sharedRow.isHidden(), suffix + ': catalog group is collapsed before alias search');
  await page.locator('#modelSearch').fill('as/');
  await sharedRow.waitFor({ state: 'visible' });
  equal((await catalogRows.locator('.model-row-id').allTextContents()).sort(), modelIDs.slice().sort(), suffix + ': catalog prefix search finds each logical fixture model');
  check(await page.locator('#modelsList .model-group-head:not(.expanded)').count() === 0, suffix + ': alias search expands matching collapsed groups');
  result.cases++;

  for (const id of modelIDs) {
    const row = catalogRows.filter({ has: page.getByText(id, { exact: true }) });
    check(await row.count() === 1, suffix + ': catalog has one logical row for ' + id);
    equal(await row.locator('.model-provider-names > .hint-xs').innerText(), 'Provider model names', suffix + ': catalog labels the provider model names');
    const names = fixtureOfferings.filter(offering => offering.modelId === id).map(offering => offering.qualifiedId).sort();
    equal((await row.locator('.model-provider-name code').allTextContents()).sort(), names, suffix + ': catalog displays the API qualified names for ' + id);
    check(names.includes('as/' + id) && names.includes('op/' + id), suffix + ': logical row exposes both same-ID provider names');
    for (const name of names) {
      const button = providerNameButton(page, row, name);
      await checkClipboard(page, button, name, 'catalog-' + suffix + '-' + name);
      if (id === longID) await checkNameWrapping(button, 'catalog-' + suffix + '-' + name);
    }
  }
  await pageShot(page, 'catalog-model-names-' + suffix, '#modelCatalogView');
  await page.locator('#modelSearch').fill('as/' + rawAtID);
  equal(await catalogRows.locator('.model-row-id').allTextContents(), [rawAtID], suffix + ': catalog searches the exact qualified literal @ model');
  await page.locator('#modelSearch').fill('AntSeed');
  equal((await catalogRows.locator('.model-row-id').allTextContents()).sort(), modelIDs.slice().sort(), suffix + ': catalog also searches the provider name');
  result.cases++;

  await page.locator('#modelOfferingsTab').click();
  await page.locator('#modelOfferingsView').waitFor({ state: 'visible' });
  if (offerings.length > 12) await page.locator('#globalOfferingsToggle').click();
  const offeringRows = page.locator('#globalOfferings tbody tr');
  equal(await page.locator('#globalOfferings th').first().innerText(), 'Client model name', suffix + ': offerings first column identifies client model names');
  for (const offering of fixtureOfferings) {
    const row = offeringRows.filter({ has: page.getByText(offering.qualifiedId, { exact: true }) });
    check(await row.count() === 1, suffix + ': offerings has one row for ' + offering.qualifiedId);
    equal(await row.locator('td').first().locator('code').innerText(), offering.qualifiedId, suffix + ': offerings first column displays the API qualified ID');
    equal(await row.locator('td').nth(1).locator('.provider-byline').innerText(), offering.providerName, suffix + ': offerings displays the API provider name');
    equal(await row.locator('td').nth(2).innerText(), offering.upstreamModelId, suffix + ': offerings keeps the raw upstream ID in its own column');
    const button = providerNameButton(page, row, offering.qualifiedId);
    await checkClipboard(page, button, offering.qualifiedId, 'offerings-' + suffix + '-' + offering.qualifiedId);
    if (offering.modelId === longID) await checkNameWrapping(button, 'offerings-' + suffix + '-' + offering.qualifiedId);
  }
  await pageShot(page, 'offerings-model-names-' + suffix, '#modelOfferingsView');
  result.cases++;

  await page.locator('#globalOfferingSearch').fill('as/');
  equal((await offeringRows.locator('td:first-child code').allTextContents()).sort(), modelIDs.map(id => 'as/' + id).sort(), suffix + ': offerings prefix search isolates AntSeed');
  await page.locator('#globalOfferingSearch').fill('as/' + sharedID);
  equal(await offeringRows.locator('td:first-child code').allTextContents(), ['as/' + sharedID], suffix + ': qualified shared-ID search excludes the OpenCode offering');
  await pageShot(page, 'offerings-provider-search-' + suffix, '#modelOfferingsView');
  await page.locator('#globalOfferingSearch').fill('op/' + sharedID);
  equal(await offeringRows.locator('td:first-child code').allTextContents(), ['op/' + sharedID], suffix + ': OpenCode qualified search excludes the AntSeed offering');
  await page.locator('#globalOfferingSearch').fill(sharedID);
  equal((await offeringRows.locator('td:first-child code').allTextContents()).sort(), ['as/' + sharedID, 'op/' + sharedID], suffix + ': raw upstream search retains both provider offerings');
  await page.locator('#globalOfferingSearch').fill('as/' + rawAtID);
  equal(await offeringRows.locator('td:first-child code').allTextContents(), ['as/' + rawAtID], suffix + ': offerings searches the exact qualified literal @ model');
  result.cases++;
}

async function selectedLabels(page) {
  return page.locator('#comboEntries .combo-entry-select select option:checked').allTextContents();
}

async function checkTestRows(page, labels, enabled) {
  const rows = page.locator('#comboTestList .combo-test-row');
  check(await rows.count() === labels.length, 'test rows retain each offering');
  for (let index = 0; index < labels.length; index++) {
    const row = rows.nth(index);
    check(await row.locator('.combo-pick-id').innerText() === labels[index], 'test row ' + index + ': qualified identity');
    check(await row.locator('input[type="checkbox"]').isChecked() === enabled[index], 'test row ' + index + ': enabled state');
    if (labels[index].startsWith('as/') && enabled[index]) {
      check(await row.locator('.combo-test-error').count() === 0, 'AntSeed row retains its successful result');
      check((await row.locator('.combo-test-verdict').innerText()).endsWith('ms'), 'AntSeed row retains its timing');
      check((await row.innerText()).includes('AntSeed'), 'AntSeed row retains its provider badge');
    }
  }
}

async function checkSummary(page, failures, label) {
  const summary = page.locator('#comboTestSummary');
  check(await summary.isVisible(), label + ': active summary is visible');
  equal(await summary.innerText(), failures ? failures + ' ✗ Failed' : '✓ All passed', label + ': summary counts active working-copy results');
}

try {
  let ready = false;
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    if (startupError) throw startupError;
    if (server.exitCode !== null) throw new Error('Isolated server exited before startup');
    try {
      const response = await fetch(apiURL('auth/status'), { signal: AbortSignal.timeout(1000) });
      if (response.ok && (await response.json()).success === true) { ready = true; break; }
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  check(ready, 'isolated server is HTTP-ready');
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'],
    env: { ...process.env, FONTCONFIG_FILE: process.env.FONTCONFIG_FILE || '/etc/fonts/fonts.conf' },
  });
  const setup = await browser.newContext();
  try {
    const response = await setup.request.post(apiURL('auth/bootstrap'), {
      headers: { Origin: new URL(base).origin }, data: { secret: bootstrapSecret, password: testPassword },
    });
    check(response.status() === 200, 'temporary administrator initialized');
    storageState = await setup.storageState();
  } finally { await setup.close(); }

  for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
    const suffix = theme + '-' + width, comboID = 'verify-isolation-' + suffix;
    const context = await browser.newContext({ viewport: { width, height: 900 }, storageState });
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(base).origin });
    await context.addInitScript(theme => {
      localStorage.setItem('cline_admin_theme', theme);
      localStorage.setItem('cline_admin_lang', 'en');
      localStorage.setItem('cline_admin_quickstart_seen', '1');
    }, theme);
    await context.route('**/admin/api/**', async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname.replace('/admin/api/', '');
      if (request.method() === 'GET' && path === 'models') {
        // Keep the real model records. This slice does not test the unrelated
        // delayed Cline first-run sync notice; its summary is fixture-only.
        const response = await route.fetch();
        const body = await response.json();
        body.data.lastSync = {};
        await route.fulfill({ response, json: body });
        return;
      }
      if (['GET', 'HEAD', 'OPTIONS'].includes(request.method())) { await route.continue(); return; }
      const body = request.postDataJSON();
      if (request.method() === 'POST' && ['combos/save', 'combos/test'].includes(path) && body.id === comboID) {
        if (path === 'combos/save') result.savePayloads.push(body);
        await route.continue();
        return;
      }
      result.forbiddenWrites.push(request.method() + ' ' + path);
      await route.fulfill({ status: 400, json: { success: false, error: 'Unexpected isolated verification write' } });
    });
    const page = await context.newPage();
    page.on('pageerror', error => result.browserErrors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') result.browserErrors.push(message.text()); });
    try {
      await checkModelNames(page, suffix);
      await page.goto(base + '#combos', { waitUntil: 'networkidle' });
      await page.locator('#tab-combos').waitFor({ state: 'visible' });
      await page.evaluate(() => loadCombos());
      const catalog = (await getCombos(page)).modelCatalog;
      for (const id of modelIDs) {
        check(catalog.some(model => model.id === id && model.providerId === providerID && model.qualifiedId === 'as/' + id && !model.disabled), 'AntSeed catalogue preserves raw ID and stable provider');
        check(catalog.some(model => model.id === id && model.providerId === 'opencode' && model.qualifiedId === 'op/' + id && !model.disabled), 'OpenCode catalogue preserves the same raw ID separately');
        check(catalog.some(model => model.id === id && model.providerId === 'opencode' && model.cost === 'free'), 'isolated OpenCode fixture has explicit free eligibility');
      }
      const startupNotice = page.locator('#modelSyncOverlay');
      if (await startupNotice.isVisible()) {
        await startupNotice.getByRole('button', { name: 'OK', exact: true }).click();
        await startupNotice.waitFor({ state: 'hidden' });
      }
      await page.evaluate(() => showComboEditor(null));
      await page.locator('#comboId').fill(comboID);
      await page.locator('#comboName').fill('Provider identity ' + suffix);
      await page.locator('#comboEditor [onclick="addComboEntry()"]').click();
      const nativeModel = page.locator('#comboEntries .combo-entry-select select').first();
      const antseedValue = await nativeModel.evaluate((select, id) => Array.from(select.options).find(option => option.textContent.startsWith('as/' + id))?.value, sharedID);
      check(!!antseedValue, 'native dropdown offers the qualified AntSeed model');
      await nativeModel.selectOption(antseedValue);
      check((await selectedLabels(page))[0].startsWith('as/' + sharedID), 'native model selection binds AntSeed');
      const nativeSource = page.locator('#comboEntries .combo-entry-source select').first();
      await nativeSource.selectOption({ label: 'OpenCode Zen' });
      check((await selectedLabels(page))[0].startsWith('op/' + sharedID), 'native upstream switch binds the same raw ID to OpenCode');
      await nativeSource.selectOption({ label: 'AntSeed' });
      check((await selectedLabels(page))[0].startsWith('as/' + sharedID), 'native upstream switch restores the stable AntSeed pin');
      await page.locator('#comboEditor [onclick="openComboModelPicker()"]').click();
      const existingAntseed = page.locator('#comboPickerList .combo-pick-row').filter({ has: page.getByText('as/' + sharedID, { exact: true }) });
      check(await existingAntseed.locator('input[type="checkbox"]').isDisabled(), 'picker protects only the existing AntSeed offering');
      for (const label of ['op/' + sharedID, 'as/' + rawAtID, 'op/' + rawAtID]) {
        const row = page.locator('#comboPickerList .combo-pick-row').filter({ has: page.getByText(label, { exact: true }) });
        check(await row.count() === 1, label + ': one selectable offering');
        await row.locator('input[type="checkbox"]').check();
      }
      check((await page.locator('#comboPickerCount').innerText()).includes('3'), 'picker counts newly selected offerings independently');
      await shot(page, 'picker-' + suffix, '#comboModelPicker .modal-card');
      await page.locator('#comboModelPicker [onclick="comboPickerConfirm(false)"]').click();
      equal((await selectedLabels(page)).map(label => label.split('  ·  ')[0]), ['as/' + sharedID, 'op/' + sharedID, 'as/' + rawAtID, 'op/' + rawAtID], 'real dropdowns show qualified models');
      result.cases++;

      const upstreamLabels = await page.locator('#comboEntries .combo-entry-source select').first().locator('option').allTextContents();
      check(upstreamLabels.includes('AntSeed') && upstreamLabels.includes('OpenCode Zen'), 'upstream dropdown names both same-ID providers');
      await page.locator('#comboEntries [onclick="moveComboEntry(1, -1)"]').click();
      equal((await selectedLabels(page)).map(label => label.split('  ·  ')[0]), ['op/' + sharedID, 'as/' + sharedID, 'as/' + rawAtID, 'op/' + rawAtID], 'real reorder retains each offering identity');
      await shot(page, 'reordered-' + suffix, '#comboEditor');
      result.cases++;

      const saved = await save(page, '#comboEditor [onclick="saveCombo()"]', suffix);
      equal(identities(saved.entries), expected, 'real save keeps raw IDs and stable provider fields');
      equal(identities(result.savePayloads.at(-1).entries), expected, 'browser save payload keeps raw IDs and stable provider fields');
      const persisted = JSON.parse(readFileSync(join(dataDir, '.routenest-config.json'), 'utf8'));
      equal(identities(persisted.combos.find(combo => combo.id === comboID).entries), expected, 'disk persistence keeps literal @ IDs and provider pins');
      await page.reload({ waitUntil: 'networkidle' });
      await page.locator('#combosList tr').filter({ has: page.getByText(comboID, { exact: true }) }).locator('button[onclick^="editCombo("]').click();
      equal((await selectedLabels(page)).map(label => label.split('  ·  ')[0]), ['op/' + sharedID, 'as/' + sharedID, 'as/' + rawAtID, 'op/' + rawAtID], 'reload keeps qualified dropdown identities');
      await page.evaluate(() => hideComboEditor());
      result.cases++;

      const testResponse = page.waitForResponse(response => response.url() === apiURL('combos/test') && response.request().method() === 'POST');
      const savedRow = page.locator('#combosList tr').filter({ has: page.getByText(comboID, { exact: true }) });
      await savedRow.locator('button[onclick^="testCombo("]').click();
      const test = await testResponse;
      check(test.status() === 200, 'real combo test completes against loopback upstreams');
      const testData = (await test.json()).data;
      result.serverTestResults.push(testData);
      equal(identities(testData.results), expected, 'server test fields preserve raw model and stable provider identity');
      await page.locator('#comboTestModal').waitFor({ state: 'visible' });
      const labels = ['op/' + sharedID, 'as/' + sharedID, 'as/' + rawAtID, 'op/' + rawAtID];
      await checkTestRows(page, labels, [true, true, true, true]);
      await checkSummary(page, 2, suffix + ' initial results');
      check(await page.locator('#comboTestList .combo-test-row').first().locator('.combo-test-error').count() === 1, 'first OpenCode failure stays on its own row');
      await shot(page, 'test-original-' + suffix, '#comboTestModal .modal-card');
      result.cases++;

      await page.locator('#comboTestList .combo-test-row').first().locator('label.switch').click();
      await checkTestRows(page, labels, [false, true, true, true]);
      await checkSummary(page, 1, suffix + ' first entry disabled');
      check(await page.locator('#comboTestList .combo-test-row').first().locator('.combo-test-error').count() === 0, 'disabled first OpenCode row has no active result');
      await shot(page, 'test-disabled-first-' + suffix, '#comboTestModal .modal-card');
      const lastRow = page.locator('#comboTestList .combo-test-row').nth(3);
      check(await lastRow.locator('.combo-test-error').count() === 1, 'same-ID OpenCode @ failure retains its own result');
      await lastRow.locator('button[onclick="comboTestRemoveEntry(3)"]').click();
      await checkTestRows(page, labels.slice(0, 3), [false, true, true]);
      await checkSummary(page, 0, suffix + ' failed offering removed');
      check(await page.locator('#comboTestList .combo-test-error').count() === 0, 'individual cleanup preserves successful AntSeed rows');
      await shot(page, 'test-cleanup-' + suffix, '#comboTestModal .modal-card');
      result.cases++;

      const modifiedExpected = expected.slice(0, 3).map((entry, index) => ({ ...entry, disabled: index === 0 }));
      const modified = await save(page, '#comboTestSaveBtn', suffix + ' result cleanup');
      equal(identities(modified.entries), modifiedExpected, 'real cleanup save disables only OpenCode and removes only its failed @ offering');
      const fromServer = (await getCombos(page)).combos.find(combo => combo.id === comboID);
      equal(identities(fromServer.entries), modifiedExpected, 'server reload preserves individual cleanup and stable AntSeed fields');
      await page.reload({ waitUntil: 'networkidle' });
      const retestResponse = page.waitForResponse(response => response.url() === apiURL('combos/test') && response.request().method() === 'POST');
      await page.locator('#combosList tr').filter({ has: page.getByText(comboID, { exact: true }) }).locator('button[onclick^="testCombo("]').click();
      const retest = await retestResponse;
      check(retest.status() === 200, 'real retest skips the disabled first OpenCode entry');
      const retestData = (await retest.json()).data;
      result.serverTestResults.push(retestData);
      equal(identities(retestData.results), expected.slice(1, 3), 'retest reports only the enabled AntSeed offerings');
      await page.locator('#comboTestModal').waitFor({ state: 'visible' });
      await checkTestRows(page, labels.slice(0, 3), [false, true, true]);
      await checkSummary(page, 0, suffix + ' saved cleanup reloaded and retested');
      await shot(page, 'test-reloaded-cleanup-' + suffix, '#comboTestModal .modal-card');
      result.cases++;
    } catch (error) {
      await page.screenshot({ path: join(artifactDir, 'failure-' + suffix + '.png'), animations: 'disabled' }).catch(() => {});
      throw error;
    } finally { await context.close(); }
  }
  check(result.upstreamRequests.some(request => request.path.startsWith('/antseed/') && request.model === rawAtID), 'AntSeed receives the literal raw @ ID');
  check(result.upstreamRequests.some(request => request.path.startsWith('/opencode/') && request.model === rawAtID), 'OpenCode receives the same literal raw @ ID');
  check(result.forbiddenWrites.length === 0, 'no unintended writes or upstream fixture requests');
  check(result.browserErrors.length === 0, 'no browser errors');
  console.log(report());
} catch (error) {
  result.failure = error.message;
  console.error(report());
  process.exitCode = 1;
} finally {
  if (browser) await browser.close();
  if (server.pid && server.exitCode === null) {
    const stopped = new Promise(resolve => server.once('exit', resolve));
    server.kill('SIGTERM');
    const force = setTimeout(() => server.kill('SIGKILL'), 5000);
    await stopped;
    clearTimeout(force);
  }
  upstream.closeAllConnections();
  await new Promise(resolve => upstream.close(resolve));
  writeFileSync(join(artifactDir, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  rmSync(dataDir, { recursive: true, force: true });
}

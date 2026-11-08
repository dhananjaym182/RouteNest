// Run against /tmp/routenest-provider-model-isolation-verification. Every
// persisted file and upstream request belongs to this disposable local fixture.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { appendFileSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
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
const verificationSlice = process.env.ROUTENEST_VERIFY_SLICE || 'all';
assert.ok(['all', 'management', 'viewer'].includes(verificationSlice), 'Verification slice must be all, management or viewer');

const providerID = 'antseed_primary';
const sharedID = 'deepseek-4-1';
const rawAtID = 'vendor/shared@reasoning';
const longID = 'vendor/' + 'long-model-family-'.repeat(4) + 'shared@reasoning';
const modelIDs = [sharedID, rawAtID, longID];
const removalSharedID = 'org/shared-model@revision';
const removalBulkIDs = ['fixture/removal-bulk-alpha', 'fixture/removal-bulk-beta'];
const removalKeepID = 'fixture/removal-keep';
const removalZenOnlyID = 'fixture/zen-only@reasoning';
const removalTwinID = 'fixture/twin-model@reasoning';
const removalTwinKeepID = 'fixture/twin-keep';
const siblingProviderID = 'provider_sibling';
const twinProviderIDs = ['provider_twin_a', 'provider_twin_b'];
const discoveryCatalogs = new Map();
const result = {
  slice: verificationSlice,
  checks: 0, cases: 0, forbiddenWrites: [], browserErrors: [],
  upstreamRequests: [], savePayloads: [], serverTestResults: [],
  modelNameProjections: [], clipboardCopies: [], pageLayouts: [],
  denseRowLayouts: [], removalPayloads: [], removalResults: [], removalFixtures: [], permissionProbes: [],
  acknowledgedNotices: [],
  browserHTTPFailures: [],
  browserErrorDetails: [], requestFailures: [], fixtureLogs: [],
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
  denseRowLayouts: result.denseRowLayouts.length, removalPayloads: result.removalPayloads.length,
  removalResults: result.removalResults.length, permissionProbes: result.permissionProbes.length,
  acknowledgedNotices: result.acknowledgedNotices.length,
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
      const ids = discoveryCatalogs.get(path) || modelIDs;
      response.end(JSON.stringify({ data: ids.map(id => ({ id, pricing: { prompt: '0', completion: '0' } })) }));
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
const server = spawn(binary, ['--host', '127.0.0.1', '--port', String(port)], { env: serverEnv, stdio: ['ignore', 'pipe', 'pipe'] });
captureFixtureLogs(server, 'main');
let startupError, browser, storageState;
server.on('error', error => { startupError = error; });
const base = 'http://127.0.0.1:' + port + '/admin/';
const apiURL = path => new URL('api/' + path, base).href;

function captureFixtureLogs(child, label) {
  for (const stream of ['stdout', 'stderr']) {
    const path = join(artifactDir, 'fixture-' + label + '-' + stream + '.log');
    result.fixtureLogs.push({ label, stream, path });
    child[stream].on('data', data => appendFileSync(path, data, { mode: 0o600 }));
  }
}

function captureBrowserEvidence(page, role, suffix, phase) {
  const starts = new WeakMap();
  const locationURL = value => {
    if (!value) return '';
    try { const url = new URL(value); return url.origin + url.pathname; } catch { return value; }
  };
  page.on('request', request => starts.set(request, { phase: phase(), at: Date.now() }));
  page.on('pageerror', error => {
    result.browserErrors.push(error.message);
    result.browserErrorDetails.push({ role, suffix, phase: phase(), type: 'pageerror', message: error.message });
  });
  page.on('console', message => {
    if (message.type() !== 'error') return;
    result.browserErrors.push(message.text());
    const location = message.location();
    result.browserErrorDetails.push({ role, suffix, phase: phase(), type: 'console', message: message.text(), location: { ...location, url: locationURL(location.url) } });
  });
  page.on('response', response => {
    if (response.status() >= 400) result.browserHTTPFailures.push({ role, suffix, phase: phase(), method: response.request().method(), url: locationURL(response.url()), status: response.status() });
  });
  page.on('requestfailed', request => {
    result.requestFailures.push({ role, suffix, phase: phase(), started: starts.get(request), method: request.method(), url: locationURL(request.url()), error: request.failure()?.errorText });
  });
}

async function stopFixtureServer(child) {
  if (!child.pid || child.exitCode !== null || child.signalCode !== null) return;
  const stopped = new Promise(resolve => child.once('exit', resolve));
  child.kill('SIGTERM');
  const force = setTimeout(() => child.kill('SIGKILL'), 5000);
  await stopped;
  clearTimeout(force);
}

async function startRemovalFixture(suffix) {
  const temporarySocket = createTCPServer();
  const fixturePort = await listen(temporarySocket);
  await new Promise(resolve => temporarySocket.close(resolve));
  assert.notEqual(fixturePort, 3457, 'Removal verification must never use the live service port');
  const fixtureDir = mkdtempSync(join(tmpdir(), 'routenest-model-removal-data-'));
  const fixtureBase = 'http://127.0.0.1:' + fixturePort + '/admin/';
  const fixtureAPI = path => new URL('api/' + path, fixtureBase).href;
  const fixtureOrigin = new URL(fixtureBase).origin;
  const fixtureSeed = (name, value) => writeFileSync(join(fixtureDir, name), JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
  const primaryIDs = [removalSharedID, longID, ...removalBulkIDs, removalKeepID];
  const fixtureProviders = [
    { id: providerID, name: 'AntSeed', modelIds: primaryIDs },
    { id: siblingProviderID, name: 'Sibling Provider', modelIds: [...primaryIDs, 'fixture/sibling-only'] },
    ...twinProviderIDs.map(id => ({ id, name: 'Twin Provider', modelIds: [removalTwinID, removalTwinKeepID] })),
  ].map(provider => ({
    ...provider, baseURL: 'http://127.0.0.1:' + upstreamPort + '/removal-' + suffix + '/' + provider.id + '/v1',
    apiKey: 'fixture-credential-' + provider.id, enabled: true, priority: 10, free: true,
    timeoutSec: 3, proxyUrl: 'direct', createdAt: new Date().toISOString(),
  }));
  for (const provider of fixtureProviders) discoveryCatalogs.set(new URL(provider.baseURL + '/models').pathname, provider.modelIds);
  const zenBaseURL = 'http://127.0.0.1:' + upstreamPort + '/removal-' + suffix + '/opencode/v1';
  discoveryCatalogs.set(new URL(zenBaseURL + '/models').pathname, [...primaryIDs, removalZenOnlyID]);
  const fixtureModel = (id, source, provider, custom = false) => ({
    id, provider, source, custom, cost: 'free', status: 'active', context: 200000, output: 32768,
    tierSignal: 'manual', tierDecidedAt: Math.floor(Date.now() / 1000),
  });
  fixtureSeed('.routenest-config.json', {
    strategy: 'round_robin', dispatchMode: 'enforce', onlyFree: false,
    providerModelPrefixes: { cl: 'cline', op: 'opencode', as: providerID, sy: siblingProviderID, ta: twinProviderIDs[0], tb: twinProviderIDs[1] },
    combos: [{
      id: 'removal-sibling-pins', name: 'Preserve other provider pins', strategy: 'priority', enabled: true,
      entries: [
        { model: removalSharedID, source: 'provider', provider: providerID },
        { model: removalSharedID, source: 'provider', provider: siblingProviderID },
        { model: removalSharedID, source: 'zen' },
        { model: removalSharedID, source: 'remote' },
        { model: removalSharedID },
      ],
    }, {
      id: 'removal-twin-pins', name: 'Preserve duplicate-name provider pins', strategy: 'priority', enabled: true,
      entries: twinProviderIDs.map(provider => ({ model: removalTwinID, source: 'provider', provider })),
    }],
  });
  fixtureSeed('.routenest-providers.json', fixtureProviders);
  fixtureSeed('.routenest-accounts.json', {
    accounts: [], keys: ['fixture-gateway-key'], currentIdx: 0, modelCatalogInitialized: true,
    models: [
      ...primaryIDs.map(id => fixtureModel(id, 'provider', 'AntSeed', id === removalSharedID)),
      ...primaryIDs.map(id => fixtureModel(id, 'zen', 'opencode')),
      ...primaryIDs.map(id => fixtureModel(id, 'remote', 'fixture-vendor')),
      fixtureModel(removalZenOnlyID, 'zen', 'opencode'),
      fixtureModel('fixture/sibling-only', 'provider', 'Sibling Provider'),
      fixtureModel(removalTwinID, 'provider', 'Twin Provider'),
      fixtureModel(removalTwinKeepID, 'provider', 'Twin Provider'),
    ],
  });
  fixtureSeed('.routenest-zen.json', {
    enabled: true, key: 'fixture-opencode-credential', baseURL: zenBaseURL, retries: 0, failover: false,
    maxConcurrency: 2, sessionAutoHarvest: false, proxies: [], proxyStrategy: 'round_robin', compaction: { auto: false },
  });
  const bootstrapPassword = randomBytes(24).toString('base64url');
  const fixturePassword = randomBytes(6).toString('base64url');
  const fixtureViewerPassword = randomBytes(6).toString('base64url');
  const fixtureEnv = { ...process.env, ROUTENEST_DATA_DIR: fixtureDir, ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD: bootstrapPassword };
  delete fixtureEnv.CLINE2API_DATA_DIR;
  const child = spawn(binary, ['--host', '127.0.0.1', '--port', String(fixturePort)], { env: fixtureEnv, stdio: ['ignore', 'pipe', 'pipe'] });
  captureFixtureLogs(child, 'management-' + suffix);
  let launchError;
  child.on('error', error => { launchError = error; });
  const metadata = { suffix, port: fixturePort, dataDir: fixtureDir, cleaned: false };
  result.removalFixtures.push(metadata);
  const close = async () => {
    await stopFixtureServer(child);
    rmSync(fixtureDir, { recursive: true, force: true });
    metadata.cleaned = !existsSync(fixtureDir);
  };
  try {
    let ready = false;
    const deadline = Date.now() + 20000;
    while (Date.now() < deadline) {
      if (launchError) throw launchError;
      if (child.exitCode !== null) throw new Error('Removal fixture exited before startup');
      try {
        const response = await fetch(fixtureAPI('auth/status'), { signal: AbortSignal.timeout(1000) });
        if (response.ok && (await response.json()).success) { ready = true; break; }
      } catch {}
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    check(ready, suffix + ': isolated removal server is HTTP-ready');
    const setup = await browser.newContext();
    let administratorState;
    try {
      const bootstrap = await setup.request.post(fixtureAPI('auth/bootstrap'), {
        headers: { Origin: fixtureOrigin }, data: { secret: bootstrapPassword, password: fixturePassword },
      });
      check(bootstrap.status() === 200, suffix + ': removal fixture administrator is initialized');
      const csrfCookie = (await setup.cookies()).find(cookie => cookie.name === 'routenest_admin_csrf');
      check(!!csrfCookie, suffix + ': removal administrator has a session CSRF cookie');
      const viewer = await setup.request.post(fixtureAPI('auth/users'), {
        headers: { Origin: fixtureOrigin, 'X-RouteNest-CSRF': decodeURIComponent(csrfCookie.value) },
        data: { username: 'fixture_viewer', password: fixtureViewerPassword, roles: ['viewer'] },
      });
      check(viewer.status() === 201, suffix + ': isolated viewer account is created for permission verification');
      administratorState = await setup.storageState();
    } finally { await setup.close(); }
    const viewerSetup = await browser.newContext();
    let viewerState, viewerCSRF;
    try {
      const login = await viewerSetup.request.post(fixtureAPI('login'), {
        headers: { Origin: fixtureOrigin }, data: { username: 'fixture_viewer', password: fixtureViewerPassword },
      });
      check(login.status() === 200, suffix + ': isolated viewer login succeeds');
      const csrfCookie = (await viewerSetup.cookies()).find(cookie => cookie.name === 'routenest_admin_csrf');
      check(!!csrfCookie, suffix + ': removal viewer has a session CSRF cookie');
      viewerCSRF = decodeURIComponent(csrfCookie.value);
      viewerState = await viewerSetup.storageState();
    } finally { await viewerSetup.close(); }
    return { suffix, base: fixtureBase, apiURL: fixtureAPI, origin: fixtureOrigin, dataDir: fixtureDir,
      primaryIDs, administratorState, viewerState, viewerCSRF, close };
  } catch (error) { await close(); throw error; }
}

async function getCombos(page) {
  const response = await page.request.get(apiURL('combos'));
  check(response.status() === 200, 'real combo catalogue is readable');
  return (await response.json()).data;
}

async function acknowledgeFixtureNotices(page, label) {
  await page.waitForFunction(() => typeof checkModelDelistings === 'function');
  await page.evaluate(() => checkModelDelistings());
  await page.waitForLoadState('networkidle');
  const overlay = page.locator('#modelSyncOverlay');
  for (let index = 0; index < 5 && await overlay.isVisible(); index++) {
    result.acknowledgedNotices.push({ label, title: await page.locator('#modelSyncTitle').innerText() });
    await overlay.getByRole('button', { name: 'OK', exact: true }).click();
    await page.waitForLoadState('networkidle');
  }
  check(await overlay.isHidden(), label + ': real fixture startup notices are acknowledged without removal');
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

async function checkCatalogDensity(row, id, suffix) {
  const layout = await row.evaluate(element => {
    const original = element.querySelector('.model-row-id');
    const names = element.querySelector('.model-provider-names');
    const badges = element.querySelector('.model-row-tags');
    const identity = element.querySelector('.model-row-names');
    const rowRect = element.getBoundingClientRect();
    const originalRect = original.getBoundingClientRect();
    const namesRect = names.getBoundingClientRect();
    const badgesRect = badges.getBoundingClientRect();
    const identityRect = identity.getBoundingClientRect();
    const centers = [originalRect, namesRect, badgesRect].map(rect => rect.y + rect.height / 2);
    const buttons = Array.from(names.querySelectorAll('.model-provider-name'));
    const visibleBadges = Array.from(badges.children).filter(badge => badge.getClientRects().length);
    return {
      width: innerWidth, height: rowRect.height, originalTitle: original.title,
      nameGroupWidth: namesRect.width, identityWidth: identityRect.width,
      originalBeforeNames: !!(original.compareDocumentPosition(names) & Node.DOCUMENT_POSITION_FOLLOWING),
      namesBeforeBadges: !!(names.compareDocumentPosition(badges) & Node.DOCUMENT_POSITION_FOLLOWING),
      oneLine: Math.max(...centers) - Math.min(...centers) <= 8,
      inlineOrder: originalRect.right <= namesRect.left + 1 && namesRect.right <= badgesRect.left + 1,
      noOverflow: element.scrollWidth <= element.clientWidth + 1,
      namesFitContainer: namesRect.x >= identityRect.x - 1 && namesRect.right <= identityRect.right + 1,
      badgesUnclipped: visibleBadges.every(badge => {
        const rect = badge.getBoundingClientRect();
        return rect.x >= badgesRect.x - 1 && rect.right <= badgesRect.right + 1 && rect.y >= rowRect.y - 1 && rect.bottom <= rowRect.bottom + 1;
      }),
      buttonSizes: buttons.map(button => {
        const rect = button.getBoundingClientRect();
        const prefix = button.querySelector('.model-provider-prefix');
        const prefixRect = prefix && prefix.getBoundingClientRect();
        return {
          width: rect.width, height: rect.height, title: button.title, name: button.querySelector('code').textContent,
          prefix: prefix && prefix.textContent,
          prefixVisible: !!prefixRect && prefixRect.x >= rect.x - 1 && prefixRect.right <= rect.right + 1 && prefixRect.x >= identityRect.x - 1 && prefixRect.right <= identityRect.right + 1 && prefix.scrollWidth <= prefix.clientWidth + 1,
          buttonContained: rect.x >= namesRect.x - 1 && rect.right <= namesRect.right + 1 && rect.x >= identityRect.x - 1 && rect.right <= identityRect.right + 1,
        };
      }),
    };
  });
  result.denseRowLayouts.push({ suffix, id, ...layout });
  equal(layout.originalTitle, id, suffix + ': original model tooltip preserves the complete raw ID');
  check(layout.originalBeforeNames && layout.namesBeforeBadges, suffix + ': logical row orders original ID, aliases, then badges');
  check(layout.noOverflow, suffix + ': dense logical row has no horizontal overflow');
  check(layout.namesFitContainer, suffix + ': alias group fits its bounded identity container');
  check(layout.badgesUnclipped, suffix + ': inline badges are fully visible without partial clipping');
  for (const button of layout.buttonSizes) {
    check(button.title.includes(button.name), suffix + ': alias tooltip preserves the complete qualified ID');
    check(button.width >= 20 && button.height >= 20, suffix + ': alias copy control remains usable');
    equal(button.prefix, button.name.slice(0, button.name.indexOf('/') + 1), suffix + ': alias exposes its complete provider prefix');
    check(button.prefixVisible && button.buttonContained, suffix + ': both provider prefixes remain visible in the compact model line');
  }
  if (layout.width >= 1000) {
    check(layout.oneLine && layout.inlineOrder, suffix + ': desktop original, aliases and badges occupy one model line');
    check(layout.height <= 72, suffix + ': desktop model row stays dense');
  }
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
    check(await row.getByText('Provider model names', { exact: true }).count() === 0, suffix + ': catalog omits the repeated provider-model caption');
    const names = fixtureOfferings.filter(offering => offering.modelId === id).map(offering => offering.qualifiedId).sort();
    equal((await row.locator('.model-provider-name code').allTextContents()).sort(), names, suffix + ': catalog displays the API qualified names for ' + id);
    check(names.includes('as/' + id) && names.includes('op/' + id), suffix + ': logical row exposes both same-ID provider names');
    await checkCatalogDensity(row, id, suffix);
    for (const name of names) {
      const button = providerNameButton(page, row, name);
      await checkClipboard(page, button, name, 'catalog-' + suffix + '-' + name);
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

const removalRefs = refs => refs.map(ref => ({ id: ref.id, source: ref.source, providerId: ref.providerId }))
  .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));

async function removalSnapshot(page, fixture) {
  const responses = await Promise.all(['model-offerings', 'credentials'].map(path => page.request.get(fixture.apiURL(path))));
  const bodies = await Promise.all(responses.map(response => response.json()));
  for (let index = 0; index < responses.length; index++) {
    check(responses[index].status() === 200 && bodies[index].success, fixture.suffix + ': removal baseline projection ' + index + ' is readable');
  }
  check(Array.isArray(bodies[0].data.offerings), fixture.suffix + ': offering baseline contains the actual model list');
  check(Array.isArray(bodies[1].data.credentials) && bodies[1].data.credentials.length >= 5, fixture.suffix + ': credential baseline includes OpenCode and every custom provider');
  const read = name => JSON.parse(readFileSync(join(fixture.dataDir, name), 'utf8'));
  return {
    config: read('.routenest-config.json'), providers: read('.routenest-providers.json'),
    accounts: read('.routenest-accounts.json'), zen: read('.routenest-zen.json'),
    offerings: bodies[0].data.offerings, credentials: bodies[1].data.credentials,
  };
}

function checkCancelledRemoval(before, after, label) {
  equal(after.config, before.config, label + ': cancellation preserves gateway configuration');
  equal(after.providers, before.providers, label + ': cancellation preserves provider declarations and credentials');
  equal(after.accounts, before.accounts, label + ': cancellation preserves all stored models and gateway keys');
  equal(after.zen, before.zen, label + ': cancellation preserves OpenCode settings and credential');
  equal(after.offerings, before.offerings, label + ': cancellation preserves all provider offerings');
  equal(after.credentials, before.credentials, label + ': cancellation preserves provider credential projections');
}

function checkScopedRemoval(before, after, refs, label) {
  const provider = refs[0].providerId;
  const ids = new Set(refs.map(ref => ref.id));
  const offeringKey = offering => offering.providerId + '\0' + offering.modelId;
  const afterOfferings = new Map(after.offerings.map(offering => [offeringKey(offering), offering]));
  for (const offering of before.offerings) {
    if (offering.providerId === provider && ids.has(offering.modelId)) {
      check(!afterOfferings.has(offeringKey(offering)), label + ': removed offering disappears only from its target provider');
    } else if (ids.has(offering.modelId)) {
      equal(afterOfferings.get(offeringKey(offering)), offering, label + ': same-ID offering from ' + offering.providerId + ' survives unchanged');
    }
  }
  for (const prior of before.providers) {
    const current = after.providers.find(item => item.id === prior.id);
    check(!!current, label + ': provider instance remains configured');
    if (prior.id !== provider) equal(current, prior, label + ': non-target provider declaration and credential are unchanged');
    else {
      const settings = ({ modelIds, updatedAt, ...rest }) => rest;
      equal(settings(current), settings(prior), label + ': target provider keeps credentials and non-model settings');
      equal(current.modelIds, prior.modelIds.filter(id => !ids.has(id)), label + ': only requested raw IDs leave the target provider declaration');
    }
  }
  equal(after.credentials, before.credentials, label + ': all provider credential projections are preserved');
  equal(after.zen, before.zen, label + ': OpenCode credential and non-model settings are preserved');
  equal(after.accounts.accounts, before.accounts.accounts, label + ': Cline account credentials are preserved');
  equal(after.accounts.keys, before.accounts.keys, label + ': gateway API keys are preserved');
  equal(after.config.providerModelPrefixes, before.config.providerModelPrefixes, label + ': provider namespaces are preserved');
  for (const field of ['strategy', 'dispatchMode', 'onlyFree']) equal(after.config[field], before.config[field], label + ': unrelated gateway ' + field + ' is preserved');
  const affected = entry => ids.has(entry.model) &&
    (provider === 'cline' ? entry.source === 'remote' : provider === 'opencode' ? entry.source === 'zen' : entry.source === 'provider' && entry.provider === provider);
  for (const beforeCombo of before.config.combos) {
    const afterCombo = after.config.combos.find(combo => combo.id === beforeCombo.id);
    check(!!afterCombo, label + ': saved combo remains configured');
    equal(identities(afterCombo.entries), identities(beforeCombo.entries.filter(entry => !affected(entry))), label + ': cleanup preserves combo pins to sibling providers');
  }
}

async function checkProviderModelManagement(theme, width) {
  const suffix = theme + '-' + width;
  const fixture = await startRemovalFixture(suffix);
  let context, viewerContext, page;
  let administratorPhase = 'active-management', viewerPhase = 'active-viewer';
  let permittedRemoval = null;
  const providerAction = (key, action) => page.locator('#modelsList .model-group-head[data-key="' + key + '"] [data-model-provider-action="' + action + '"]');
  const choices = () => page.locator('#modelProviderActionList .model-provider-removal-choice input[data-model-id]');
  const choiceIDs = () => choices().evaluateAll(inputs => inputs.map(input => input.dataset.modelId).sort());
  const checkedIDs = () => choices().evaluateAll(inputs => inputs.filter(input => input.checked).map(input => input.dataset.modelId).sort());
  const returnToCatalog = async () => {
    if (await page.locator('#tab-models').isHidden()) {
      const navigation = page.locator('.nav-item[data-tab="models"]');
      const mobileNavigation = page.locator('#mobileNavBtn');
      if (await mobileNavigation.isVisible() && await mobileNavigation.getAttribute('aria-expanded') !== 'true') await mobileNavigation.click();
      await navigation.click();
    }
    await page.waitForLoadState('networkidle');
    await acknowledgeFixtureNotices(page, 'return-models-' + suffix);
    await page.locator('#modelCatalogTab').click();
  };
  const closeAction = async () => {
    await page.locator('#modelProviderActionCancel').click();
    await page.locator('#modelProviderActionModal').waitFor({ state: 'hidden' });
  };
  const openRemoval = async (key, id) => {
    await providerAction(key, 'remove').click();
    await page.locator('#modelProviderActionModal').waitFor({ state: 'visible' });
    equal(await page.locator('#modelProviderActionProvider').inputValue(), id, suffix + ': removal dialog binds its sole stable provider ID');
  };
  const approveRemoval = async (button, refs, label) => {
    const before = await removalSnapshot(page, fixture);
    for (const ref of refs.filter(ref => ref.source === 'provider')) {
      const offering = before.offerings.find(offering => offering.providerId === ref.providerId && offering.modelId === ref.id);
      check(!!offering && !offering.delisted && !offering.disabled && offering.status === 'active', label + ': custom provider target is active before explicit removal');
    }
    const priorWrites = result.removalPayloads.length;
    await button.click();
    await page.locator('#confirmModal').waitFor({ state: 'visible' });
    check(result.removalPayloads.length === priorWrites, label + ': opening confirmation performs no mutation');
    const current = await removalSnapshot(page, fixture);
    checkCancelledRemoval(before, current, label + ' before confirmation');
    check((await page.locator('#confirmMsg').innerText()).includes('(' + refs.length + ')'), label + ': confirmation states the requested removal count');
    permittedRemoval = removalRefs(refs);
    const responsePromise = page.waitForResponse(response => response.url() === fixture.apiURL('models/remove') && response.request().method() === 'POST');
    await page.locator('#confirmModal [onclick="resolveConfirm(true)"]').click();
    const response = await responsePromise;
    const body = await response.json();
    result.removalResults.push({ suffix, label, status: response.status(), body });
    check(response.status() === 200 && body.success === true, label + ': real provider-scoped removal succeeds');
    equal(removalRefs(body.data.removed), removalRefs(refs), label + ': real API reports exactly the removed stable-provider refs');
    equal(body.data.skipped, [], label + ': active fixture targets are not silently skipped');
    equal(body.data.errors, [], label + ': removal has no hidden partial errors');
    await page.waitForLoadState('networkidle');
    await page.waitForFunction(() => !_modelProviderAction || !_modelProviderAction.busy);
    check(await page.locator('#modelProviderActionError').isHidden(), label + ': dialog has no removal error');
    check(result.removalPayloads.length === priorWrites + 1, label + ': one confirmation sends one scoped mutation');
    check(permittedRemoval === null, label + ': mutation consumed the one-time fixture authorization');
    const after = await removalSnapshot(page, fixture);
    checkScopedRemoval(before, after, refs, label);
    return after;
  };
  try {
    context = await browser.newContext({ viewport: { width, height: 900 }, storageState: fixture.administratorState });
    await context.addInitScript(theme => {
      localStorage.setItem('cline_admin_theme', theme);
      localStorage.setItem('cline_admin_lang', 'en');
      localStorage.setItem('cline_admin_quickstart_seen', '1');
    }, theme);
    await context.route('**/admin/api/**', async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname.replace('/admin/api/', '');
      if (request.method() === 'GET' && path === 'models') {
        const response = await route.fetch();
        const body = await response.json();
        body.data.lastSync = {};
        await route.fulfill({ response, json: body });
        return;
      }
      if (['GET', 'HEAD', 'OPTIONS'].includes(request.method())) { await route.continue(); return; }
      const body = request.postDataJSON();
      if (request.method() === 'POST' && path === 'models/remove' && permittedRemoval &&
        JSON.stringify(removalRefs(body.models || [])) === JSON.stringify(permittedRemoval)) {
        result.removalPayloads.push({ suffix, ...body });
        permittedRemoval = null;
        await route.continue();
        return;
      }
      result.forbiddenWrites.push('removal-' + suffix + ': ' + request.method() + ' ' + path);
      await route.fulfill({ status: 400, json: { success: false, error: 'Unexpected isolated removal verification write' } });
    });
    page = await context.newPage();
    captureBrowserEvidence(page, 'administrator', suffix, () => administratorPhase);
    await page.goto(fixture.base + '#models', { waitUntil: 'networkidle' });
    await acknowledgeFixtureNotices(page, 'management-' + suffix);
    await page.locator('#modelCatalogTab').click();
    await providerAction('src:prov:AntSeed', 'remove').waitFor({ state: 'visible' });
    if (verificationSlice !== 'viewer') {
      const untouched = await removalSnapshot(page, fixture);
      for (const key of ['src:cline', 'src:zen', 'src:prov:AntSeed', 'src:prov:Twin Provider']) {
        const header = page.locator('#modelsList .model-group-head[data-key="' + key + '"]');
        const labels = (await header.locator('.model-group-actions button').allTextContents()).map(label => label.trim());
        for (const label of ['Enable all', 'Disable all', 'Edit Provider', 'Remove models']) check(labels.includes(label), suffix + ': provider header includes ' + label);
      }
      for (const [key, node] of [['src:cline', '#providerNativeCline'], ['src:zen', '#providerNativeOpenCode']]) {
        await providerAction(key, 'edit').click();
        await page.locator('#providerSetup.open ' + node).waitFor({ state: 'visible' });
        check(await page.locator('#providerSetup.open ' + node).count() === 1, suffix + ': builtin provider edit opens its actual native configuration');
        await page.locator('#providerSetup .drawer-close').click();
        await page.locator('#providerSetup.open').waitFor({ state: 'hidden' });
        await returnToCatalog();
        result.cases++;
      }
      await providerAction('src:prov:AntSeed', 'edit').click();
      await page.locator('#providerEditor.open').waitFor({ state: 'visible' });
      equal(await page.evaluate(() => _editingProviderId), providerID, suffix + ': AntSeed edit binds the stable provider ID');
      equal(await page.locator('#provName').inputValue(), 'AntSeed', suffix + ': AntSeed edit displays the matching provider name');
      check((await page.locator('#provBaseURL').inputValue()).includes('/' + providerID + '/v1'), suffix + ': AntSeed edit displays the matching upstream');
      await page.locator('#providerEditor .drawer-close').click();
      await page.locator('#providerEditor.open').waitFor({ state: 'hidden' });
      await returnToCatalog();
      result.cases++;

      await providerAction('src:prov:Twin Provider', 'edit').click();
      await page.locator('#modelProviderActionModal').waitFor({ state: 'visible' });
      equal(await page.locator('#modelProviderActionProvider').inputValue(), '', suffix + ': same-name edit requires an explicit provider choice');
      equal(await page.locator('#modelProviderActionProvider').evaluate(select => Array.from(select.options).map(option => option.value).filter(Boolean).sort()), twinProviderIDs, suffix + ': same-name chooser exposes both stable IDs');
      check(await page.locator('#modelProviderActionSubmit').isDisabled(), suffix + ': ambiguous edit cannot proceed without a choice');
      check(await page.locator('#providerEditor.open').count() === 0, suffix + ': ambiguous edit never opens an arbitrary provider');
      await shot(page, 'provider-edit-chooser-' + suffix, '#modelProviderActionModal .modal-card');
      await page.locator('#modelProviderActionProvider').selectOption(twinProviderIDs[1]);
      await page.locator('#modelProviderActionSubmit').click();
      await page.locator('#providerEditor.open').waitFor({ state: 'visible' });
      equal(await page.evaluate(() => _editingProviderId), twinProviderIDs[1], suffix + ': explicit same-name choice opens the selected stable provider');
      check((await page.locator('#provBaseURL').inputValue()).includes('/' + twinProviderIDs[1] + '/v1'), suffix + ': same-name choice opens the selected upstream');
      await page.locator('#providerEditor .drawer-close').click();
      await page.locator('#providerEditor.open').waitFor({ state: 'hidden' });
      await returnToCatalog();
      checkCancelledRemoval(untouched, await removalSnapshot(page, fixture), suffix + ' provider edits');
      result.cases++;

      await openRemoval('src:prov:AntSeed', providerID);
      equal(await choiceIDs(), fixture.primaryIDs.slice().sort(), suffix + ': removal lists only the chosen provider raw model IDs');
      await page.locator('#modelProviderActionSearch').fill('removal-bulk');
      equal(await choiceIDs(), removalBulkIDs, suffix + ': provider removal list is searchable');
      await page.locator('#modelProviderActionSelectAll').click();
      equal(await checkedIDs(), removalBulkIDs, suffix + ': select all shown selects only matching provider models');
      check((await page.locator('#modelProviderActionCount').innerText()).includes('2 /'), suffix + ': removal selection count matches the visible selection');
      await page.locator('#modelProviderActionClear').click();
      equal(await checkedIDs(), [], suffix + ': clear resets the selected models');
      check(await page.locator('#modelProviderActionSubmit').isDisabled(), suffix + ': empty removal selection cannot submit');
      await page.locator('#modelProviderActionCancel').focus();
      await page.keyboard.press('Tab');
      check(await page.locator('#modelProviderActionSearch').evaluate(element => element === document.activeElement), suffix + ': removal dialog traps keyboard focus');
      await shot(page, 'provider-removal-filter-' + suffix, '#modelProviderActionModal .modal-card');
      await page.locator('#modelProviderActionSearch').fill('as/' + removalSharedID);
      equal(await choiceIDs(), [removalSharedID], suffix + ': removal searches the qualified literal @ ID');
      await choices().check();
      const cancelled = await removalSnapshot(page, fixture);
      const cancelledWriteCount = result.removalPayloads.length;
      await closeAction();
      await page.waitForLoadState('networkidle');
      check(result.removalPayloads.length === cancelledWriteCount, suffix + ': dialog cancellation sends no mutation');
      checkCancelledRemoval(cancelled, await removalSnapshot(page, fixture), suffix + ' dialog cancellation');
      result.cases++;

      await openRemoval('src:prov:AntSeed', providerID);
      await page.locator('#modelProviderActionSearch').fill(removalSharedID);
      await choices().check();
      await page.keyboard.press('Escape');
      await page.locator('#modelProviderActionModal').waitFor({ state: 'hidden' });
      check(result.removalPayloads.length === cancelledWriteCount, suffix + ': Escape cancellation sends no mutation');
      checkCancelledRemoval(cancelled, await removalSnapshot(page, fixture), suffix + ' keyboard dialog cancellation');
      result.cases++;

      await openRemoval('src:prov:AntSeed', providerID);
      await page.locator('#modelProviderActionSearch').fill(removalSharedID);
      const inlineRemove = page.locator('#modelProviderActionList button[data-model-id="' + removalSharedID + '"]');
      await inlineRemove.click();
      await page.locator('#confirmModal').waitFor({ state: 'visible' });
      check(result.removalPayloads.length === cancelledWriteCount, suffix + ': individual removal waits for confirmation');
      check((await page.locator('#confirmMsg').innerText()).includes('AntSeed'), suffix + ': confirmation identifies the target provider');
      await shot(page, 'provider-removal-confirmation-' + suffix, '#confirmModal .modal-card');
      await page.locator('#confirmModal [onclick="resolveConfirm(true)"]').focus();
      await page.keyboard.press('Tab');
      check(await page.locator('#confirmModal [onclick="resolveConfirm(false)"]').evaluate(element => element === document.activeElement), suffix + ': confirmation traps keyboard focus');
      await page.keyboard.press('Escape');
      await page.locator('#confirmModal').waitFor({ state: 'hidden' });
      await page.waitForFunction(() => !_modelProviderAction || !_modelProviderAction.busy);
      check(await page.locator('#modelProviderActionModal').isVisible(), suffix + ': cancelling confirmation keeps the parent removal dialog open');
      checkCancelledRemoval(cancelled, await removalSnapshot(page, fixture), suffix + ' confirmation cancellation');
      result.cases++;

      await approveRemoval(inlineRemove, [{ id: removalSharedID, source: 'provider', providerId: providerID }], suffix + ' custom-active individual @ removal');
      check(await choiceIDs().then(ids => !ids.includes(removalSharedID)), suffix + ': successful single removal leaves no stale picker row');
      result.cases++;
      await page.locator('#modelProviderActionSearch').fill('removal-bulk');
      await page.locator('#modelProviderActionSelectAll').click();
      await approveRemoval(page.locator('#modelProviderActionSubmit'), removalBulkIDs.map(id => ({ id, source: 'provider', providerId: providerID })), suffix + ' AntSeed bulk removal');
      await closeAction();
      result.cases++;

      for (const [key, source, id] of [['src:zen', 'zen', 'opencode'], ['src:cline', 'remote', 'cline']]) {
        await openRemoval(key, id);
        await page.locator('#modelProviderActionSearch').fill((id === 'cline' ? 'cl/' : 'op/') + removalSharedID);
        equal(await choiceIDs(), [removalSharedID], suffix + ': builtin removal searches its provider-qualified @ model');
        await approveRemoval(page.locator('#modelProviderActionList button[data-model-id="' + removalSharedID + '"]'),
          [{ id: removalSharedID, source, providerId: id }], suffix + ' ' + id + ' individual @ removal');
        await closeAction();
        result.cases++;
      }

      await providerAction('src:prov:Twin Provider', 'remove').click();
      await page.locator('#modelProviderActionModal').waitFor({ state: 'visible' });
      equal(await page.locator('#modelProviderActionProvider').inputValue(), '', suffix + ': same-name removal requires an explicit provider choice');
      equal(await choiceIDs(), [], suffix + ': ambiguous removal exposes no arbitrary provider models');
      check(await page.locator('#modelProviderActionSubmit').isDisabled(), suffix + ': ambiguous removal cannot submit');
      await page.locator('#modelProviderActionProvider').selectOption(twinProviderIDs[1]);
      equal(await choiceIDs(), [removalTwinKeepID, removalTwinID].sort(), suffix + ': explicit same-name choice lists only selected provider models');
      await page.locator('#modelProviderActionSearch').fill('tb/' + removalTwinID);
      equal(await choiceIDs(), [removalTwinID], suffix + ': selected duplicate provider is searched by its own namespace');
      await shot(page, 'provider-removal-duplicate-choice-' + suffix, '#modelProviderActionModal .modal-card');
      await approveRemoval(page.locator('#modelProviderActionList button[data-model-id="' + removalTwinID + '"]'),
        [{ id: removalTwinID, source: 'provider', providerId: twinProviderIDs[1] }], suffix + ' duplicate-name stable-provider removal');
      await closeAction();
      result.cases++;
      await pageShot(page, 'provider-management-after-removal-' + suffix, '#modelCatalogView');
    }

    viewerContext = await browser.newContext({ viewport: { width, height: 900 }, storageState: fixture.viewerState });
    await viewerContext.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: fixture.origin });
    await viewerContext.addInitScript(theme => {
      localStorage.setItem('cline_admin_theme', theme);
      localStorage.setItem('cline_admin_lang', 'en');
      localStorage.setItem('cline_admin_quickstart_seen', '1');
    }, theme);
    await viewerContext.route('**/admin/api/**', async route => {
      const request = route.request();
      if (['GET', 'HEAD', 'OPTIONS'].includes(request.method())) { await route.continue(); return; }
      result.forbiddenWrites.push('viewer-' + suffix + ': ' + request.method() + ' ' + new URL(request.url()).pathname);
      await route.fulfill({ status: 400, json: { success: false, error: 'Unexpected isolated viewer browser mutation' } });
    });
    const viewerPage = await viewerContext.newPage();
    captureBrowserEvidence(viewerPage, 'viewer', suffix, () => viewerPhase);
    await viewerPage.goto(fixture.base + '#models', { waitUntil: 'networkidle' });
    await acknowledgeFixtureNotices(viewerPage, 'viewer-' + suffix);
    await viewerPage.locator('#modelCatalogTab').click();
    await viewerPage.locator('#modelsList .model-group-head').first().waitFor({ state: 'visible' });
    const publicResponse = await viewerPage.request.get(fixture.apiURL('model-offerings'));
    check(publicResponse.status() === 200, suffix + ': viewer can read public provider model offerings');
    const publicOfferings = (await publicResponse.json()).data.offerings;
    const publicOffering = publicOfferings.find(offering => offering.providerId === providerID && offering.modelId === longID);
    check(!!publicOffering && publicOffering.qualifiedId === 'as/' + longID, suffix + ': viewer projection retains the qualified provider model name');
    await viewerPage.locator('#modelSearch').fill(publicOffering.qualifiedId);
    const publicRow = viewerPage.locator('#modelsList .model-row').filter({ has: viewerPage.getByText(longID, { exact: true }) });
    check(await publicRow.count() === 1, suffix + ': viewer can search and see the logical model by its provider alias');
    equal((await publicRow.locator('.model-provider-name code').allTextContents()).sort(),
      publicOfferings.filter(offering => offering.modelId === longID).map(offering => offering.qualifiedId).sort(),
      suffix + ': viewer Catalog renders the public provider alias data');
    const publicNames = publicOfferings.filter(offering => offering.modelId === longID).map(offering => offering.qualifiedId).sort();
    check(publicNames.length >= 3, suffix + ': viewer fixture exercises at least three provider aliases');
    const publicNamesGroup = publicRow.locator('.model-provider-names');
    const scrollLayout = await publicNamesGroup.evaluate(element => ({
      overflow: getComputedStyle(element).overflowX, scrollWidth: element.scrollWidth, width: element.clientWidth,
    }));
    if (scrollLayout.scrollWidth > scrollLayout.width + 1) check(['auto', 'scroll'].includes(scrollLayout.overflow), suffix + ': multiple provider aliases allow horizontal scrolling inside the group');
    for (const name of publicNames) {
      const button = providerNameButton(viewerPage, publicRow, name);
      await button.scrollIntoViewIfNeeded();
      await checkClipboard(viewerPage, button, name, 'viewer-catalog-' + suffix + '-' + name);
      const reachable = await button.evaluate(element => {
        const rect = element.getBoundingClientRect(), parent = element.closest('.model-provider-names').getBoundingClientRect();
        return rect.x >= parent.x - 1 && rect.right <= parent.right + 1;
      });
      check(reachable, suffix + ': scrolled provider alias is completely reachable by mouse');
    }
    if (scrollLayout.scrollWidth > scrollLayout.width + 1) check(await publicNamesGroup.evaluate(element => element.scrollLeft > 0), suffix + ': copying the last alias scrolls the compact group');
    await pageShot(viewerPage, 'provider-management-viewer-catalog-' + suffix, '#modelCatalogView');
    check(await viewerPage.locator('#modelsList [data-model-provider-action]:visible').count() === 0, suffix + ': viewer cannot see provider edit or removal actions');
    await viewerPage.evaluate(() => openModelProviderAction({ closest: () => document.querySelector('#modelsList .model-group-head') }, 'remove'));
    check(await viewerPage.locator('#modelProviderActionModal').isHidden(), suffix + ': direct browser invocation respects removal permission');
    const deniedBefore = await removalSnapshot(page, fixture);
    const denied = await viewerPage.request.post(fixture.apiURL('models/remove'), {
      headers: { Origin: fixture.origin, 'X-RouteNest-CSRF': fixture.viewerCSRF },
      data: { models: [{ id: longID, source: 'provider', providerId: providerID }] },
    });
    const deniedBody = await denied.json();
    result.permissionProbes.push({ suffix, status: denied.status(), body: deniedBody });
    check(denied.status() === 403 && deniedBody.error === 'permission_denied', suffix + ': real backend denies viewer model removal');
    checkCancelledRemoval(deniedBefore, await removalSnapshot(page, fixture), suffix + ' denied viewer mutation');
    await viewerPage.locator('#modelOfferingsTab').click();
    await viewerPage.locator('#globalOfferingSearch').fill(publicOffering.qualifiedId);
    equal(await viewerPage.locator('#globalOfferings td:first-child code').allTextContents(), [publicOffering.qualifiedId], suffix + ': viewer Offerings renders and searches the public provider alias');
    await pageShot(viewerPage, 'provider-management-viewer-offerings-' + suffix, '#modelOfferingsView');
    await viewerPage.locator('#modelCatalogTab').click();
    await pageShot(viewerPage, 'provider-management-viewer-' + suffix, '#modelCatalogView');
    result.cases++;
  } catch (error) {
    if (page) await page.screenshot({ path: join(artifactDir, 'failure-management-' + suffix + '.png'), animations: 'disabled' }).catch(() => {});
    throw error;
  } finally {
    viewerPhase = 'cleanup-viewer-context';
    if (viewerContext) await viewerContext.close();
    administratorPhase = 'cleanup-administrator-context';
    if (context) await context.close();
    await fixture.close();
  }
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

  if (verificationSlice === 'all') for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
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
    let phase = 'active-models';
    captureBrowserEvidence(page, 'administrator', suffix, () => phase);
    try {
      await checkModelNames(page, suffix);
      phase = 'active-combos';
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
    } finally { phase = 'cleanup-combo-context'; await context.close(); }
  }
  await stopFixtureServer(server);
  if (verificationSlice === 'viewer') await checkProviderModelManagement('light', 390);
  else for (const theme of ['light', 'dark']) for (const width of [390, 1440]) await checkProviderModelManagement(theme, width);
  check(result.removalFixtures.length === (verificationSlice === 'viewer' ? 1 : 4) && result.removalFixtures.every(fixture => fixture.cleaned), 'every isolated removal fixture is stopped and cleaned');
  if (verificationSlice === 'all') {
    check(result.upstreamRequests.some(request => request.path.startsWith('/antseed/') && request.model === rawAtID), 'AntSeed receives the literal raw @ ID');
    check(result.upstreamRequests.some(request => request.path.startsWith('/opencode/') && request.model === rawAtID), 'OpenCode receives the same literal raw @ ID');
  }
  check(result.forbiddenWrites.length === 0, 'no unintended writes or upstream fixture requests');
  check(result.browserHTTPFailures.length === 0, 'no unexpected browser HTTP failures');
  check(result.requestFailures.filter(request => request.phase.startsWith('active') && request.error !== 'net::ERR_ABORTED').length === 0, 'no live browser transport failures');
  check(result.browserErrors.length === 0, 'no browser errors');
  console.log(report());
} catch (error) {
  result.failure = error.message;
  console.error(report());
  process.exitCode = 1;
} finally {
  if (browser) await browser.close();
  await stopFixtureServer(server);
  upstream.closeAllConnections();
  await new Promise(resolve => upstream.close(resolve));
  writeFileSync(join(artifactDir, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  rmSync(dataDir, { recursive: true, force: true });
}

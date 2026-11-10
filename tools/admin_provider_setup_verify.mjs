// Exercise the actual embedded UI with catalogue/auth and masked-proxy fixtures.
// Fixture writes are intercepted. Run against a separate temporary-data instance.
import { createRequire } from 'node:module';
import { existsSync, mkdirSync } from 'node:fs';
const require = createRequire(import.meta.url);
let chromium;
for (const name of process.env.PLAYWRIGHT_MODULE ? [process.env.PLAYWRIGHT_MODULE] : ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core']) {
  try { ({ chromium } = require(name)); break; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
}
if (!chromium) throw new Error('Set PLAYWRIGHT_MODULE to an installed Playwright module.');
const base = new URL(process.env.BASE_URL || 'http://127.0.0.1:3467/admin/');
base.hash = '';
const bootstrapSecret = process.env.ADMIN_TEST_BOOTSTRAP_SECRET || '';
const testPassword = process.env.ADMIN_TEST_PASSWORD || '';
const widths = (process.env.VERIFY_WIDTHS || '1440,1280,1024,768,390,360').split(',').map(Number).filter(Number.isFinite);
const themes = (process.env.VERIFY_THEMES || 'light,dark').split(',').map(value => value.trim()).filter(Boolean);
const operationTimeout = Number(process.env.VERIFY_TIMEOUT_MS || 15000);
if (!Number.isFinite(operationTimeout) || operationTimeout <= 0) throw new Error('VERIFY_TIMEOUT_MS must be a positive number.');
const apiURL = path => new URL('/admin/api' + path, base).href;
const result = { cases: [], checks: 0, failures: [], diagnostics: { consoleErrors: 0, pageErrors: 0, requestFailures: 0 } };
let step = 'launch';
const screenshots = process.env.SCREENSHOTS === '1';
const artifactDirectory = process.env.ARTIFACT_DIR || '/tmp/routenest-unified-final-verification';
async function fixtureDeadline(pending) {
  let timer;
  try {
    return await Promise.race([pending, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error('Verification fixture request timed out.')), operationTimeout);
    })]);
  } finally { clearTimeout(timer); }
}
async function screenshot(page, label, width) {
  if (screenshots && [1440, 390].includes(width)) await page.screenshot({ path: artifactDirectory + '/' + label + '.png', fullPage: true });
}
function check(condition, label) {
  result.checks++;
  if (!condition) result.failures.push({ step, label });
}
const fixturePreset = { key: 'github-copilot', name: 'GitHub Copilot', baseURL: 'https://api.githubcopilot.com', kind: 'freeTier', freeTier: true, loginFlow: true, authFlow: 'github', modelIDs: ['verification/model'] };
const fixtureUnknown = { key: 'verification-unknown', name: 'Verification unavailable login', baseURL: 'https://example.invalid/v1', kind: 'other', loginFlow: true, authFlow: 'verification-unavailable' };
const fixtureConnection = { id: 'verification-connection', provider: 'github', email: 'verification@example.invalid', authType: 'device_code', status: 'active' };
const fixtureImported = { id: 'verification-imported-connection', provider: 'github', email: 'verification-import@example.invalid', authType: 'import_token', status: 'active' };
const fixtureSession = 'verification-device-session';
const envelope = data => ({ success: true, data });
async function apiData(context, path) {
  const response = await context.request.get(apiURL(path));
  if (!response.ok()) throw new Error('Verification backend read failed.');
  const body = await response.json();
  if (!body.success) throw new Error('Verification backend envelope failed.');
  return body.data;
}
async function duplicateIDs(page) {
  const ids = await page.evaluate(() => {
    const counts = new Map();
    document.querySelectorAll('[id]').forEach(el => counts.set(el.id, (counts.get(el.id) || 0) + 1));
    return [...counts].filter(([, count]) => count > 1).map(([id]) => id);
  });
  check(ids.length === 0, 'DOM IDs remain unique');
}
async function openDirectory(page) {
  await page.locator('#connectProviderBtn').click();
  await page.locator('#providerSetup.open').waitFor();
  await page.locator('#providerSetupChoices [data-setup-kind="cline"]').waitFor();
  check(await page.evaluate(() => location.hash === '#providers'), 'launcher retains Providers route');
}
async function closeSetup(page) {
  await page.evaluate(() => closeProviderSetup());
  await page.waitForFunction(() => !document.getElementById('providerSetup').classList.contains('open'));
}
async function interTypography(page, selector) {
  const fonts = await page.locator(selector).evaluateAll(nodes => nodes.filter(node => node.getClientRects().length).map(node => getComputedStyle(node).fontFamily));
  check(fonts.length > 0 && fonts.every(font => /^['"]?Inter['"]?(,|$)/.test(font) && !font.includes('IBM Plex Mono')), 'visible UI typography uses Inter: ' + selector);
}
let browser, storageState, csrfToken;
try {
  if (screenshots) mkdirSync(artifactDirectory, { recursive: true });
  browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined), args: ['--no-sandbox', '--disable-dev-shm-usage'] });
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
    csrfToken = storageState.cookies.find(cookie => cookie.name === 'routenest_admin_csrf')?.value || '';
    if (!csrfToken) throw new Error('Temporary administrator CSRF cookie is unavailable.');
  } finally {
    await setup.close();
  }
  for (const theme of themes) for (const width of widths) {
    const label = theme + '-' + width;
    const context = await browser.newContext({
      viewport: { width, height: 1000 }, storageState,
      extraHTTPHeaders: { 'X-RouteNest-CSRF': csrfToken },
    });
    let originalZen;
    let zenChanged = false;
    let releaseCatalogue;
    const captured = [];
    let mode = 'normal';
    let catalogueRequested;
    let approved = false;
    let imported = false;
    let deferConnectionRead = false, releaseConnectionRead, connectionReadRequested;
    let deferZenRead = false, releaseZenRead, zenReadRequested;
    let deferLoginStart = false, releaseLoginStart, loginStartRequested;
    let deferLoginStatus = false, releaseLoginStatus, loginStatusRequested;
    let expectedConsoleError = false;
    let closing = false;
    let proxyFixtures = null;
    try {
      const beforeChecks = result.checks;
      await context.addInitScript(({ theme }) => {
        localStorage.setItem('cline_admin_theme', theme);
        localStorage.setItem('cline_admin_lang', 'en');
        localStorage.setItem('cline_admin_quickstart_seen', '1');
      }, { theme });
      const realConnections = await apiData(context, '/connections');
      originalZen = await apiData(context, '/opencode/config');
      await context.route('**/admin/api/providers/presets', async route => {
        if (mode === 'delay') {
          if (catalogueRequested) catalogueRequested();
          await new Promise(resolve => { releaseCatalogue = resolve; });
        }
        if (mode === 'error') {
          await route.fulfill({ status: 503, json: { success: false, error: 'Verification directory unavailable' } });
        } else {
          await route.fulfill({ json: envelope({ presets: [fixturePreset, fixtureUnknown] }) });
        }
      });
      await context.route('**/admin/api/connections', async route => {
        const snapshot = [...(realConnections.connections || []), ...(approved ? [fixtureConnection] : []), ...(imported ? [fixtureImported] : [])];
        if (deferConnectionRead) {
          deferConnectionRead = false;
          const pending = new Promise(resolve => { releaseConnectionRead = resolve; });
          connectionReadRequested();
          await pending;
        }
        await route.fulfill({ json: envelope({ ...realConnections, connections: snapshot }) });
      });
      await context.route('**/admin/api/connections/import', async route => {
        imported = true;
        await route.fulfill({ json: envelope({ connection: fixtureImported }) });
      });
      await context.route('**/admin/api/connections/login/start', async route => {
        if (deferLoginStart) {
          deferLoginStart = false;
          const pending = new Promise(resolve => { releaseLoginStart = resolve; });
          loginStartRequested();
          await pending;
        }
        await route.fulfill({ json: envelope({ sessionId: fixtureSession, userCode: 'VERIFY', authUrl: 'https://example.invalid/device' }) });
      });
      await context.route('**/admin/api/connections/login/status?*', async route => {
        const snapshot = approved ? { done: true, success: true, connection: fixtureConnection } : { done: false };
        if (deferLoginStatus) {
          deferLoginStatus = false;
          const pending = new Promise(resolve => { releaseLoginStatus = resolve; });
          loginStatusRequested();
          await pending;
        }
        await route.fulfill({ json: envelope(snapshot) });
      });
      await context.route('**/admin/api/opencode/config', async route => {
        if (proxyFixtures) { await route.fulfill({ json: envelope(proxyFixtures.zen) }); return; }
        if (!deferZenRead) { await route.continue(); return; }
        deferZenRead = false;
        const response = await route.fetch();
        const pending = new Promise(resolve => { releaseZenRead = resolve; });
        zenReadRequested();
        await pending;
        await route.fulfill({ response }); // Real backend response, only delayed.
      });
      for (const [path, owner] of [['/opencode/config/update', 'zen'], ['/routenest/config/update', 'shared']]) {
        await context.route('**/admin/api' + path, async route => {
          if (!proxyFixtures) { await route.continue(); return; }
          proxyFixtures[owner] = { ...proxyFixtures[owner], ...route.request().postDataJSON() };
          await route.fulfill({ json: envelope({}) });
        });
      }
      await context.route('**/admin/api/routenest/config', async route => {
        if (proxyFixtures) await route.fulfill({ json: envelope(proxyFixtures.shared) });
        else await route.continue();
      });
      // A fake OAuth credential cannot be saved by the real backend. Verify the
      // unchanged submitted contract here; Go tests cover backend persistence.
      await context.route('**/admin/api/providers/save', route => route.fulfill({ json: envelope({ provider: { id: 'verification-provider' } }) }));
      const page = await context.newPage();
      page.setDefaultTimeout(operationTimeout);
      page.on('request', request => {
        const url = new URL(request.url());
        if (url.pathname.startsWith('/admin/api/') && !['GET', 'HEAD'].includes(request.method())) {
          captured.push({ path: url.pathname.slice('/admin/api'.length), body: request.postDataJSON() });
        }
      });
      page.on('console', message => { if (message.type() === 'error' && !expectedConsoleError && !closing) result.diagnostics.consoleErrors++; });
      page.on('pageerror', () => { if (!closing) result.diagnostics.pageErrors++; });
      page.on('requestfailed', () => { if (!closing) result.diagnostics.requestFailures++; });
      step = label + ': initialize';
      await page.goto(base.href + '#providers', { waitUntil: 'networkidle' });
      await page.evaluate(() => { if (typeof closeModelSyncModal === 'function') closeModelSyncModal(); setLang('en'); });
      check(await page.locator('#connectProviderBtn').count() === 1, 'one primary Connect Provider entry');
      await page.evaluate(() => {
        window.__verifyClineNode = document.getElementById('loginImportSection');
        window.__verifyClineParent = window.__verifyClineNode.parentNode;
        window.__verifyZenNode = document.getElementById('providerNativeOpenCode');
        window.__verifyZenParent = window.__verifyZenNode.parentNode;
        window.__verifyClineConfigNode = document.getElementById('providerNativeCline');
        window.__verifyClineConfigParent = window.__verifyClineConfigNode.parentNode;
        // Real clicks do not await async inline handlers. Observe their actual
        // promises so an already-idle page cannot hide pending form hydration.
        const choose = chooseProviderSetup;
        chooseProviderSetup = function (...args) {
          window.__verifySelectionPending = Promise.resolve(choose(...args));
          return window.__verifySelectionPending;
        };
        const importToken = importProviderSetupToken;
        importProviderSetupToken = function (...args) {
          window.__verifyImportPending = Promise.resolve(importToken(...args));
          return window.__verifyImportPending;
        };
        const startLogin = startProviderLogin;
        startProviderLogin = function (...args) {
          window.__verifyLoginStartPending = Promise.resolve(startLogin(...args));
          return window.__verifyLoginStartPending;
        };
        const submitToken = submitConnectionImport;
        submitConnectionImport = function (...args) {
          window.__verifyTokenSubmitPending = Promise.resolve(submitToken(...args));
          return window.__verifyTokenSubmitPending;
        };
      });
      step = label + ': settings ownership and account navigation';
      check(await page.evaluate(() => window.__verifyClineNode.closest('.tab-panel').id === 'tab-providers' && window.__verifyClineNode.closest('.page-subpage').dataset.subpage === 'oauth'), 'original Cline login form belongs to Providers OAuth settings');
      check(await page.evaluate(() => window.__verifyZenNode.closest('.tab-panel').id === 'tab-providers' && window.__verifyZenParent.hidden), 'original OpenCode settings belong to hidden Providers native forms');
      check(await page.locator('#tab-network #ocKey, #tab-network #ocProxies, #tab-network #zenHeadersBulk, #tab-network #headersTableBody').count() === 0, 'Network contains no provider-specific credentials, proxies, or headers');
      check(await page.locator('#tab-network #clineProxies, #tab-network #clineProxyStrategy').count() === 2, 'Network retains existing default egress settings');
      step = label + ': open connected accounts';
      await page.evaluate(() => showPageSubTab('credentials', 'conns'));
      check(await page.locator('#tab-credentials > [data-subpage="conns"]').isVisible() && !await page.locator('#tab-credentials > [data-subpage="inventory"]').isVisible(), 'Credentials OAuth accounts and API inventory have distinct subviews');
      step = label + ': connected accounts to Providers OAuth';
      await page.locator('#tab-credentials button[onclick="showPageSubTab(\'providers\',\'oauth\')"]').click();
      check(await page.evaluate(() => location.hash === '#providers'), 'account management link opens Providers route');
      check(await page.locator('#tab-providers > [data-subpage="oauth"]').isVisible() && await page.locator('#tab-providers .subtab[data-sub="oauth"]').getAttribute('aria-selected') === 'true', 'account management link selects Providers OAuth settings');
      check(await page.evaluate(() => document.getElementById('loginImportSection') === window.__verifyClineNode && window.__verifyClineNode.parentNode === window.__verifyClineParent), 'OAuth navigation preserves original Cline form identity');
      await screenshot(page, label + '-providers-oauth', width);
      step = label + ': open Cline connection settings';
      await page.locator('#tab-providers button[onclick="openProviderSetup(\'cline-config\')"]').click();
      await page.locator('#providerSetupContent #headersTableBody').waitFor({ state: 'attached' });
      await page.evaluate(() => window.__verifySelectionPending);
      check(await page.evaluate(() => document.getElementById('providerSetupContent').firstElementChild === window.__verifyClineConfigNode), 'Cline connection settings mount the original native form');
      check(await page.locator('#providerSetupContent button[onclick="saveHeaders(this)"]').isVisible(), 'Cline native header save remains available');
      await screenshot(page, label + '-cline-settings', width);
      await duplicateIDs(page);
      step = label + ': Cline settings to Network';
      await page.locator('#providerSetupContent button[onclick="applyTab(\'network\')"]').click();
      await page.waitForFunction(() => !document.getElementById('providerSetup').classList.contains('open'));
      check(await page.evaluate(() => location.hash === '#network') && await page.locator('#clineProxies').isVisible(), 'Cline network link closes setup and opens default proxy settings');
      check(await page.evaluate(() => window.__verifyClineConfigNode.parentNode === window.__verifyClineConfigParent && window.__verifyClineConfigNode.closest('.tab-panel').id === 'tab-providers'), 'Cline network navigation restores original provider settings');
      step = label + ': Network to provider settings';
      await page.locator('#tab-network .network-provider-settings-link button').click();
      check(await page.evaluate(() => location.hash === '#providers'), 'Network service settings link opens Providers');
      await page.locator('#tab-providers .subtab[data-sub="oauth"]').click();
      step = label + ': Providers OAuth to connected accounts';
      await page.locator('#tab-providers button[onclick="showPageSubTab(\'credentials\',\'conns\')"]').click();
      check(await page.evaluate(() => location.hash === '#credentials') && await page.locator('#connectionsList').isVisible(), 'Providers OAuth link returns to connected account management');
      step = label + ': open API credential inventory';
      await page.locator('#tab-credentials .subtab[data-sub="inventory"]').click();
      check(await page.locator('#globalCredentials').isVisible() && !await page.locator('#connectionsList').isVisible(), 'API credential tab shows its inventory separately');
      step = label + ': open API provider editor';
      await page.locator('#tab-credentials button[onclick="openProviderSetup(\'api\')"]').click();
      await page.locator('#providerEditor.open').waitFor();
      check(await page.evaluate(() => location.hash === '#providers') && await page.locator('#provKey').getAttribute('type') === 'password', 'API credential link opens the existing masked provider editor');
      await page.evaluate(() => { hideProviderEditor(); showPageSubTab('providers', 'inventory'); });
      check(captured.length === 0, 'settings and credential navigation do not write configuration');
      await interTypography(page, 'body');
      await interTypography(page, '#tab-providers .large-title, #tab-providers .provider-card-title');
      step = label + ': loading directory';
      const beforeDirectory = captured.length;
      await page.evaluate(() => { _presetList = []; });
      mode = 'delay';
      const requested = new Promise(resolve => { catalogueRequested = resolve; });
      await page.locator('#connectProviderBtn').click();
      await fixtureDeadline(requested);
      await page.locator('#providerSetupChoices .skeleton').first().waitFor({ state: 'attached' });
      check(captured.length === beforeDirectory, 'directory loading does not write');
      mode = 'normal';
      releaseCatalogue(); releaseCatalogue = null;
      await page.locator('#providerSetupChoices [data-setup-kind="cline"]').waitFor();
      check(await page.evaluate(() => location.hash === '#providers'), 'directory stays on Providers');
      step = label + ': directory search';
      await page.locator('#providerSetupSearch').fill('__no_matching_provider__');
      check(await page.locator('#providerSetupChoices [data-setup-kind]').count() === 0, 'search removes unmatched choices');
      check(await page.locator('#providerSetupChoices .system-empty').isVisible(), 'search has honest empty state');
      await page.locator('#providerSetupSearch').fill('GitHub');
      check(await page.locator('#providerSetupChoices [data-setup-kind]').count() === 1, 'search isolates matching provider');
      await page.locator('#providerSetupSearch').fill('');
      await duplicateIDs(page);
      await page.keyboard.press('Escape');
      await page.waitForFunction(() => !document.getElementById('providerSetup').classList.contains('open'));
      await page.waitForFunction(() => document.activeElement.id === 'connectProviderBtn');
      check(true, 'Escape closes and restores launcher focus');
      step = label + ': directory failure and retry';
      await page.evaluate(() => { _presetList = []; });
      mode = 'error'; expectedConsoleError = true;
      await page.locator('#connectProviderBtn').click();
      await page.locator('#providerSetupChoices .setup-error').waitFor();
      check(await page.locator('#providerSetupChoices [data-setup-kind]').count() === 0, 'failed catalogue does not invent choices');
      mode = 'normal';
      await page.locator('#providerSetupChoices .setup-error button').click();
      await page.locator('#providerSetupChoices [data-setup-kind="cline"]').waitFor();
      await page.waitForLoadState('networkidle'); expectedConsoleError = false;
      check(captured.length === beforeDirectory, 'error and retry do not write');
      step = label + ': Cline original form';
      await page.locator('[data-setup-kind="cline"]').click();
      await page.locator('#providerSetupContent #loginImportSection').waitFor();
      check(await page.evaluate(() => document.getElementById('loginImportSection') === window.__verifyClineNode), 'Cline setup uses original node');
      await screenshot(page, label + '-cline-login', width);
      await page.locator('#importTabs [data-tab="token"]').click();
      await page.locator('#tokenInput').fill('verification-unsaved-refresh-token');
      check(captured.length === beforeDirectory, 'choosing Cline does not start login or save');
      await duplicateIDs(page);
      await closeSetup(page);
      check(await page.evaluate(() => window.__verifyClineNode.parentNode === window.__verifyClineParent && window.__verifyClineNode.querySelector('#tokenInput').value === 'verification-unsaved-refresh-token'), 'closing returns Cline node and preserves unsaved input');
      step = label + ': OpenCode original form';
      await openDirectory(page);
      await page.locator('[data-setup-kind="opencode"]').click();
      await page.evaluate(() => window.__verifySelectionPending);
      await page.locator('#providerSetupContent #ocKey').waitFor();
      await page.waitForLoadState('networkidle');
      check(await page.evaluate(() => document.getElementById('providerSetupContent').firstElementChild === window.__verifyZenNode), 'OpenCode setup uses original node');
      check(await page.locator('#ocKey').getAttribute('type') === 'password', 'OpenCode key masked');
      await screenshot(page, label + '-opencode-settings', width);
      check(captured.length === beforeDirectory, 'choosing OpenCode does not write');
      const nextConcurrency = originalZen.maxConcurrency === 7 ? 8 : 7;
      await page.evaluate(value => { document.getElementById('ocMaxConcurrency').value = String(value); }, nextConcurrency);
      await page.locator('#providerSetupContent button[onclick="saveOcConfig(this)"]').click();
      await page.waitForFunction(() => document.getElementById('ocSaveResult').textContent.length > 0);
      await page.waitForLoadState('networkidle');
      zenChanged = true;
      const zenWrite = captured.filter(request => request.path === '/opencode/config/update').at(-1);
      check(!!zenWrite && zenWrite.body.maxConcurrency === nextConcurrency, 'existing OpenCode save receives selected value');
      const updatedZen = await apiData(context, '/opencode/config');
      check(updatedZen.maxConcurrency === nextConcurrency, 'actual backend stores selected OpenCode value');
      for (const key of ['enabled', 'key', 'baseURL', 'proxies', 'proxyStrategy', 'retries', 'failover', 'failoverCount', 'failoverMinutes', 'compaction', 'zenHeaders']) {
        check(JSON.stringify(updatedZen[key]) === JSON.stringify(originalZen[key]), 'OpenCode save preserves existing ' + key);
      }
      await duplicateIDs(page);
      await closeSetup(page);
      check(await page.evaluate(() => window.__verifyZenNode.parentNode === window.__verifyZenParent && window.__verifyZenNode.closest('.tab-panel').id === 'tab-providers'), 'closing returns OpenCode node to Providers native settings');
      const restore = await context.request.post(apiURL('/opencode/config/update'), { data: originalZen });
      check(restore.ok() && (await restore.json()).success, 'restore temporary OpenCode configuration'); zenChanged = false;
      step = label + ': API provider branch';
      await openDirectory(page);
      await page.locator('[data-setup-kind="api"]').click();
      await page.locator('#providerEditor.open').waitFor();
      check(await page.locator('#provKey').getAttribute('type') === 'password', 'custom API provider key masked');
      check(await page.evaluate(() => location.hash === '#providers'), 'API editor retains Providers route');
      await page.evaluate(() => hideProviderEditor());
      step = label + ': unsupported auth branch';
      await openDirectory(page);
      await page.locator('[data-setup-kind="preset"][data-setup-key="verification-unknown"]').click();
      await page.locator('#providerAuthSetup').waitFor();
      check(await page.locator('#providerAuthLoginBtn').count() === 0, 'unsupported flow offers no fake login');
      check(await page.locator('#providerAuthSetup button[onclick*="importProviderSetupToken"]').isVisible(), 'unsupported flow retains explicit token import');
      await page.evaluate(() => { discardProviderForm(); hideProviderEditor(); });
      step = label + ': OAuth preset and explicit start';
      await openDirectory(page);
      await page.locator('[data-setup-kind="preset"][data-setup-key="github-copilot"]').click();
      await page.locator('#providerAuthLoginBtn').waitFor();
      const beforeAuth = captured.length;
      check(captured.every(request => !['/connections/login/start', '/providers/save'].includes(request.path)), 'choosing preset does not auto-login or save');
      await page.locator('#providerAuthLoginBtn').click();
      await page.waitForFunction(() => _loginSessionId === 'verification-device-session');
      const authStart = captured.find(request => request.path === '/connections/login/start');
      check(authStart && authStart.body.provider === 'github', 'OAuth start uses preset authFlow rather than preset key');
      await page.evaluate(() => { window.__verifyAuthContext = _providerSetupAuthContext; });
      step = label + ': nonmatching completions';
      await page.evaluate(session => {
        providerSetupLoginComplete({ id: 'verification-wrong-provider', provider: 'kilocode' }, session);
        providerSetupLoginComplete({ id: 'verification-old-session', provider: 'github' }, 'verification-previous-session');
      }, fixtureSession);
      check(await page.evaluate(() => !_provConnSel.includes('verification-wrong-provider') && !_provConnSel.includes('verification-old-session')), 'wrong provider and stale session do not bind');
      step = label + ': OAuth success binding';
      approved = true;
      await page.evaluate(() => pollDeviceLogin());
      await page.waitForFunction(id => _provConnSel.includes(id), fixtureConnection.id);
      check(await page.locator('#provConnPick input:checked').count() === 1, 'completed account selected in existing connection picker');
      check(captured.length === beforeAuth + 1 && !captured.some(request => request.path === '/providers/save'), 'OAuth completion does not auto-save provider');
      step = label + ': explicit existing provider save';
      await page.locator('#providerEditor button[onclick="saveProvider(this)"]').click();
      await page.locator('#diffModal').waitFor({ state: 'visible' });
      check(!captured.some(request => request.path === '/providers/save'), 'provider confirmation precedes write');
      const refreshedInstances = page.waitForRequest(request => request.method() === 'GET' && new URL(request.url()).pathname === '/admin/api/provider-instances');
      await page.locator('#diffModal button[onclick="resolveDiff(true)"]').click();
      await refreshedInstances;
      check(true, 'provider save refreshes actual global provider projection');
      await page.waitForFunction(() => !document.getElementById('providerEditor').classList.contains('open'));
      const saved = captured.find(request => request.path === '/providers/save');
      check(saved && JSON.stringify(saved.body.connectionIds) === JSON.stringify([fixtureConnection.id]) && saved.body.connectionId === '', 'user save retains original connectionIds contract');
      check(saved && saved.body.baseURL === fixturePreset.baseURL && saved.body.name === fixturePreset.name && JSON.stringify(saved.body.modelIds) === JSON.stringify(fixturePreset.modelIDs), 'user save preserves preset endpoint/name/model contract');
      check(saved && saved.body.free === false, 'provider free-tier hint does not mark all models free');
      step = label + ': late completion after editor closes';
      await page.evaluate(session => providerSetupLoginComplete({ id: 'verification-after-close', provider: 'github' }, session), fixtureSession);
      check(await page.evaluate(() => !_provConnSel.includes('verification-after-close')), 'closed editor cannot bind late completion');
      await openDirectory(page);
      await page.locator('[data-setup-kind="preset"][data-setup-key="github-copilot"]').click();
      await page.evaluate(session => providerSetupLoginComplete({ id: 'verification-old-context', provider: 'github' }, session), fixtureSession);
      check(await page.evaluate(() => !_provConnSel.includes('verification-old-context')), 'new same-flow editor rejects previous login session');
      await page.evaluate(session => {
        providerSetupLoginStarted('github', session, window.__verifyAuthContext);
        providerSetupLoginComplete({ id: 'verification-old-generation', provider: 'github' }, session, window.__verifyAuthContext);
      }, fixtureSession);
      check(await page.evaluate(() => _providerSetupAuthContext.sessionId === null && !_provConnSel.includes('verification-old-generation')), 'in-flight start and completion reject an earlier editor context');
      await page.locator('#providerAuthSetup button[onclick*="importProviderSetupToken"]').click();
      await page.evaluate(() => window.__verifyImportPending);
      await page.locator('#connImportModal').waitFor({ state: 'visible' });
      check(await page.locator('#connImpProvider').inputValue() === 'github', 'explicit token import preselects matching auth flow');
      check(await page.locator('#connImpToken').getAttribute('type') === 'password', 'imported token input masked');
      if (theme === 'light' && width === 1440) {
        step = label + ': fresh connection snapshot after mutation';
        deferConnectionRead = true;
        const oldReadRequested = new Promise(resolve => { connectionReadRequested = resolve; });
        await page.evaluate(() => { window.__verifyOldConnectionsPending = loadConnections(); });
        await fixtureDeadline(oldReadRequested);
        await page.locator('#connImpToken').fill('verification-access-token');
        const importedResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/admin/api/connections/import');
        await page.locator('#connImpManualFoot').click();
        await importedResponse;
        releaseConnectionRead(); releaseConnectionRead = null;
        await page.evaluate(() => window.__verifyTokenSubmitPending);
        check(await page.evaluate(id => _connList.some(connection => connection.id === id) && _provConnSel.includes(id), fixtureImported.id), 'import ignores pre-mutation snapshot and binds newly returned account');
        check(await page.locator('#provConnPick input:checked').count() === 1, 'newly imported account is visibly selected after fresh read');
      }
      await page.evaluate(() => { hideConnectionImport(); discardProviderForm(); hideProviderEditor(); applyTab('models'); });
      step = label + ': Models subviews and typography';
      await page.locator('#modelCatalogTab').click();
      check(await page.locator('#modelCatalogView').isVisible() && !await page.locator('#modelOfferingsView').isVisible(), 'Models catalog has its own visible subview');
      check(await page.locator('#modelCatalogTab').getAttribute('aria-selected') === 'true', 'catalog tab selected accessibly');
      await interTypography(page, '#tab-models .large-title, #modelsList');
      await page.locator('#modelOfferingsTab').click();
      check(await page.locator('#modelOfferingsView').isVisible() && !await page.locator('#modelCatalogView').isVisible(), 'Models offerings has its own visible subview');
      check(await page.locator('#modelOfferingsTab').getAttribute('aria-selected') === 'true', 'offerings tab selected accessibly');
      await interTypography(page, '#modelOfferingsView');
      await duplicateIDs(page);
      check(await page.evaluate(() => Math.max(document.body.scrollWidth, document.documentElement.scrollWidth) <= innerWidth + 1), 'new workflows have no page overflow');
      if (theme === 'light' && width === 1440) {
        step = label + ': stale deferred setup opening';
        await page.evaluate(() => { _presetList = []; });
        mode = 'delay';
        const oldDirectoryRequested = new Promise(resolve => { catalogueRequested = resolve; });
        await page.evaluate(() => { window.__verifyOldOpenPending = openProviderSetup('cline'); });
        await fixtureDeadline(oldDirectoryRequested);
        await closeSetup(page);
        await page.evaluate(() => { window.__verifyNewOpenPending = openProviderSetup(); });
        mode = 'normal'; releaseCatalogue(); releaseCatalogue = null;
        await page.evaluate(() => Promise.all([window.__verifyOldOpenPending, window.__verifyNewOpenPending]));
        check(await page.locator('#providerSetupDirectory').isVisible() && !await page.locator('#providerSetupWorkspace').isVisible(), 'old deferred selection cannot replace newly opened directory');
        check(await page.evaluate(() => window.__verifyClineNode.parentNode === window.__verifyClineParent), 'old deferred Cline selection cannot move original form');
        step = label + ': Zen hydration protects interactive controls';
        deferZenRead = true;
        const zenRequested = new Promise(resolve => { zenReadRequested = resolve; });
        await page.locator('[data-setup-kind="opencode"]').click();
        await fixtureDeadline(zenRequested);
        check(await page.locator('#providerSetupContent [onchange="saveOcConfig()"], #providerSetupContent [onclick="saveOcConfig(this)"]').evaluateAll(controls => controls.length > 0 && controls.every(control => control.disabled)), 'Zen config controls disabled until actual config hydration completes');
        releaseZenRead(); releaseZenRead = null;
        await page.evaluate(() => window.__verifySelectionPending);
        check(await page.locator('#ocMaxConcurrency').isEnabled(), 'Zen controls restored after hydration');
        await closeSetup(page);
        step = label + ': cancelled login start cannot retarget new editor';
        approved = false;
        await page.evaluate(() => openProviderSetup('preset', 'github-copilot'));
        deferLoginStart = true;
        const oldLoginRequested = new Promise(resolve => { loginStartRequested = resolve; });
        await page.locator('#providerAuthLoginBtn').click();
        await fixtureDeadline(oldLoginRequested);
        await page.evaluate(() => { closeDeviceLogin(); discardProviderForm(); hideProviderEditor(); });
        await page.evaluate(() => openProviderSetup('preset', 'github-copilot'));
        releaseLoginStart(); releaseLoginStart = null;
        await page.evaluate(() => window.__verifyLoginStartPending);
        check(await page.evaluate(() => _loginSessionId === '' && _providerSetupAuthContext.sessionId === null && _provConnSel.length === 0), 'cancelled start response cannot activate or bind a new editor');
        step = label + ': cancelled poll cannot close or bind new login';
        await page.locator('#providerAuthLoginBtn').click();
        await page.evaluate(() => window.__verifyLoginStartPending);
        approved = true; deferLoginStatus = true;
        const oldStatusRequested = new Promise(resolve => { loginStatusRequested = resolve; });
        await page.evaluate(() => { clearInterval(_loginPollTimer); _loginPollTimer = null; window.__verifyOldPollPending = pollDeviceLogin(); });
        await fixtureDeadline(oldStatusRequested);
        await page.evaluate(() => { closeDeviceLogin(); discardProviderForm(); hideProviderEditor(); });
        await page.evaluate(() => openProviderSetup('preset', 'github-copilot'));
        await page.locator('#providerAuthLoginBtn').click();
        await page.evaluate(() => window.__verifyLoginStartPending);
        await page.evaluate(() => { clearInterval(_loginPollTimer); _loginPollTimer = null; });
        releaseLoginStatus(); releaseLoginStatus = null;
        await page.evaluate(() => window.__verifyOldPollPending);
        check(await page.evaluate(id => _loginSessionId === id && _providerSetupAuthContext.sessionId === id && _provConnSel.length === 0, fixtureSession), 'old poll completion cannot bind or cancel the new session');
        check(await page.locator('#connLoginModal').isVisible(), 'old poll leaves current login modal open');
        await page.evaluate(() => { closeDeviceLogin(); discardProviderForm(); hideProviderEditor(); });
        await duplicateIDs(page);
        step = label + ': masked proxy configuration preservation';
        const maskedProxy = 'http://***@verification-proxy.invalid:8080';
        const editedMask = maskedProxy + '\nhttps://***@verification-second.invalid:8443';
        const replacementProxy = 'http://verification-replacement.invalid:8080';
        proxyFixtures = {
          zen: { ...originalZen, key: 'verification-zen-key', maxConcurrency: 9, proxies: [maskedProxy], proxyStrategy: 'round_robin' },
          shared: { proxies: [maskedProxy], proxyStrategy: 'round_robin' },
        };
        await page.evaluate(() => openProviderSetup('opencode'));
        await page.evaluate(async () => { document.getElementById('ocMaxConcurrency').value = '10'; await saveOcConfig(); });
        let proxyWrite = captured.filter(request => request.path === '/opencode/config/update').at(-1);
        check(proxyWrite.body.maxConcurrency === 10 && !Object.hasOwn(proxyWrite.body, 'proxies'), 'unrelated Zen save omits unchanged masked proxies');
        await page.evaluate(async () => { document.getElementById('ocProxyStrategy').value = 'random'; await saveOcConfig(); });
        proxyWrite = captured.filter(request => request.path === '/opencode/config/update').at(-1);
        check(proxyWrite.body.proxyStrategy === 'random' && !Object.hasOwn(proxyWrite.body, 'proxies'), 'Zen strategy save preserves existing proxy credentials');
        let writesBeforeMask = captured.length;
        await page.evaluate(async value => { document.getElementById('ocProxies').value = value; await saveOcConfig(); }, editedMask);
        check(captured.length === writesBeforeMask, 'edited Zen proxy masks are rejected before a write');
        await page.evaluate(async value => { document.getElementById('ocProxies').value = value; await saveOcConfig(); }, replacementProxy);
        proxyWrite = captured.filter(request => request.path === '/opencode/config/update').at(-1);
        check(JSON.stringify(proxyWrite.body.proxies) === JSON.stringify([replacementProxy]), 'explicit Zen proxy replacement retains original save contract');
        await page.evaluate(async () => { document.getElementById('ocProxies').value = ''; await saveOcConfig(); });
        proxyWrite = captured.filter(request => request.path === '/opencode/config/update').at(-1);
        check(Array.isArray(proxyWrite.body.proxies) && proxyWrite.body.proxies.length === 0, 'explicit Zen proxy clearing sends an empty list');
        await closeSetup(page);
        await page.evaluate(async () => { applyTab('network'); await loadClineProxyConfig(); });
        await page.evaluate(async () => { document.getElementById('clineProxyStrategy').value = 'random'; await saveClineProxyConfig(); });
        proxyWrite = captured.filter(request => request.path === '/routenest/config/update').at(-1);
        check(proxyWrite.body.proxyStrategy === 'random' && !Object.hasOwn(proxyWrite.body, 'proxies'), 'default egress strategy save preserves existing proxy credentials');
        writesBeforeMask = captured.length;
        await page.evaluate(async value => { document.getElementById('clineProxies').value = value; await saveClineProxyConfig(); }, editedMask);
        check(captured.length === writesBeforeMask, 'edited default egress masks are rejected before a write');
        await page.evaluate(async value => { document.getElementById('clineProxies').value = value; await saveClineProxyConfig(); }, replacementProxy);
        proxyWrite = captured.filter(request => request.path === '/routenest/config/update').at(-1);
        check(JSON.stringify(proxyWrite.body.proxies) === JSON.stringify([replacementProxy]), 'explicit default egress replacement retains original save contract');
        await page.evaluate(async () => { document.getElementById('clineProxies').value = ''; await saveClineProxyConfig(); });
        proxyWrite = captured.filter(request => request.path === '/routenest/config/update').at(-1);
        check(Array.isArray(proxyWrite.body.proxies) && proxyWrite.body.proxies.length === 0, 'explicit default egress clearing sends an empty list');
        proxyFixtures = null;
        await page.evaluate(() => Promise.all([loadOcConfig(), loadClineProxyConfig()]));
      }
      result.cases.push({ theme, width, checks: result.checks - beforeChecks });
      console.error('Verified provider setup ' + label + '.');
    } catch (error) {
      result.failures.push({ step, errorType: error.name });
      console.error('Provider setup failed at ' + step + ' (' + error.name + ').');
    } finally {
      if (releaseCatalogue) releaseCatalogue();
      for (const release of [releaseConnectionRead, releaseZenRead, releaseLoginStart, releaseLoginStatus]) if (release) release();
      if (zenChanged && originalZen) {
        try { await context.request.post(apiURL('/opencode/config/update'), { data: originalZen }); } catch { result.failures.push({ step: label, label: 'temporary Zen restore failed' }); }
      }
      closing = true;
      await context.close();
    }
  }
  for (const [kind, count] of Object.entries(result.diagnostics)) check(count === 0, 'unexpected browser diagnostic: ' + kind);
} catch (error) {
  result.failures.push({ step, errorType: error.name });
} finally {
  if (browser) await browser.close();
}
console.log(JSON.stringify(result, null, 2));
process.exitCode = result.failures.length ? 1 : 0;

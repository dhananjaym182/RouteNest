// Exercise the shipped Access Settings and auth API on an isolated local instance.
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { existsSync, mkdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { isAbsolute, relative, resolve } from 'node:path';

const result = {
  layoutChecks: 0, formChecks: 0, securityChecks: 0, screenshots: 0, failures: [],
  diagnostics: { consoleErrors: 0, pageErrors: 0, requestFailures: 0, externalRequests: 0 },
};
class VerificationFailure extends Error {}
let currentStep = 'validate verification configuration';
let browser, cleanupContext, activeAdminPassword, initialAdminPassword, base;
let attemptedCreate = false, passwordRotated = false, collectDiagnostics = false;
const testUsername = 'access-verify-' + randomBytes(6).toString('hex');
const eightCharacterPassword = () => randomBytes(6).toString('base64url');
const userPassword = eightCharacterPassword(), resetPassword = eightCharacterPassword(), rotatedPassword = eightCharacterPassword();

function check(actual, expected, label, category = 'formChecks') {
  result[category]++;
  try { assert.deepEqual(actual, expected); }
  catch { throw new VerificationFailure(label); }
}

async function api(context, path, { method = 'GET', data, status = 200 } = {}) {
  const headers = { Origin: base.origin };
  if (method !== 'GET') {
    const csrf = (await context.cookies()).find(cookie => cookie.name === 'routenest_admin_csrf');
    if (csrf) headers['X-RouteNest-CSRF'] = csrf.value;
  }
  const request = () => context.request.fetch(new URL('/admin/api' + path, base).href, { method, headers, data, maxRetries: 2, timeout: 15000 });
  let response = await request();
  if (response.status() === 429 && method === 'POST' && (path === '/login' || path === '/auth/bootstrap')) {
    const retrySeconds = Number(response.headers()['retry-after']);
    if (retrySeconds > 0 && retrySeconds <= 15) {
      await new Promise(resolve => setTimeout(resolve, retrySeconds * 1000));
      response = await request();
    }
  }
  check(response.status(), status, currentStep + ': API status', 'securityChecks');
  if (status !== 200) return null;
  const envelope = await response.json();
  check(envelope.success, true, currentStep + ': successful API response', 'securityChecks');
  return envelope.data;
}

async function login(context, password, username = 'admin') {
  return api(context, '/login', { method: 'POST', data: { username, password } });
}

async function loginFormChecks(screenshotDir) {
  for (const theme of ['light', 'dark']) {
    for (const width of [1440, 390, 360]) {
      currentStep = theme + '-' + width + ': login fields';
      const context = await browser.newContext({ viewport: { width, height: 900 } });
      try {
        await context.addInitScript(theme => {
          localStorage.setItem('cline_admin_theme', theme);
          localStorage.setItem('cline_admin_lang', 'en');
          localStorage.setItem('cline_admin_quickstart_seen', '1');
        }, theme);
        const page = await context.newPage();
        page.setDefaultTimeout(10000);
        currentStep = theme + '-' + width + ': load the login page';
        await page.goto(base.href, { waitUntil: 'networkidle' });
        currentStep = theme + '-' + width + ': show the login form';
        await page.locator('#loginOverlay').waitFor({ state: 'visible' });
        currentStep = theme + '-' + width + ': inspect login spacing';
        const fields = await page.evaluate(() => {
          const username = document.getElementById('loginUsername');
          const password = document.getElementById('loginPassword');
          const userRow = document.getElementById('loginUsernameRow').getBoundingClientRect();
          const passwordRow = document.getElementById('loginPasswordRow').getBoundingClientRect();
          const card = document.querySelector('.login-card').getBoundingClientRect();
          return {
            labelled: [username, password].every(input => input.labels.length && [...input.labels].some(label => label.textContent.trim())),
            gap: passwordRow.top - userRow.bottom,
            controls: [username, password].every(input => {
              const bounds = input.getBoundingClientRect(), styles = getComputedStyle(input);
              return bounds.height >= 44 && bounds.left > card.left && bounds.right < card.right && parseFloat(styles.borderTopWidth) >= 1 && styles.borderTopStyle === 'solid';
            }),
            contained: card.left >= 0 && card.right <= innerWidth,
          };
        });
        check(fields.labelled, true, currentStep + ': visible field labels', 'layoutChecks');
        check(fields.gap >= 15, true, currentStep + ': comfortable spacing between fields', 'layoutChecks');
        check(fields.controls, true, currentStep + ': bordered controls with comfortable height', 'layoutChecks');
        check(fields.contained, true, currentStep + ': card fits viewport', 'layoutChecks');
        if (screenshotDir) {
          await page.screenshot({ path: resolve(screenshotDir, 'login-' + theme + '-' + width + '.png') });
          result.screenshots++;
        }
        if ((theme === 'light' && width === 1440) || (theme === 'dark' && width === 360)) {
          await page.locator('#loginForm').getByLabel('Username', { exact: true }).fill('admin');
          currentStep = theme + '-' + width + ': enter the login password';
          await page.locator('#loginForm').getByLabel('Password', { exact: true }).fill(initialAdminPassword);
          const response = mutationResponse(page, '/login');
          const navigation = page.waitForNavigation({ waitUntil: 'networkidle' });
          navigation.catch(() => {});
          await page.locator('#loginPassword').press('Enter');
          currentStep = theme + '-' + width + ': verify Enter sign-in';
          const loginStatus = (await response).status();
          check(loginStatus, 200, currentStep + ': Enter signs in (HTTP ' + loginStatus + ')', 'securityChecks');
          await navigation;
          await page.waitForFunction(() => typeof _adminPrincipal !== 'undefined' && _adminPrincipal && document.getElementById('loginOverlay').style.display !== 'flex');
        }
      } finally { await context.close(); }
    }
  }
}

async function openAccess(context, theme = 'light') {
  await context.addInitScript(theme => {
    localStorage.setItem('cline_admin_theme', theme);
    localStorage.setItem('cline_admin_lang', 'en');
    localStorage.setItem('cline_admin_quickstart_seen', '1');
  }, theme);
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on('console', message => { if (collectDiagnostics && message.type() === 'error') result.diagnostics.consoleErrors++; });
  page.on('pageerror', () => { if (collectDiagnostics) result.diagnostics.pageErrors++; });
  page.on('requestfailed', () => { if (collectDiagnostics) result.diagnostics.requestFailures++; });
  page.on('request', request => {
    if (collectDiagnostics && new URL(request.url()).origin !== base.origin) result.diagnostics.externalRequests++;
  });
  await page.goto(base.href + '#security', { waitUntil: 'networkidle' });
  await page.waitForFunction(() => typeof _adminPrincipal !== 'undefined' && _adminPrincipal && document.getElementById('loginOverlay').style.display !== 'flex');
  await page.evaluate(() => { if (typeof closeModelSyncModal === 'function') closeModelSyncModal(); });
  try { await page.locator('#tab-security .subtab[data-sub="access"]').click(); }
  catch {
    const state = await page.evaluate(() => ({
      activeTab: typeof _activeTabName === 'undefined' ? null : _activeTabName,
      login: document.getElementById('loginOverlay').style.display,
      sync: document.getElementById('modelSyncOverlay').style.display,
      security: document.getElementById('tab-security').style.display,
    }));
    throw new VerificationFailure(currentStep + ': cannot open Access Settings ' + JSON.stringify(state));
  }
  await page.waitForLoadState('networkidle');
  return page;
}

async function layout(page, label) {
  const state = await page.evaluate(() => {
    const visible = element => !!element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden';
    const access = document.querySelector('.access-settings');
    const controls = [...access.querySelectorAll('input:not([type=hidden],[type=checkbox]),select,textarea,button,summary')].filter(visible);
    return {
      overflow: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > innerWidth + 1,
      cardCount: [...access.querySelectorAll('.access-card')].filter(visible).length,
      contained: controls.every(control => {
        const bounds = control.getBoundingClientRect(), card = control.closest('.access-card').getBoundingClientRect();
        return bounds.left >= card.left - 1 && bounds.right <= card.right + 1;
      }),
      labelled: [...access.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(visible).every(control => control.labels.length || control.getAttribute('aria-label')),
      namedButtons: [...access.querySelectorAll('button')].filter(visible).every(button => button.textContent.trim() || button.getAttribute('aria-label')),
      touchTargets: innerWidth > 600 || controls.every(control => control.getBoundingClientRect().height >= 43),
    };
  });
  check(state.overflow, false, label + ': no page overflow', 'layoutChecks');
  check(state.cardCount, 5, label + ': five distinct access sections', 'layoutChecks');
  check(state.contained, true, label + ': controls fit their cards', 'layoutChecks');
  check(state.labelled, true, label + ': labelled inputs', 'layoutChecks');
  check(state.namedButtons, true, label + ': named buttons', 'layoutChecks');
  check(state.touchTargets, true, label + ': mobile touch targets', 'layoutChecks');
}

async function captureEditor(page, path) {
  const scrollTop = await page.evaluate(() => {
    const main = document.querySelector('.main'), editor = document.getElementById('ssoProviderEditor');
    const previous = main.scrollTop;
    const toolbarHeight = document.querySelector('.top-tools')?.getBoundingClientRect().height || 72;
    document.getElementById('toastStack').replaceChildren();
    main.scrollTop += editor.getBoundingClientRect().top - main.getBoundingClientRect().top - toolbarHeight - 16;
    return previous;
  });
  try { await page.screenshot({ path }); result.screenshots++; }
  finally { await page.evaluate(value => { document.querySelector('.main').scrollTop = value; }, scrollTop); }
}

async function keyboardChecks(page) {
  await page.locator('#settingCurrentPassword').focus();
  for (const next of ['settingPassword', 'settingPasswordConfirm', 'saveAdminPassword']) {
    await page.keyboard.press('Tab');
    check(await page.evaluate(() => document.activeElement.id), next, 'password form keyboard order');
  }
  const summary = page.locator('#ssoOIDCAdvanced > summary');
  await summary.focus();
  await page.keyboard.press('Enter');
  check(await page.locator('#ssoOIDCAdvanced').evaluate(element => element.open), true, 'optional settings open with keyboard');
  await page.keyboard.press('Enter');
  check(await page.locator('#ssoOIDCAdvanced').evaluate(element => element.open), false, 'optional settings close with keyboard');
  const role = page.locator('#ssoDefaultRoles input[value="viewer"]');
  await role.focus();
  await page.keyboard.press('Space');
  check(await role.isChecked(), true, 'default role selectable with keyboard');
  await page.keyboard.press('Space');
}

async function localeChecks(page, language) {
  await page.evaluate(language => setLang(language), language);
  await page.waitForLoadState('networkidle');
  await page.evaluate(() => { if (typeof closeModelSyncModal === 'function') closeModelSyncModal(); });
  check(await page.locator('#adminPasswordHelp').textContent(), language === 'en' ? 'At least 8 characters.' : '至少 8 个字符。', language + ': password minimum help');
  for (const id of ['settingPassword', 'settingPasswordConfirm', 'adminUserPassword', 'loginNewPassword', 'loginConfirmPassword']) {
    check(await page.locator('#' + id).getAttribute('minlength'), '8', language + ': ' + id + ' minimum');
  }
  check((await page.locator('label[for="adminUserPassword"]').textContent()).includes('8'), true, language + ': administrator password label');
  check((await page.locator('#loginNewPassword').getAttribute('placeholder')).includes('8'), true, language + ': bootstrap password placeholder');
}

async function ssoChecks(page) {
  const oidc = {
    id: 'verify-oidc', type: 'oidc', enabled: false,
    oidc: {
      issuer: 'https://id.example.invalid', clientID: 'verification-client',
      redirectURL: 'https://admin.example.invalid/admin/api/auth/oidc/callback',
      clientSecret: { kind: 'env', name: 'ROUTENEST_VERIFY_OIDC_SECRET' },
      scopes: ['openid', 'profile'], usernameClaim: 'preferred_username', groupsClaim: 'groups',
      defaultRoles: ['viewer', 'provider_admin'], groupRoleMap: { operators: ['provider_admin'] },
    },
  };
  await page.evaluate(config => fillSSOProviderEditor(config, true), oidc);
  check(await page.evaluate(() => collectSSOProvider()), oidc, 'OIDC checkbox roles and reference payload preserved');
  check(await page.locator('#ssoOIDCAdvanced').evaluate(element => element.open), true, 'populated OIDC options disclosed');
  check(await page.locator('#ssoRoleMappingDetails').evaluate(element => element.open), true, 'populated group mapping disclosed');
  check(await page.locator('#ssoSAMLFields').isVisible(), false, 'irrelevant SAML fields hidden');
  check(await page.locator('#ssoProviderID').isDisabled(), true, 'stored provider ID immutable');
  check(await page.locator('#ssoProviderType').isDisabled(), true, 'stored provider protocol immutable');

  const saml = {
    id: 'verify-saml', type: 'saml', enabled: false,
    saml: {
      metadataURL: 'https://id.example.invalid/metadata', entityID: 'https://admin.example.invalid/saml',
      metadataEndpoint: 'https://admin.example.invalid/admin/api/auth/saml/metadata', acsURL: 'https://admin.example.invalid/admin/api/auth/saml/acs',
      usernameAttribute: 'email', groupsAttribute: 'groups',
      certificate: { kind: 'file', name: '/tmp/verification-certificate.pem' }, privateKey: { kind: 'env', name: 'ROUTENEST_VERIFY_SAML_KEY' },
      defaultRoles: ['viewer'], groupRoleMap: {},
    },
  };
  await page.evaluate(config => fillSSOProviderEditor(config, true), saml);
  check(await page.evaluate(() => collectSSOProvider()), saml, 'SAML role and paired signing-reference payload preserved');
  check(await page.locator('#ssoSAMLAdvanced').evaluate(element => element.open), true, 'populated SAML options disclosed');
  check(await page.locator('#ssoOIDCFields').isVisible(), false, 'irrelevant OIDC fields hidden');
  check(await page.locator('#ssoRoleMappingDetails').evaluate(element => element.open), false, 'empty group mapping remains collapsed');

  for (const mapping of ['not json', '[]', '{"operators":["break_glass"]}', '{"operators":"viewer"}', '{"":["viewer"]}']) {
    const rejected = await page.evaluate(mapping => {
      document.getElementById('ssoGroupRoleMap').value = mapping;
      try { readSSORoleMapping(); return false; } catch { return true; }
    }, mapping);
    check(rejected, true, 'invalid or reserved group role mapping rejected');
  }
  const validation = await page.evaluate(() => {
    newSSOProvider('oidc');
    const available = [...document.querySelectorAll('#ssoDefaultRoles input')].map(input => input.value);
    const rejects = () => { try { readSSORoleMapping(); return false; } catch { return true; } };
    const noRoles = rejects();
    const injected = document.createElement('input'); injected.type = 'checkbox'; injected.value = 'break_glass'; injected.checked = true;
    document.getElementById('ssoDefaultRoles').appendChild(injected);
    const reserved = rejects(); injected.remove();
    return { available, noRoles, reserved, unicodeTooShort: adminPasswordTooShort('🔒'.repeat(7)), unicodeAccepted: !adminPasswordTooShort('🔒'.repeat(8)) };
  });
  check(validation.available, ['viewer', 'provider_admin', 'credential_admin', 'security_admin'], 'only assignable default roles offered');
  check(validation.noRoles, true, 'empty role assignment rejected');
  check(validation.reserved, true, 'reserved default role rejected');
  check(validation.unicodeTooShort, true, 'seven Unicode code points rejected');
  check(validation.unicodeAccepted, true, 'eight Unicode code points accepted');
  await page.evaluate(() => closeSSOProviderEditor());
}

function mutationResponse(page, path) {
  const pending = page.waitForResponse(response => new URL(response.url()).pathname === '/admin/api' + path && response.request().method() === 'POST');
  pending.catch(() => {});
  return pending;
}

async function passwordLifecycle(context, page) {
  currentStep = 'create disposable local administrator with eight characters';
  await page.evaluate(() => { setLang('en'); closeSSOProviderEditor(); openAdminUserEditor(); });
  await page.locator('#adminUserUsername').fill(testUsername);
  await page.locator('#adminUserPassword').fill('🔒'.repeat(7));
  await page.locator('#adminUserPassword').press('Enter');
  check((await page.locator('#adminUserError').textContent()).includes('8'), true, 'UI rejects seven-character local-user password');
  await page.locator('#adminUserPassword').fill(userPassword);
  const created = mutationResponse(page, '/auth/users'); attemptedCreate = true;
  await page.locator('#adminUserPassword').press('Enter');
  check((await created).status(), 201, 'eight-character local-user creation', 'securityChecks');
  await page.waitForFunction(() => document.getElementById('adminUserEditor').style.display === 'none');
  const userContext = await browser.newContext();
  try {
    await login(userContext, userPassword, testUsername);
    currentStep = 'reset disposable local administrator password';
    const row = page.locator('#adminUsersList .access-list-row').filter({ hasText: testUsername });
    await row.getByRole('button', { name: 'Edit', exact: true }).click();
    await page.locator('#adminUserPassword').fill(resetPassword);
    const reset = mutationResponse(page, '/auth/users/password');
    await page.locator('#saveAdminUserButton').click();
    check((await reset).status(), 200, 'eight-character local-user reset', 'securityChecks');
    await api(userContext, '/auth/session', { status: 401 });
    await login(userContext, resetPassword, testUsername);
  } finally { await userContext.close(); }

  currentStep = 'rotate current administrator password and revoke sessions';
  const secondSession = await browser.newContext();
  const oldCurrentSession = await browser.newContext();
  try {
    await login(secondSession, initialAdminPassword);
    await oldCurrentSession.addCookies(await context.cookies());
    await page.locator('#settingCurrentPassword').fill(initialAdminPassword);
    await page.locator('#settingPassword').fill('🔒'.repeat(7));
    await page.locator('#settingPasswordConfirm').fill('🔒'.repeat(7));
    await page.locator('#saveAdminPassword').click();
    check((await page.locator('#adminPasswordError').textContent()).includes('8'), true, 'UI rejects seven-character password rotation');
    await page.locator('#settingPassword').fill(rotatedPassword);
    await page.locator('#settingPasswordConfirm').fill(rotatedPassword);
    const rotation = mutationResponse(page, '/auth/password');
    const logoutNavigation = page.waitForNavigation({ waitUntil: 'networkidle' });
    logoutNavigation.catch(() => {});
    await page.locator('#settingPasswordConfirm').press('Enter');
    check((await rotation).status(), 200, 'eight-character password rotation', 'securityChecks');
    activeAdminPassword = rotatedPassword; passwordRotated = true;
    await api(secondSession, '/auth/session', { status: 401 });
    await api(oldCurrentSession, '/auth/session', { status: 401 });
    const status = await api(secondSession, '/auth/status');
    check([status.initialized, status.bootstrapRequired, status.recoveryRequired], [true, false, false], 'mandatory administrator identity remains initialized', 'securityChecks');
    currentStep = 'wait for the password-rotation reload';
    await logoutNavigation;
    await page.waitForFunction(() => document.getElementById('loginOverlay').style.display === 'flex');
    currentStep = 'sign in with the rotated eight-character password';
    await page.locator('#loginUsername').fill('admin');
    await page.locator('#loginPassword').fill(rotatedPassword);
    const loginResponse = mutationResponse(page, '/login');
    await page.locator('#loginPassword').press('Enter');
    let signedInResponse = await loginResponse;
    if (signedInResponse.status() === 429) {
      const retrySeconds = Number(signedInResponse.headers()['retry-after']);
      check(retrySeconds > 0 && retrySeconds <= 15, true, 'login throttle gives a bounded retry time', 'securityChecks');
      await new Promise(resolve => setTimeout(resolve, retrySeconds * 1000));
      await page.locator('#loginPassword').fill(rotatedPassword);
      const retryResponse = mutationResponse(page, '/login');
      await page.locator('#loginPassword').press('Enter');
      signedInResponse = await retryResponse;
    }
    check(signedInResponse.status(), 200, 'rotated password accepted by the login form (HTTP ' + signedInResponse.status() + ')', 'securityChecks');
    currentStep = 'verify the reauthenticated administrator console';
    await page.waitForFunction(() => document.getElementById('loginOverlay').style.display !== 'flex' && typeof _adminPrincipal !== 'undefined' && _adminPrincipal);
    await page.waitForLoadState('networkidle');
    check((await api(context, '/auth/session')).principal.method, 'local', 'rotated password signs in through the real login form', 'securityChecks');
  } finally { await secondSession.close(); await oldCurrentSession.close(); }
}

try {
  if (process.env.ROUTENEST_ACCESS_VERIFY_MUTATIONS !== '1') throw new VerificationFailure('Explicit ROUTENEST_ACCESS_VERIFY_MUTATIONS=1 is required for this isolated-instance test.');
  base = new URL(process.env.BASE_URL || 'http://127.0.0.1:3467/admin/');
  if (!['http:', 'https:'].includes(base.protocol) || !['127.0.0.1', '[::1]', 'localhost'].includes(base.hostname) || base.username || base.password || base.search || base.hash || base.pathname !== '/admin/') throw new VerificationFailure('BASE_URL must be a loopback administrator URL without credentials or query parameters.');
  initialAdminPassword = process.env.ADMIN_TEST_PASSWORD;
  if (!initialAdminPassword || Array.from(initialAdminPassword).length < 8) throw new VerificationFailure('ADMIN_TEST_PASSWORD must contain at least eight characters.');
  activeAdminPassword = initialAdminPassword;
  const require = createRequire(import.meta.url);
  const modules = process.env.PLAYWRIGHT_MODULE ? [process.env.PLAYWRIGHT_MODULE] : ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core'];
  let chromium;
  for (const module of modules) { try { ({ chromium } = require(module)); break; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; } }
  if (!chromium) throw new VerificationFailure('Set PLAYWRIGHT_MODULE to an existing Playwright installation.');
  currentStep = 'launch browser';
  browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined), args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  cleanupContext = await browser.newContext();
  currentStep = 'initialize verification administrator';
  const auth = await api(cleanupContext, '/auth/status');
  check([auth.recoveryRequired, auth.legacyRotationRequired], [false, false], 'fresh or initialized test identity only', 'securityChecks');
  if (auth.bootstrapRequired) {
    if (!process.env.ADMIN_TEST_BOOTSTRAP_SECRET) throw new VerificationFailure('ADMIN_TEST_BOOTSTRAP_SECRET is required for the fresh isolated instance.');
    await api(cleanupContext, '/auth/bootstrap', { method: 'POST', data: { secret: process.env.ADMIN_TEST_BOOTSTRAP_SECRET, password: initialAdminPassword } });
  } else { await login(cleanupContext, initialAdminPassword); }
  const configuration = await api(cleanupContext, '/config');
  const tempRelativePath = typeof configuration.poolPath === 'string' ? relative(resolve(tmpdir()), resolve(configuration.poolPath)) : '..';
  if (!isAbsolute(configuration.poolPath || '') || tempRelativePath.startsWith('..') || isAbsolute(tempRelativePath)) throw new VerificationFailure('Verification requires a separate data directory under the system temporary directory.');
  const originalUsers = (await api(cleanupContext, '/auth/users')).users;
  check(originalUsers.some(user => user.username === testUsername), false, 'disposable username is unused', 'securityChecks');
  const screenshotDir = process.env.ARTIFACT_DIR ? resolve(process.env.ARTIFACT_DIR) : null;
  if (screenshotDir) mkdirSync(screenshotDir, { recursive: true });
  await loginFormChecks(screenshotDir);

  for (const theme of ['light', 'dark']) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    try {
      currentStep = theme + ': sign in for Access Settings';
      await login(context, initialAdminPassword);
      collectDiagnostics = true;
      currentStep = theme + ': open Access Settings';
      const page = await openAccess(context, theme);
      for (const width of [1440, 1280, 1024, 768, 390, 360]) {
        currentStep = theme + '-' + width + ': access layout';
        process.stderr.write(currentStep + '\n');
        await page.setViewportSize({ width, height: 1000 });
        await page.evaluate(() => { closeAdminUserEditor(); closeSSOProviderEditor(); });
        await localeChecks(page, 'en'); await localeChecks(page, 'zh'); await localeChecks(page, 'en');
        await layout(page, currentStep);
        for (const protocol of ['oidc', 'saml']) {
          await page.evaluate(type => newSSOProvider(type), protocol);
          check(await page.locator(protocol === 'oidc' ? '#ssoOIDCAdvanced' : '#ssoSAMLAdvanced').evaluate(element => element.open), false, 'new ' + protocol + ' optional fields collapsed');
          check(await page.locator('#ssoRoleMappingDetails').evaluate(element => element.open), false, 'new group mapping collapsed');
          await layout(page, currentStep + ': ' + protocol + ' editor');
          if (protocol === 'oidc' && width === 1440) await keyboardChecks(page);
          if (screenshotDir) await captureEditor(page, resolve(screenshotDir, currentStep.split(':')[0] + '-' + protocol + '.png'));
        }
      }
      currentStep = theme + ': SSO payload and permission presentation';
      await ssoChecks(page);
      const principal = (await api(context, '/auth/session')).principal;
      await page.evaluate(() => renderAdminPrincipal({ userID: 'verification-federated', username: 'verification', method: 'oidc', provider: 'verify-oidc', roles: ['viewer'] }));
      check(await page.locator('#localAdminPasswordForm').isVisible(), false, 'federated principal has no local-password form');
      check(await page.locator('#ssoAdminPasswordNotice').isVisible(), true, 'federated password managed at identity provider');
      check(await page.locator('.access-side-stack').isVisible(), false, 'viewer cannot see security controls');
      await page.evaluate(principal => renderAdminPrincipal(principal), principal);
    } finally { collectDiagnostics = false; await context.close(); }
  }
  check(result.diagnostics, { consoleErrors: 0, pageErrors: 0, requestFailures: 0, externalRequests: 0 }, 'layout and SSO checks have no browser errors or external requests');
  currentStep = 'password lifecycle';
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  try { await login(context, initialAdminPassword); await passwordLifecycle(context, await openAccess(context)); }
  finally { await context.close(); }
} catch (error) {
  let detail = String(error.message || error).split('\n')[0];
  for (const secret of [initialAdminPassword, activeAdminPassword, userPassword, resetPassword, rotatedPassword, process.env.ADMIN_TEST_BOOTSTRAP_SECRET]) {
    if (secret) detail = detail.split(secret).join('<REDACTED>');
  }
  result.failures.push(error instanceof VerificationFailure ? detail : currentStep + ': ' + detail);
  process.exitCode = 1;
} finally {
  collectDiagnostics = false;
  if (cleanupContext) {
    try {
      currentStep = 'clean up disposable administrator';
      if (passwordRotated) await login(cleanupContext, activeAdminPassword);
      if (attemptedCreate) {
        const users = (await api(cleanupContext, '/auth/users')).users;
        const user = users.find(user => user.username === testUsername);
        if (user) await api(cleanupContext, '/auth/users/delete', { method: 'POST', data: { userID: user.id, revision: user.revision } });
      }
      if (passwordRotated) {
        currentStep = 'restore isolated administrator test password';
        await api(cleanupContext, '/auth/password', { method: 'POST', data: { oldPassword: activeAdminPassword, newPassword: initialAdminPassword } });
      }
    } catch { result.failures.push('Isolated-instance cleanup failed; discard its temporary data directory after stopping the test server.'); process.exitCode = 1; }
    await cleanupContext.close().catch(() => {});
  }
  if (browser) await browser.close().catch(() => {});
  process.stdout.write(JSON.stringify(result) + '\n');
}

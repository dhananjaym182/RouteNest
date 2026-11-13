import assert from 'node:assert/strict';
import { createHash, randomBytes } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, realpathSync, statSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const TABS = ['dashboard', 'providers', 'credentials', 'network', 'models', 'routing', 'combos', 'logs', 'quota', 'health', 'security', 'about', 'quickstart'];
const ALIASES = { global: 'providers', accounts: 'credentials', import: 'credentials', upstreams: 'network' };
const AUTH_COOKIE = 'cline_admin_session';
const CSRF_COOKIE = 'routenest_admin_csrf';
const TIMEOUT = 12000;
const HTTP_TIMEOUT = 15000;
const RUN_TIMEOUT = 600000;
const FOCUSABLE = 'a[href],button,input:not([type="hidden"]),select,textarea,summary,[tabindex]';
const CODE_SELECTORS = ['#issuedRecoveryCode', '#passwordRecoveryReplacementCode'];
const KNOWN_ERRORS = new Set(['authentication_failed', 'login_required', 'session_expired', 'csrf_invalid', 'same_origin_required', 'desktop_handoff_unavailable', 'permission_denied', 'rate_limited', 'security_restart_required', 'security_unavailable', 'security_degraded', 'security_state_invalid', 'method_not_allowed', 'not_found']);
const AUTH_PATHS = new Set(['/login', '/logout', '/auth/status', '/auth/bootstrap', '/auth/session', '/auth/password', '/auth/recovery-code', '/auth/recovery', '/auth/desktop-handoff', '/auth/desktop-signout', '/auth/trusted-devices', '/auth/trusted-devices/revoke', '/auth/trusted-devices/revoke-all', '/auth/sessions', '/auth/sessions/revoke', '/auth/users', '/auth/users/delete', '/auth/audit', '/auth/sso/providers']);
const AUXILIARY_PATHS = new Set(['/models', '/models/delistings', '/models/catalog', '/providers', '/connections', '/global', '/accounts', '/stats', '/dashboard', '/dashboard/overview', '/dashboard/activity', '/onboarding', '/onboarding/status', '/quota', '/version-check', '/opencode/config', '/cline/egress', '/cline/proxy', '/request-logs', '/combos']);

class VerificationFailure extends Error {}

export function validateBase(value) {
  let url;
  try { url = new URL(value); } catch { throw new VerificationFailure('BASE_URL must be an explicit loopback /admin/ URL.'); }
  if (!['http:', 'https:'].includes(url.protocol) || !['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname) || url.username || url.password || url.search || url.hash || url.pathname !== '/admin/') throw new VerificationFailure('BASE_URL must be a loopback /admin/ URL without credentials, query, or fragment.');
  return url;
}

export function validPassword(value) {
  return typeof value === 'string' && Array.from(value).length >= 8 && Buffer.byteLength(value, 'utf8') <= 1024 && value.trim().length > 0;
}

export function strictDescendant(root, path) {
  if (!isAbsolute(path)) return false;
  const part = relative(resolve(root), resolve(path));
  return part !== '' && part !== '..' && !part.startsWith('..' + sep) && !isAbsolute(part);
}

export function isolatedPool(path) {
  if (typeof path !== 'string' || !isAbsolute(path)) return false;
  try {
    const root = realpathSync(tmpdir());
    return strictDescendant(root, realpathSync(dirname(path))) && strictDescendant(root, realpathSync(path)) && statSync(path).isFile();
  } catch { return false; }
}

export function requestHeaders(origin, cookies, { originMode = 'same', csrfMode = 'valid', method = 'POST' } = {}) {
  const headers = {};
  if (originMode !== 'missing') headers.Origin = originMode === 'same' ? origin : originMode === 'null' ? 'null' : 'https://foreign-origin.invalid';
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfMode !== 'missing') {
    const token = cookies.find(cookie => cookie.name === CSRF_COOKIE)?.value;
    if (csrfMode === 'wrong') headers['X-RouteNest-CSRF'] = 'intentionally-invalid-csrf';
    else if (token) headers['X-RouteNest-CSRF'] = token;
  }
  return headers;
}

export function parseColor(value) {
  const text = String(value).trim().toLowerCase();
  if (text === 'transparent') return [0, 0, 0, 0];
  if (/^#[0-9a-f]{6}([0-9a-f]{2})?$/.test(text)) return [1, 3, 5].map(i => parseInt(text.slice(i, i + 2), 16) / 255).concat(text.length === 9 ? parseInt(text.slice(7), 16) / 255 : 1);
  const srgb = text.match(/^color\(srgb\s+(.+)\)$/);
  const rgb = text.match(/^rgba?\((.+)\)$/);
  if (!srgb && !rgb) return null;
  const parts = (srgb || rgb)[1].replace(/[,/]/g, ' ').trim().split(/\s+/);
  if (parts.length < 3 || parts.length > 4) return null;
  const numbers = parts.map((part, i) => parseFloat(part) / (part.endsWith('%') ? 100 : i < 3 && !srgb ? 255 : 1));
  if (numbers.some(number => !Number.isFinite(number) || number < 0 || number > 1)) return null;
  return [...numbers.slice(0, 3), numbers[3] ?? 1];
}

export function composite(foreground, background) {
  const alpha = foreground[3] + background[3] * (1 - foreground[3]);
  if (!alpha) return [0, 0, 0, 0];
  return foreground.slice(0, 3).map((value, i) => (value * foreground[3] + background[i] * background[3] * (1 - foreground[3])) / alpha).concat(alpha);
}

export function contrastRatio(foreground, background) {
  const luminance = color => color.slice(0, 3).map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4).reduce((sum, value, i) => sum + value * [0.2126, 0.7152, 0.0722][i], 0);
  const a = luminance(foreground), b = luminance(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

export function computedContrast(sample) {
  if (sample.unsupported) return null;
  const foreground = parseColor(sample.foreground), layers = sample.backgrounds.map(parseColor);
  if (!foreground || layers.some(layer => !layer) || !layers.some(layer => layer[3] === 1)) return null;
  let background = [1, 1, 1, 1];
  for (const layer of layers.reverse()) background = composite(layer, background);
  return contrastRatio(composite(foreground, background), background);
}

export function redactedText(value, secrets = []) {
  let text = String(value);
  for (const secret of [...secrets].filter(Boolean).sort((a, b) => b.length - a.length)) text = text.split(secret).join('[redacted]');
  return text.replace(/\b[A-Za-z0-9_-]{40,}\b/g, '[redacted-token]').replace(/(?:cookie|authorization|x-routenest-csrf)\s*[:=][^\n]*/gi, '[redacted-header]');
}

export function requestCategory(url, origin) {
  try {
    const parsed = new URL(url);
    if (!['http:', 'https:'].includes(parsed.protocol)) return 'embedded';
    if (parsed.origin !== origin) return 'external';
    const path = parsed.pathname.replace(/^\/admin\/api/, '');
    if (AUTH_PATHS.has(path) || path.startsWith('/auth/')) return 'authentication';
    if (AUXILIARY_PATHS.has(path) || /^\/(models|providers|opencode|cline|dashboard|request-logs)(\/|$)/.test(path)) return 'auxiliary';
    return 'other';
  } catch { return 'other'; }
}

export function retryDelay(header) {
  if (!/^\d+$/.test(String(header))) return null;
  const seconds = Number(header);
  return seconds >= 1 && seconds <= 15 ? seconds * 1000 + 100 : null;
}

function maskScreenshotDOM(secrets) {
  if (window.__securityScreenshotRestore) throw new Error('screenshot_mask_already_active');
  const restores = [];
  const mask = text => {
    let output = String(text);
    for (const secret of secrets) if (secret) output = output.split(secret).join('•'.repeat(Array.from(secret).length));
    return output.replace(/\b[A-Za-z0-9_-]{40,}\b/g, match => '•'.repeat(match.length));
  };
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) {
    const node = walker.currentNode;
    if (['SCRIPT', 'STYLE', 'NOSCRIPT'].includes(node.parentElement?.tagName)) continue;
    const original = node.nodeValue;
    const code = node.parentElement?.closest('#issuedRecoveryCode,#passwordRecoveryReplacementCode');
    const replacement = code && original.trim() ? '•'.repeat(Array.from(original).length) : mask(original);
    if (replacement !== original) { restores.push(() => { node.nodeValue = original; }); node.nodeValue = replacement; }
  }
  for (const input of document.querySelectorAll('input,textarea')) {
    const original = input.value;
    const replacement = input.type === 'password' ? '' : mask(original);
    if (replacement !== original) { restores.push(() => { input.value = original; }); input.value = replacement; }
  }
  window.__securityScreenshotRestore = () => { for (const restore of restores) restore(); delete window.__securityScreenshotRestore; };
  return restores.length;
}

function restoreScreenshotDOM() {
  if (typeof window.__securityScreenshotRestore !== 'function') throw new Error('screenshot_mask_missing');
  window.__securityScreenshotRestore();
}

export async function withScreenshotMask(page, secrets, action) {
  await page.evaluate(maskScreenshotDOM, [...secrets].filter(Boolean));
  try { return await action(); }
  finally { await page.evaluate(restoreScreenshotDOM); }
}

const AUTH_HEADER_CONTRAST = ['#adminAuthPage .auth-wordmark', '#adminAuthPage .auth-header [data-theme-opt]', '#adminAuthPage .auth-header [data-lang-opt]'];
const CONTRAST = {
  login: [...AUTH_HEADER_CONTRAST, '#loginTitle', '#loginHelp', '#loginLocalHelp', 'label[for="loginUsername"]', 'label[for="loginPassword"]', '#loginPassword', '#browserLoginNotice', '#loginSubmit', '#forgotPasswordButton', '#loginError'],
  security: ['.access-intro h2', '#accessAccountTitle', '#adminIdentity', '#adminAuthMethod', '#passwordStatus', '#adminPasswordHelp', '#adminPasswordError', 'label[for="settingCurrentPassword"]', '#saveAdminPassword', '#recoveryCodeStatus', '#issueRecoveryCodeButton', '#recoveryCodeIssueError', '#desktopSignInTitle', '#desktopAutoSignInStatus', '#desktopThisDeviceStatus', '#desktopCredentialStorageStatus', '#desktopSignInGuidance', '#desktopSignInPasswordHelp', '#desktopSignInError', '#trustedDevicesList .access-empty', '#accessSessionsTitle', '#adminSessionsList .access-row-meta', '#issuedRecoveryCode'],
  'recovery-form': [...AUTH_HEADER_CONTRAST, '#passwordRecoveryBack', '#passwordRecoveryTitle', '#passwordRecoveryHelp', '#passwordRecoveryForm label', '#passwordRecoveryCode', '#passwordRecoverySubmit', '#passwordRecoveryError'],
  'recovery-result': [...AUTH_HEADER_CONTRAST, '#passwordRecoveryResultTitle', '#passwordRecoveryResultView > p:not([hidden])', '#passwordRecoveryReplacementCode', '#passwordRecoveryResultView .recovery-confirm span', '#passwordRecoveryFinish'],
};
const SCOPES = { login: '.login-card', security: '#tab-security', 'recovery-form': '.recovery-dialog', 'recovery-result': '.recovery-dialog' };

export async function runVerification(environment = process.env) {
  const report = {
    evidence: 'real HTTP service + headless Chromium browser; no authentication API mocks; not Wails or OS-keyring runtime',
    counters: { securityChecks: 0, httpChecks: 0, uiChecks: 0, navigationChecks: 0, aliasChecks: 0, accessibilityChecks: 0, contrastSamples: 0, reflowCells: 0, zoomCells: 0, screenshots: 0 },
    diagnostics: { consoleErrors: 0, pageErrors: 0, requestFailures: 0, externalRequests: 0, auxiliaryReadDeadlineHits: 0, auxiliaryHTTPFailures: 0, authenticationHTTPFailures: 0, otherHTTPFailures: 0, expectedNegativeResponses: 0, throttleRetries: 0, throttleWaitMs: 0, contrastUnsupported: 0, minimum24Targets: 0, spacing24Targets: 0, comfortable44Targets: 0, pageErrorCategories: {}, failedResponseCategories: {} },
    inventory: [], screenshotLabels: [], failures: [],
    coverage: {
      isolatedTemporaryPool: false, bootstrap: 'not attempted', browserPasswordLogins: 0,
      nativeDeviceTransitions: 'Not exercised: ordinary HTTP browsers cannot enroll or use OS credentials. Native device transitions are covered separately by Go tests, not this browser run; this harness does not run or certify those tests.',
      nativeHandoff: { fabricatedTokenProbes: 0, validTokenProbes: 0, validTokenLimitation: 'No native-issued token is available to an ordinary HTTP browser; fabricated tokens are not valid-token evidence.' },
      loginResponse: 'Immediate UI reload can evict the login response body in Chromium. Login requires HTTP 200 plus a fresh authenticated session API response and authenticated principal/owner DOM, never visibility or status alone.',
      deviceFixtures: 'none; a nonempty initial device list is rejected',
      nativeErrorPresentation: 'Browser-only production guard invoked directly to inspect its real error presentation; no native transition or OS failure is simulated.',
      navigation: '13 canonical sidebar destinations and four aliases at 1440/light/EN and 390/dark/ZH, plus signed-out #login/#recover refresh and real back/forward; comprehensive authenticated history/font/width coverage remains in the navigation harness',
      accessibility: 'Targeted computed sRGB text contrast, semantics, target size/spacing, keyboard page flow, reduced-motion and reflow checks; not a WCAG conformance audit.',
      zoom: '200%/400% desktop equivalent: effective layout viewport 720/360 CSS px with Chromium device scale 2/4, verified from innerWidth, DPR and layout; not native browser-menu zoom.',
      auxiliaryErrors: 'Provider/model/network/update reads are diagnostic-only unless authentication, route ownership or tested security UI fails. No provider mutation controls are used; server-side upstream egress is not observable here.',
      cleanup: 'not attempted', recoveryProof: 'not issued', authRecoveryRequestsBeforeSubmit: 0,
    },
  };
  const secrets = new Set([environment.ADMIN_TEST_PASSWORD, environment.ADMIN_TEST_BOOTSTRAP_SECRET].filter(Boolean));
  const contexts = new Set(), tracked = new Map(), pendingReads = new WeakMap();
  let browser, base, artifactDir, deadlineTimer, currentStep = 'configuration', expired = false;
  let activePassword = environment.ADMIN_TEST_PASSWORD, passwordKnown = true, changedPassword = false, restartRequired = false;
  let primary, setup, viewer, disposableUser, userCreationAttempted = false, completed = false, axeSource = null;
  const initialPassword = activePassword;
  const viewerName = 'security-journey-' + randomBytes(6).toString('hex');
  const newPassword = () => { const password = randomBytes(18).toString('base64url'); secrets.add(password); return password; };
  const viewerPassword = newPassword(), changed = newPassword(), recovered = newPassword(), replacementReset = newPassword(), wrongPassword = newPassword();
  const diagnosticMessage = error => {
    const raw = String(error?.message || '').split('\n')[0].slice(0, 500);
    const safe = redactedText(raw, secrets).replace(/https?:\/\/[^\s)]+/gi, '[redacted-url]');
    return safe || 'no exception message';
  };
  const fail = (label, category = 'securityChecks', fatal = false, detail) => {
    report.counters[category]++;
    report.failures.push({ step: currentStep, check: label, ...(detail ? { detail } : {}) });
    if (fatal) throw new VerificationFailure(label);
  };
  const check = (condition, label, category = 'securityChecks', fatal = false, detail) => {
    if (!condition) { fail(label, category, fatal, detail); return false; }
    report.counters[category]++;
    return true;
  };
  const equal = (actual, expected, label, category = 'securityChecks', fatal = false) => {
    let ok = false;
    try { assert.deepEqual(actual, expected); ok = true; } catch {}
    return check(ok, label, category, fatal);
  };
  const rememberCookies = async context => {
    const cookies = await context.cookies();
    for (const cookie of cookies) if ([AUTH_COOKIE, CSRF_COOKIE].includes(cookie.name)) secrets.add(cookie.value);
    return cookies;
  };
  const decode = async response => {
    let envelope;
    try { envelope = await response.json(); } catch { envelope = null; }
    if (envelope?.data?.recoveryCode) secrets.add(envelope.data.recoveryCode);
    return { status: response.status(), envelope, retry: response.headers()['retry-after'] };
  };
  const waitThrottle = async reply => {
    const delay = retryDelay(reply.retry);
    check(delay !== null && report.diagnostics.throttleWaitMs + delay <= 180000, 'rate limit supplies a bounded Retry-After within the total retry budget', 'securityChecks', true);
    report.diagnostics.throttleRetries++;
    report.diagnostics.throttleWaitMs += delay;
    await new Promise(resolve => setTimeout(resolve, delay));
  };
  const http = async (context, path, options = {}) => {
    const { method = 'GET', data, originMode = 'same', csrfMode = 'valid', retry = false } = options;
    for (let attempt = 0; ; attempt++) {
      const cookies = await rememberCookies(context);
      const response = await context.request.fetch(new URL('/admin/api' + path, base).href, { method, data, headers: requestHeaders(base.origin, cookies, { method, originMode, csrfMode }), timeout: HTTP_TIMEOUT, maxRetries: 0, maxRedirects: 0, failOnStatusCode: false });
      report.counters.httpChecks++;
      const reply = await decode(response);
      await response.dispose();
      await rememberCookies(context);
      if (reply.status !== 429 || !retry || attempt >= 2) return reply;
      await waitThrottle(reply);
    }
  };
  const success = (reply, label, statuses = [200]) => {
    const errorCode = KNOWN_ERRORS.has(reply.envelope?.error) ? reply.envelope.error : 'redacted-or-unrecognized';
    check(statuses.includes(reply.status) && reply.envelope?.success === true, label, 'securityChecks', true, { status: reply.status, errorCode });
    return reply.envelope?.data;
  };
  const rejected = (reply, statuses, label, code) => {
    check(statuses.includes(reply.status) && reply.envelope?.success !== true && (!code || reply.envelope?.error === code), label, 'securityChecks', true, { status: reply.status });
    report.diagnostics.expectedNegativeResponses++;
  };
  const apiLogin = async (context, password, username = 'admin') => success(await http(context, '/login', { method: 'POST', data: { username, password }, retry: true }), 'real HTTP password login succeeds');
  const session = async (context, authenticated, label) => {
    const reply = await http(context, '/auth/session');
    if (!authenticated) { rejected(reply, [401], label); return null; }
    const data = success(reply, label);
    check(data?.principal?.method === 'local' && !data.principal.deviceID, 'password session is local and not a desktop-device session', 'securityChecks', true);
    return data.principal;
  };
  const track = async (context, name, userID = 'break-glass') => {
    const principal = await session(context, true, name + ': authenticated session from server');
    equal(principal.userID, userID, name + ': expected server identity', 'securityChecks', true);
    const cookie = (await rememberCookies(context)).find(item => item.name === AUTH_COOKIE);
    check(!!cookie?.value && cookie.httpOnly && cookie.sameSite === 'Lax' && cookie.path === '/admin', 'session cookie has expected HttpOnly/SameSite/path attributes', 'securityChecks', true);
    const csrf = (await rememberCookies(context)).find(item => item.name === CSRF_COOKIE);
    check(!!csrf?.value && !csrf.httpOnly && csrf.sameSite === 'Strict' && csrf.path === '/admin', 'CSRF cookie has expected separate SameSite/path attributes', 'securityChecks', true);
    check(base.protocol !== 'https:' || cookie.secure && csrf.secure, 'HTTPS credentials use Secure cookies', 'securityChecks', true);
    const hash = createHash('sha256').update(cookie.value).digest('hex');
    secrets.add(hash);
    tracked.set(name, { context, hash, userID });
    return hash;
  };
  const newContext = async (options = {}) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'en-US', reducedMotion: 'reduce', serviceWorkers: 'block', ...options });
    contexts.add(context);
    await context.addInitScript(() => {
      localStorage.setItem('cline_admin_theme', 'light');
      localStorage.setItem('cline_admin_lang', 'en');
      localStorage.setItem('cline_admin_quickstart_seen', '1');
    });
    return context;
  };
  const attach = page => {
    const reads = new Set();
    pendingReads.set(page, reads);
    const readFinished = request => reads.delete(request);
    page.setDefaultTimeout(TIMEOUT);
    page.setDefaultNavigationTimeout(HTTP_TIMEOUT);
    page.on('console', message => { if (message.type() === 'error') report.diagnostics.consoleErrors++; });
    page.on('pageerror', error => {
      report.diagnostics.pageErrors++;
      const category = /model|provider|upstream|network|quota|version/i.test(String(error.message)) ? 'auxiliary-indication' : 'unclassified';
      report.diagnostics.pageErrorCategories[category] = (report.diagnostics.pageErrorCategories[category] || 0) + 1;
    });
    page.on('requestfailed', request => { report.diagnostics.requestFailures++; readFinished(request); });
    page.on('requestfinished', readFinished);
    page.on('request', request => {
      if (requestCategory(request.url(), base.origin) === 'external') report.diagnostics.externalRequests++;
      if (request.method() === 'GET' && new URL(request.url()).origin === base.origin && /^\/admin\/api\/(?:models(?:\/|$)|config$)/.test(new URL(request.url()).pathname)) reads.add(request);
    });
    page.on('response', response => {
      if (response.status() < 400) return;
      const category = requestCategory(response.url(), base.origin);
      const key = category + ':' + response.status();
      report.diagnostics.failedResponseCategories[key] = (report.diagnostics.failedResponseCategories[key] || 0) + 1;
      if (category === 'auxiliary') report.diagnostics.auxiliaryHTTPFailures++;
      else if (category === 'authentication') report.diagnostics.authenticationHTTPFailures++;
      else report.diagnostics.otherHTTPFailures++;
    });
  };
  const open = async (context, hash = 'login') => {
    const page = await context.newPage();
    attach(page);
    await page.goto(new URL('#' + hash, base).href, { waitUntil: 'domcontentloaded' });
    await page.waitForFunction(() => typeof nativeDesktopAvailable === 'function' && typeof _adminAuthStatus !== 'undefined' && _adminAuthStatus !== null);
    check(await page.evaluate(() => !window.go?.main?.DesktopSetupBridge && !window.runtime && !nativeDesktopAvailable()), 'ordinary Chromium has no native desktop bridge or Wails runtime', 'securityChecks', true);
    return page;
  };
  const waitAuthView = async (page, expected) => {
    await page.waitForFunction(view => document.documentElement.dataset.adminAuthView === view, expected);
    await page.locator(expected === 'login' ? '#loginOverlay' : expected === 'recover' ? '#passwordRecoveryFormView' : '#passwordRecoveryResultView').waitFor({ state: 'visible' });
  };
  const waitLogin = async page => {
    await page.waitForFunction(() => document.documentElement.dataset.adminAuth === 'signed-out' && document.documentElement.dataset.adminAuthView === 'login' && location.hash === '#login' && typeof _adminAuthMode !== 'undefined' && _adminAuthMode === 'login' && !document.getElementById('loginPassword').disabled);
    await page.locator('#loginForm').waitFor({ state: 'visible' });
    check(await page.evaluate(() => _adminPrincipal === null && getComputedStyle(document.querySelector('.layout')).display === 'none'), 'standalone login page hides the dashboard and has no authenticated principal', 'uiChecks');
    check(await page.evaluate(({ tabs, aliases }) => {
      const state = history.state;
      return !state || Object.keys(state).every(key => key === 'routenestAuthReturnTab') && (state.routenestAuthReturnTab === undefined || tabs.includes(state.routenestAuthReturnTab) || aliases.includes(state.routenestAuthReturnTab));
    }, { tabs: TABS, aliases: Object.keys(ALIASES) }), 'signed-out history state contains only an optional canonical/alias return destination', 'securityChecks');
  };
  const waitAuthenticated = async page => {
    await page.waitForFunction(() => document.documentElement.dataset.adminAuth === 'authenticated' && typeof _adminPrincipal !== 'undefined' && _adminPrincipal?.userID === 'break-glass' && getComputedStyle(document.getElementById('adminAuthPage')).display === 'none');
    await settleBootReads(page);
    await page.waitForFunction(() => !document.querySelector('.layout').closest('[inert]'));
    await session(page.context(), true, 'authenticated DOM is backed by a live HTTP session');
  };
  const settleBootReads = async page => {
    const deadline = Date.now() + 3000;
    while (pendingReads.get(page)?.size && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 50));
    if (pendingReads.get(page)?.size) report.diagnostics.auxiliaryReadDeadlineHits++;
    await dismissNotice(page);
  };
  const uiMutation = async (page, path, fill, trigger, retry = true) => {
    await settleBootReads(page);
    for (let attempt = 0; ; attempt++) {
      await dismissNotice(page);
      await fill();
      await dismissNotice(page);
      const waiting = page.waitForResponse(response => new URL(response.url()).origin === base.origin && new URL(response.url()).pathname === '/admin/api' + path && response.request().method() === 'POST', { timeout: HTTP_TIMEOUT }).then(response => {
        report.counters.httpChecks++;
        return decode(response);
      });
      waiting.catch(() => {});
      await trigger();
      const reply = await waiting;
      await rememberCookies(page.context());
      if (reply.status !== 429 || !retry || attempt >= 2) return reply;
      await waitThrottle(reply);
    }
  };
  const uiLogin = async (page, password, accepted = true) => {
    await waitLogin(page);
    const returnRoute = await page.evaluate(() => history.state?.routenestAuthReturnTab || 'dashboard');
    const reply = await uiMutation(page, '/login', async () => {
      await page.locator('#loginUsername').fill('admin');
      await page.locator('#loginPassword').fill(password);
    }, () => page.locator('#loginSubmit').click());
    if (!accepted) {
      rejected(reply, [401], 'old password is rejected through the actual login form', 'authentication_failed');
      await page.waitForFunction(() => !!document.getElementById('loginError').textContent.trim() && !document.getElementById('loginSubmit').disabled);
      await session(page.context(), false, 'failed password login does not establish authority');
      return;
    }
    check(reply.status === 200 && (reply.envelope === null || reply.envelope.success === true), 'password submitted through the actual login form is accepted by the real HTTP endpoint', 'securityChecks', true, { status: reply.status });
    await waitAuthenticated(page);
    check(await page.evaluate(route => location.hash === '#' + route, returnRoute), 'successful login restores the remembered non-secret return destination', 'navigationChecks');
    report.coverage.browserPasswordLogins++;
  };
  const dismissNotice = async page => {
    if (await page.locator('#modelSyncOverlay').isVisible()) await page.evaluate(() => closeModelSyncModal());
  };
  const browserTrustHidden = async page => {
    const state = await page.evaluate(() => {
      const ids = ['desktopTrustLoginRow', 'trustThisDeviceButton', 'toggleDesktopAutoSignInButton', 'forgetThisDeviceButton'];
      return ids.map(id => {
        const element = document.getElementById(id);
        return element && element.hidden && getComputedStyle(element).display === 'none' && element.getClientRects().length === 0;
      });
    });
    check(state[0], 'browser login trust option is genuinely hidden, not merely unchecked', 'securityChecks');
    if (await page.locator('#desktopSignInCard').isVisible()) check(state.slice(1).every(Boolean), 'native trust/toggle/forget controls are genuinely hidden in authenticated browser security', 'securityChecks');
  };
  const selected = async (page, expected) => {
    await page.waitForFunction(tab => location.hash === '#' + tab && document.getElementById('tab-' + tab)?.getClientRects().length > 0, expected);
    const state = await page.evaluate(() => {
      const visible = element => element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden';
      const nav = [...document.querySelectorAll('.nav-item[data-tab]')];
      return {
        panels: [...document.querySelectorAll('.tab-panel')].filter(visible).map(element => element.id),
        active: nav.filter(element => element.classList.contains('active')).map(element => element.dataset.tab),
        current: nav.filter(element => element.getAttribute('aria-current') === 'page').map(element => element.dataset.tab),
        stale: nav.some(element => element.hasAttribute('aria-current') && element.getAttribute('aria-current') !== 'page'),
        hash: location.hash, titleMatches: document.getElementById('currentPageTitle').textContent.trim() === nav.find(element => element.classList.contains('active'))?.querySelector('.nav-label')?.textContent.trim(),
      };
    });
    equal(state.panels, ['tab-' + expected], 'canonical route owns the sole visible panel', 'navigationChecks');
    equal(state.active, [expected], 'canonical route owns the sole active sidebar button', 'navigationChecks');
    equal(state.current, [expected], 'canonical route owns the sole aria-current button', 'navigationChecks');
    check(!state.stale && state.hash === '#' + expected && state.titleMatches, 'route hash, shell title and navigation agree', 'navigationChecks');
    await session(page.context(), true, 'route selection retains authenticated server authority');
  };
  const inventory = async (context, label, page) => {
    const sessions = success(await http(context, '/auth/sessions'), label + ': real session inventory')?.sessions;
    const devices = success(await http(context, '/auth/trusted-devices'), label + ': real device inventory')?.devices;
    check(Array.isArray(sessions) && Array.isArray(devices), 'session and device inventories are arrays', 'securityChecks', true);
    for (const item of [...sessions, ...devices]) if (item.id) secrets.add(item.id);
    const ids = new Set(sessions.map(item => item.id));
    const known = new Set([...tracked.values()].map(item => item.hash));
    const row = { label, sessions: sessions.length, breakGlassSessions: sessions.filter(item => item.userID === 'break-glass').length, otherUserSessions: sessions.filter(item => item.userID !== 'break-glass').length, trackedSessionsPresent: [...ids].filter(id => known.has(id)).length, untrackedSessions: [...ids].filter(id => !known.has(id)).length, devices: devices.length, seededDevices: 0 };
    report.inventory.push(row);
    check(sessions.filter(item => item.current).length === 1, 'session inventory marks exactly one current session', 'securityChecks');
    check(devices.length === 0, 'unseeded browser journey has zero registered desktop devices', 'securityChecks', true);
    check(devices.every(item => !Object.keys(item).some(key => /secret|token|password|hash/i.test(key))), 'device projection contains no credential/verifier fields', 'securityChecks');
    if (page) {
      await dismissNotice(page);
      const activeSub = await page.locator('#tab-security .page-subtabs[data-page="security"] .subtab.active').getAttribute('data-sub') || 'identity';
      if (activeSub !== 'devices') await page.locator('#tab-security .page-subtabs[data-page="security"] .subtab[data-sub="devices"]').click();
      await page.locator('#tab-security .page-subpage[data-subpage="devices"]').waitFor({ state: 'visible' });
      await page.locator('#tab-security [onclick="loadAdminSessions()"]').click();
      await page.locator('#desktopSignInCard [onclick="loadTrustedDevices()"]').click();
      await page.waitForFunction(count => document.querySelectorAll('#adminSessionsList .access-list-row').length === count && typeof _trustedDesktopState !== 'undefined' && _trustedDesktopState?.devicesKnown === true, sessions.length);
      equal(await page.locator('#adminSessionsList .access-list-row').count(), sessions.length, 'security UI accounts for every returned session', 'uiChecks');
      equal(await page.locator('#trustedDevicesList [role="listitem"]').count(), devices.length, 'security UI accounts for every returned device', 'uiChecks');
      check(await page.locator('#trustedDevicesList .access-empty').isVisible(), 'zero devices is an explicit loaded empty state', 'uiChecks');
      await browserTrustHidden(page);
      if (activeSub !== 'devices') {
        await page.locator('#tab-security .page-subtabs[data-page="security"] .subtab[data-sub="' + activeSub + '"]').click();
        await page.locator('#tab-security .page-subpage[data-subpage="' + activeSub + '"]').waitFor({ state: 'visible' });
      }
    }
    return { sessions, devices, ids };
  };
  const screenshot = async (page, label) => {
    await dismissNotice(page);
    await rememberCookies(page.context());
    const saved = await page.evaluate(() => ['issuedRecoveryCode', 'passwordRecoveryReplacementCode'].map(id => document.getElementById(id)?.textContent || ''));
    saved.filter(Boolean).forEach(value => secrets.add(value));
    await withScreenshotMask(page, secrets, () => page.screenshot({ path: resolve(artifactDir, label + '.png'), animations: 'disabled', fullPage: false, timeout: HTTP_TIMEOUT, mask: [page.locator('input[type="password"]'), ...CODE_SELECTORS.map(selector => page.locator(selector))] }));
    const restored = await page.evaluate(values => ['issuedRecoveryCode', 'passwordRecoveryReplacementCode'].every((id, i) => (document.getElementById(id)?.textContent || '') === values[i]), saved);
    check(restored, 'displayed recovery values are restored after redacted screenshot', 'securityChecks', true);
    report.counters.screenshots++;
    report.screenshotLabels.push(label);
  };
  const preference = async (page, theme, language) => {
    await page.evaluate(({ theme, language }) => { setTheme(theme); setLang(language); }, { theme, language });
    await page.waitForFunction(({ theme, language }) => document.documentElement.dataset.theme === theme && document.documentElement.lang === (language === 'en' ? 'en' : 'zh-CN'), { theme, language });
    await dismissNotice(page);
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  const contrast = async (page, surface) => {
    const settled = await page.evaluate(async selectors => {
      const elements = selectors.flatMap(selector => [...document.querySelectorAll(selector)]).filter(element => element.getClientRects().length && !element.closest('[hidden],[inert]'));
      let previous = '', stable = 0;
      for (let attempt = 0; attempt < 24; attempt++) {
        const signature = elements.map(element => { const style = getComputedStyle(element); return [style.color, style.backgroundColor, style.opacity].join('|'); }).join(';');
        stable = signature === previous ? stable + 1 : 0;
        previous = signature;
        if (attempt >= 6 && stable >= 4 && elements.every(element => element.getAnimations().every(animation => animation.playState !== 'running'))) return true;
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      return false;
    }, CONTRAST[surface]);
    check(settled, surface + ': computed colors settle before contrast sampling', 'accessibilityChecks');
    const samples = await page.evaluate(selectors => selectors.flatMap(selector => [...document.querySelectorAll(selector)].filter(element => element.getClientRects().length && !element.closest('[hidden],[inert]') && !element.disabled && getComputedStyle(element).visibility !== 'hidden' && (element.textContent.trim() || element.tagName === 'INPUT')).map(element => {
      const style = getComputedStyle(element), backgrounds = [];
      let unsupported = false;
      for (let node = element; node; node = node.parentElement) {
        const computed = getComputedStyle(node);
        if (computed.backgroundImage !== 'none' || Number(computed.opacity) !== 1 || computed.mixBlendMode !== 'normal' || computed.filter !== 'none') unsupported = true;
        backgrounds.push(computed.backgroundColor);
        if (/^rgb\(/.test(computed.backgroundColor) || /,\s*1\)$/.test(computed.backgroundColor)) break;
      }
      return { selector, foreground: style.color, backgrounds, unsupported, fontSize: parseFloat(style.fontSize), fontWeight: parseFloat(style.fontWeight) || 400 };
    })), CONTRAST[surface]);
    check(samples.length >= 3, surface + ': meaningful visible contrast samples exist', 'accessibilityChecks');
    for (const sample of samples) {
      const ratio = computedContrast(sample);
      if (ratio === null) { report.diagnostics.contrastUnsupported++; continue; }
      report.counters.contrastSamples++;
      const minimum = sample.fontSize >= 24 || sample.fontSize >= 18.6667 && sample.fontWeight >= 700 ? 3 : 4.5;
      check(ratio >= minimum, surface + ': computed text contrast ' + sample.selector, 'accessibilityChecks', false, { ratio: Number(ratio.toFixed(2)), minimum });
    }
  };
  const semantics = async (page, surface) => {
    const selectors = {
      login: { alerts: ['#loginError'], status: [] },
      security: { alerts: ['#adminPasswordError', '#recoveryCodeIssueError', '#desktopSignInError'], status: ['#desktopSignInGuidance', '#trustedDevicesList', '#adminSessionsList', '.desktop-signin-status'] },
      'recovery-form': { alerts: ['#passwordRecoveryError'], status: [] },
      'recovery-result': { alerts: [], status: [] },
    }[surface];
    const state = await page.evaluate(({ alerts, status, scope }) => {
      const root = document.querySelector(scope);
      return {
        alerts: alerts.every(selector => document.querySelector(selector)?.getAttribute('role') === 'alert'),
        status: status.every(selector => { const element = document.querySelector(selector); return element && (element.getAttribute('role') === 'status' || ['polite', 'assertive'].includes(element.getAttribute('aria-live'))); }),
        described: [...root.querySelectorAll('input[aria-describedby]')].every(input => input.getAttribute('aria-describedby').split(/\s+/).every(id => !!document.getElementById(id))),
      };
    }, { ...selectors, scope: SCOPES[surface] });
    check(state.alerts, surface + ': error areas retain alert semantics', 'accessibilityChecks');
    check(state.status, surface + ': dynamic security status/list areas have live semantics', 'accessibilityChecks');
    check(state.described, surface + ': field error/help associations resolve to DOM elements', 'accessibilityChecks');
  };
  const keyboard = async (page, ids) => {
    await page.locator('#' + ids[0]).focus();
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    for (const id of ids.slice(1)) {
      const before = await page.locator('#' + id).evaluate(element => { const style = getComputedStyle(element); return { shadow: style.boxShadow, border: style.borderColor }; });
      await page.keyboard.press('Tab');
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const state = await page.locator('#' + id).evaluate((element, before) => {
        const style = getComputedStyle(element);
        const outline = parseFloat(style.outlineWidth) >= 2 && !['none', 'hidden'].includes(style.outlineStyle);
        const ring = style.boxShadow !== 'none' && style.boxShadow !== before.shadow;
        const border = style.borderColor !== before.border;
        return { focused: document.activeElement === element, indicator: element.matches(':focus-visible') && (outline || ring || border), focusVisible: element.matches(':focus-visible'), outline, ring, border };
      }, before);
      check(state.focused, 'keyboard order reaches ' + id, 'accessibilityChecks');
      check(state.indicator, 'keyboard focus has a computed visible indicator on ' + id, 'accessibilityChecks', false, { focused: state.focused, focusVisible: state.focusVisible, outline: state.outline, ring: state.ring, borderChanged: state.border });
    }
  };
  const reachable = async (page, surface) => {
    const selector = { login: '#loginSubmit', security: '#saveAdminPassword', 'recovery-form': '#passwordRecoverySubmit', 'recovery-result': '#passwordRecoveryFinish' }[surface];
    await page.locator(selector).scrollIntoViewIfNeeded();
    const state = await page.locator(selector).evaluate(element => {
      const bounds = element.getBoundingClientRect();
      const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2);
      return bounds.top >= -1 && bounds.bottom <= innerHeight + 1 && bounds.left >= -1 && bounds.right <= innerWidth + 1 && !!hit && (hit === element || element.contains(hit));
    });
    check(state, surface + ': primary action remains visible and reachable after scrolling at enlarged scale', 'accessibilityChecks');
  };
  const geometry = async (page, surface, zoom = 1) => {
    const state = await page.evaluate(({ scope, selector, zoom }) => {
      const root = document.querySelector(scope);
      const visible = element => element.getClientRects().length > 0 && !element.closest('[hidden],[inert]') && getComputedStyle(element).visibility !== 'hidden';
      const controls = [...root.querySelectorAll(selector)].filter(element => visible(element) && !element.disabled && element.tabIndex >= 0);
      const accessibleName = element => (element.getAttribute('aria-label') || '').trim() || (element.getAttribute('aria-labelledby') || '').split(/\s+/).some(id => document.getElementById(id)?.textContent.trim()) || [...(element.labels || [])].some(label => label.textContent.trim()) || ['BUTTON', 'A', 'SUMMARY', 'CODE'].includes(element.tagName) && element.textContent.trim();
      const rectangles = controls.map(element => {
        const label = ['checkbox', 'radio'].includes(element.type) && [...(element.labels || [])].find(visible);
        return (label || element).getBoundingClientRect();
      });
      const target = rectangle => rectangle.width >= 24 - .5 && rectangle.height >= 24 - .5;
      const spaced = (rectangle, i) => rectangles.every((other, j) => {
        if (i === j) return true;
        const x = rectangle.x + rectangle.width / 2, y = rectangle.y + rectangle.height / 2;
        if (target(other)) return Math.hypot(Math.max(other.left - x, 0, x - other.right), Math.max(other.top - y, 0, y - other.bottom)) >= 12;
        return Math.hypot(x - other.x - other.width / 2, y - other.y - other.height / 2) >= 24;
      });
      const containers = [document.documentElement, document.body, root, ...root.querySelectorAll('.access-card,.access-list,.recovery-code-box,.recovery-issued-panel')];
      const heading = [...root.querySelectorAll('h1,h2,h3,h4,[role="heading"]')].some(element => visible(element) && element.textContent.trim());
      return {
        width: innerWidth, dpr: devicePixelRatio, heading, controls: controls.length,
        labelled: controls.every(accessibleName),
        overflow: containers.some(element => element.clientWidth > 0 && element.scrollWidth > element.clientWidth + 2),
        contained: controls.every(element => { const bounds = element.getBoundingClientRect(); return bounds.left >= -1 && bounds.right <= innerWidth + 1; }),
        targets: rectangles.every((rectangle, i) => target(rectangle) || spaced(rectangle, i)),
        minimum: rectangles.filter(target).length,
        spacing: rectangles.filter((rectangle, i) => !target(rectangle) && spaced(rectangle, i)).length,
        comfortable: rectangles.filter(rectangle => rectangle.width >= 43.5 && rectangle.height >= 43.5).length,
        reduced: matchMedia('(prefers-reduced-motion: reduce)').matches,
        motion: [root, ...controls].every(element => {
          const style = getComputedStyle(element);
          return [style.animationDuration, style.transitionDuration].every(value => value.split(',').every(part => parseFloat(part) * (part.trim().endsWith('ms') ? 1 : 1000) <= 1));
        }),
        zoomLayout: zoom === 1 || innerWidth === 1440 / zoom && Math.abs(devicePixelRatio - zoom) < .01,
      };
    }, { scope: SCOPES[surface], selector: FOCUSABLE, zoom });
    check(state.controls > 0 && state.heading, surface + ': visible heading and interactive controls', 'accessibilityChecks');
    check(state.labelled, surface + ': visible controls have accessible names', 'accessibilityChecks');
    check(!state.overflow && state.contained, surface + ': no horizontal page/card overflow or clipped controls', 'accessibilityChecks');
    check(state.targets, surface + ': controls meet 24px target or measured spacing check', 'accessibilityChecks');
    check(state.reduced && state.motion, surface + ': reduced motion is honored in computed styles', 'accessibilityChecks');
    check(state.zoomLayout, surface + ': zoom changes effective layout width and pixel scale', 'accessibilityChecks');
    report.diagnostics.minimum24Targets += state.minimum;
    report.diagnostics.spacing24Targets += state.spacing;
    report.diagnostics.comfortable44Targets += state.comfortable;
    report.counters.reflowCells++;
  };
  const authPage = async (page, expected) => {
    await waitAuthView(page, expected);
    const state = await page.evaluate(view => {
      const visible = element => !!element && element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden';
      const root = document.getElementById('adminAuthPage');
      const header = root?.querySelector(':scope > header.auth-header');
      const main = root?.querySelector(':scope > main#adminAuthContent');
      const login = document.getElementById('loginOverlay');
      const recovery = document.getElementById('passwordRecoveryOverlay');
      const expectedHeading = view === 'login' ? 'loginTitle' : view === 'recover' ? 'passwordRecoveryTitle' : 'passwordRecoveryResultTitle';
      const inner = view === 'login' ? login?.querySelector(':scope > .login-card') : recovery?.querySelector(':scope > .recovery-dialog');
      const activeView = view === 'login' ? login : recovery;
      const plainNodes = [root, header, main, activeView, inner].filter(Boolean);
      const cardStyle = inner && getComputedStyle(inner);
      const pageStyle = root && getComputedStyle(root);
      return {
        dataset: document.documentElement.dataset.adminAuthView,
        structure: !!root?.matches('div.auth-page') && header?.parentElement === root && main?.parentElement === root && login?.matches('section.auth-view') && recovery?.matches('section.auth-view') && login?.parentElement === main && recovery?.parentElement === main && !!login.querySelector(':scope > .login-card') && !!recovery.querySelector(':scope > .recovery-dialog'),
        preferences: header ? [...header.querySelectorAll('[data-theme-opt],[data-lang-opt]')].filter(visible).length === 4 : false,
        views: [login, recovery].filter(visible).map(element => element.id),
        mains: [...document.querySelectorAll('main')].filter(visible).map(element => element.id),
        heading: visible(document.getElementById(expectedHeading)) && document.getElementById(expectedHeading).tagName === 'H1' && [...root.querySelectorAll('h1')].filter(visible).map(element => element.id).join(',') === expectedHeading,
        dashboardHidden: !visible(document.querySelector('.layout')),
        nonModal: !root.matches('[role="dialog"],[role="alertdialog"],[aria-modal],.overlay-modal') && !root.querySelector('[role="dialog"],[role="alertdialog"],[aria-modal],.overlay-modal,.modal-close'),
        active: !root.closest('[inert]') && !main.closest('[inert]') && !inner.closest('[inert]'),
        flow: plainNodes.every(element => getComputedStyle(element).position !== 'fixed' && ['none', ''].includes(getComputedStyle(element).backdropFilter || 'none')),
        pageBackdrop: !!pageStyle && pageStyle.backgroundImage === 'none' && ['none', ''].includes(pageStyle.backdropFilter || 'none'),
        undecorated: !!cardStyle && cardStyle.boxShadow === 'none' && parseFloat(cardStyle.borderTopWidth) === 0 && parseFloat(cardStyle.borderRadius) === 0 && (cardStyle.backgroundColor === 'transparent' || /rgba?\([^)]*[\s,\/]0(?:\.0+)?\)$/.test(cardStyle.backgroundColor)),
        backContract: !!document.getElementById('passwordRecoveryBack') && document.getElementById('passwordRecoveryBack').closest('#passwordRecoveryFormView') && !document.querySelector('#passwordRecoveryResultView #passwordRecoveryBack') && !document.querySelector('#passwordRecoveryOverlay .modal-close'),
      };
    }, expected);
    const expectedView = expected === 'login' ? 'loginOverlay' : 'passwordRecoveryOverlay';
    equal(state.views, [expectedView], expected + ': exactly one authentication view is visible', 'uiChecks');
    equal(state.mains, ['adminAuthContent'], expected + ': authentication content is the sole visible main landmark', 'accessibilityChecks');
    check(state.dataset === expected && state.structure && state.preferences, expected + ': shared auth-page/header/main and view structure is intact', 'accessibilityChecks');
    check(state.heading, expected + ': the sole visible authentication heading is the contracted h1', 'accessibilityChecks');
    check(state.dashboardHidden && state.nonModal && state.active, expected + ': dashboard is hidden and the page has no modal dialog/inert semantics', 'accessibilityChecks');
    check(state.flow && state.pageBackdrop && state.undecorated, expected + ': authentication UI uses document flow without fixed/backdrop effects or decorated cards', 'accessibilityChecks');
    check(state.backContract, expected + ': Back exists only on the recovery form and no close-X control exists', 'accessibilityChecks');
  };
  const authFocus = async (page, view, targetID) => {
    await page.waitForFunction(id => document.activeElement?.id === id, targetID);
    const state = await page.evaluate(({ view, targetID }) => {
      const heading = document.getElementById(view === 'login' ? 'loginTitle' : view === 'recover' ? 'passwordRecoveryTitle' : 'passwordRecoveryResultTitle');
      const header = document.querySelector('#adminAuthPage > .auth-header');
      const target = document.getElementById(targetID);
      const headingRect = heading.getBoundingClientRect(), headerRect = header.getBoundingClientRect(), targetRect = target.getBoundingClientRect();
      return { focused: document.activeElement === target, heading: headingRect.top >= headerRect.bottom - 1 && headingRect.top < innerHeight && headingRect.bottom > 0, target: targetRect.top < innerHeight && targetRect.bottom > 0 };
    }, { view, targetID });
    check(Object.values(state).every(Boolean), view + ': route change focuses the intended control and keeps its h1/content in view', 'accessibilityChecks');
  };
  const authKeyboardFlow = async (page, view) => {
    const count = await page.evaluate(({ view, selector }) => {
      const scope = document.getElementById(view === 'login' ? 'loginOverlay' : 'passwordRecoveryOverlay');
      const items = [...scope.querySelectorAll(selector)].filter(element => !element.disabled && element.tabIndex >= 0 && !element.closest('[hidden],[inert]') && element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden');
      items.at(-1)?.focus();
      window.__authTabProbe = null;
      window.addEventListener('keydown', event => {
        if (event.key !== 'Tab') return;
        queueMicrotask(() => { window.__authTabProbe = { prevented: event.defaultPrevented }; });
      }, { capture: true, once: true });
      return items.length;
    }, { view, selector: FOCUSABLE });
    check(count >= 2, view + ': page exposes a usable document focus order', 'accessibilityChecks');
    await page.keyboard.press('Tab');
    await page.waitForFunction(() => window.__authTabProbe !== null);
    check(await page.evaluate(() => window.__authTabProbe.prevented === false), view + ': Tab follows native document order without a page focus trap', 'accessibilityChecks');
    check(await page.locator('#adminAuthPage .auth-header [data-theme-opt="light"]').evaluate(element => { element.focus(); return document.activeElement === element && !element.closest('[inert]'); }), view + ': header preferences remain keyboard-focusable', 'accessibilityChecks');
  };
  const authPreferences = async page => {
    for (const theme of ['dark', 'light']) {
      await page.locator('#adminAuthPage .auth-header [data-theme-opt="' + theme + '"]').click();
      await page.waitForFunction(value => document.documentElement.dataset.theme === value && document.querySelector('#adminAuthPage .auth-header [data-theme-opt="' + value + '"]').getAttribute('aria-pressed') === 'true', theme);
    }
    for (const language of ['zh', 'en']) {
      await page.locator('#adminAuthPage .auth-header [data-lang-opt="' + language + '"]').click();
      await page.waitForFunction(value => document.documentElement.lang === (value === 'en' ? 'en' : 'zh-CN') && document.querySelector('#adminAuthPage .auth-header [data-lang-opt="' + value + '"]').getAttribute('aria-pressed') === 'true', language);
    }
    check(true, 'authentication header theme and language controls are usable without signing in', 'accessibilityChecks');
  };
  const axe = async (page, surface) => {
    if (!axeSource) return;
    await page.addScriptTag({ content: axeSource });
    const summary = await page.evaluate(async scope => {
      const result = await window.axe.run(document.querySelector(scope), { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] }, resultTypes: ['violations', 'incomplete'], reporter: 'v2' });
      return { violations: result.violations.map(item => ({ id: item.id, impact: item.impact, nodes: item.nodes.length })), incomplete: result.incomplete.length };
    }, SCOPES[surface]);
    report.coverage.axe.runs++;
    report.coverage.axe.incomplete += summary.incomplete;
    for (const violation of summary.violations) check(false, 'optional axe rule: ' + violation.id, 'accessibilityChecks', false, { impact: violation.impact, nodes: violation.nodes });
  };
  const matrix = async (page, surface) => {
    const cdp = await page.context().newCDPSession(page);
    try {
      for (const theme of ['light', 'dark']) for (const language of ['en', 'zh']) {
        currentStep = surface + '/' + theme + '/' + language;
        await preference(page, theme, language);
        await semantics(page, surface);
        for (const width of [1440, 390, 360]) {
          currentStep = surface + '/' + theme + '/' + language + '/' + width;
          await page.setViewportSize({ width, height: 1000 });
          await page.waitForFunction(value => innerWidth === value, width);
          await geometry(page, surface);
          if (width === 1440) {
            await contrast(page, surface);
            await screenshot(page, surface + '-' + theme + '-' + language + '-1440');
            await axe(page, surface);
          }
        }
        for (const factor of [2, 4]) {
          currentStep = surface + '/' + theme + '/' + language + '/zoom-' + factor;
          await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440 / factor, height: 1000 / factor, deviceScaleFactor: factor, mobile: false, screenWidth: 1440, screenHeight: 1000 });
          await page.waitForFunction(factor => innerWidth === 1440 / factor && devicePixelRatio === factor, factor);
          await geometry(page, surface, factor);
          await reachable(page, surface);
          if (surface === 'security') check(await page.evaluate(() => {
            const left = document.querySelector('.security-overview-left').getBoundingClientRect(), right = document.querySelector('.security-overview-right').getBoundingClientRect();
            return right.top >= left.bottom - 2 && Math.abs(right.left - left.left) < 2;
          }), 'security columns genuinely reflow at the zoom-equivalent width', 'accessibilityChecks');
          report.counters.zoomCells++;
        }
        await cdp.send('Emulation.clearDeviceMetricsOverride');
        await page.setViewportSize({ width: 1440, height: 1000 });
      }
    } finally {
      await cdp.send('Emulation.clearDeviceMetricsOverride').catch(() => {});
      await cdp.detach().catch(() => {});
      await page.setViewportSize({ width: 1440, height: 1000 });
    }
  };
  const readRecoveryValue = async (page, selector, reply) => {
    const value = reply.envelope?.data?.recoveryCode;
    check(typeof value === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value), 'real service returned a fresh 256-bit recovery credential', 'securityChecks', true);
    secrets.add(value);
    await page.waitForFunction(({ selector, value }) => document.querySelector(selector)?.textContent === value, { selector, value });
    check(reply.envelope.data.shownOnce === true, 'recovery response explicitly marks one-time display', 'securityChecks');
    return value;
  };
  const issue = async (page, password, accepted = true) => {
    const reply = await uiMutation(page, '/auth/recovery-code', () => page.locator('#recoveryCodeCurrentPassword').fill(password), () => page.locator('#issueRecoveryCodeButton').click());
    if (!accepted) {
      rejected(reply, [401], 'recovery issuance requires current password, not session alone', 'authentication_failed');
      await page.waitForFunction(() => !!document.getElementById('recoveryCodeIssueError').textContent.trim());
      await session(page.context(), true, 'failed reauthentication does not replace or end the session');
      return null;
    }
    if (reply.status === 503 && reply.envelope?.error === 'security_restart_required') { restartRequired = true; report.coverage.cleanup = 'restart-required persistence outcome; discard isolated data after stopping server'; }
    success(reply, 'recovery code issued through authenticated management form');
    const value = await readRecoveryValue(page, '#issuedRecoveryCode', reply);
    check(await page.locator('#issuedRecoveryCodeFinish').isDisabled(), 'issued recovery code must be acknowledged before finishing', 'uiChecks');
    check(await page.locator('#recoveryCodeCurrentPassword').inputValue() === '', 'current password cleared after recovery-code issuance', 'securityChecks');
    return value;
  };
  const finishIssue = async page => {
    await page.locator('#issuedRecoveryCodeSaved').check();
    check(await page.locator('#issuedRecoveryCodeFinish').isEnabled(), 'saving recovery code enables finish action', 'uiChecks');
    await page.locator('#issuedRecoveryCodeFinish').click();
    await page.locator('#issuedRecoveryCodePanel').waitFor({ state: 'hidden' });
    equal(await page.locator('#issuedRecoveryCode').textContent(), '', 'one-time recovery display cleared after finish', 'securityChecks');
  };
  const recover = async (page, code, password, accepted = true) => {
    let reply;
    passwordKnown = false;
    reply = await uiMutation(page, '/auth/recovery', async () => {
      await page.locator('#passwordRecoveryCode').fill(code);
      await page.locator('#passwordRecoveryNewPassword').fill(password);
      await page.locator('#passwordRecoveryConfirmPassword').fill(password);
    }, () => page.locator('#passwordRecoverySubmit').click());
    if (reply.status === 200 && reply.envelope?.success) { activePassword = password; passwordKnown = true; changedPassword = true; }
    else if (reply.status >= 400 && reply.status < 500) passwordKnown = true;
    if (!accepted) {
      rejected(reply, [401], 'old recovery credential is rejected through the actual recovery form', 'authentication_failed');
      await page.waitForFunction(() => !!document.getElementById('passwordRecoveryError').textContent.trim() && !document.getElementById('passwordRecoverySubmit').disabled);
      check(await page.locator('#passwordRecoveryFormView').isVisible() && !await page.locator('#passwordRecoveryResultView').isVisible(), 'rejected recovery stays on the form without a replacement result', 'uiChecks');
      await session(page.context(), false, 'rejected recovery does not sign in');
      return null;
    }
    if (reply.status === 503 && reply.envelope?.error === 'security_restart_required') restartRequired = true;
    success(reply, 'single-use code resets password through real recovery UI');
    currentStep += '/wait for result';
    await waitAuthView(page, 'recovery-result');
    currentStep += '/read replacement';
    const replacement = await readRecoveryValue(page, '#passwordRecoveryReplacementCode', reply);
    check(replacement !== code, 'successful recovery rotates the credential', 'securityChecks', true);
    check(reply.envelope.data.loginRequired === true, 'recovery requires a separate password login', 'securityChecks');
    currentStep += '/verify result';
    check(await page.locator('#passwordRecoveryFinish').isDisabled(), 'replacement proof requires save acknowledgement', 'uiChecks');
    check(await page.evaluate(() => ['passwordRecoveryCode', 'passwordRecoveryNewPassword', 'passwordRecoveryConfirmPassword'].every(id => document.getElementById(id).value === '')), 'successful recovery clears all entered secret fields', 'securityChecks');
    await session(page.context(), false, 'recovery result does not grant an authenticated session');
    return replacement;
  };
  const finishRecovery = async page => {
    await page.locator('#passwordRecoverySaved').check();
    await page.locator('#passwordRecoveryFinish').click();
    await waitLogin(page);
    check(await page.evaluate(() => location.hash === '#login'), 'acknowledged recovery result returns to canonical #login', 'navigationChecks');
    equal(await page.locator('#passwordRecoveryReplacementCode').textContent(), '', 'replacement credential is cleared after leaving result', 'securityChecks');
  };
  const openRecovery = async page => {
    await waitLogin(page);
    await page.locator('#forgotPasswordButton').waitFor({ state: 'visible' });
    await page.locator('#forgotPasswordButton').click();
    await waitAuthView(page, 'recover');
    await authFocus(page, 'recover', 'passwordRecoveryCode');
    check(await page.evaluate(() => location.hash === '#recover'), 'forgot-password navigation owns canonical #recover', 'navigationChecks');
  };
  const returnRecoveryToLogin = async page => {
    await page.evaluate(() => closePasswordRecovery());
    await waitLogin(page);
  };
  const recoveryStage = async (page, stage, action) => {
    currentStep = stage;
    return action(page);
  };
  try {
    if (environment.ROUTENEST_SECURITY_VERIFY_MUTATIONS !== '1') throw new VerificationFailure('Set ROUTENEST_SECURITY_VERIFY_MUTATIONS=1 only for a disposable service.');
    base = validateBase(environment.BASE_URL);
    if (!validPassword(initialPassword)) throw new VerificationFailure('ADMIN_TEST_PASSWORD must satisfy the 8-character/1024-byte permanent-password policy.');
    if (environment.DEBUG || environment.PWDEBUG) throw new VerificationFailure('Disable DEBUG and PWDEBUG to prevent framework credential logging.');
    const require = createRequire(import.meta.url);
    const modules = environment.PLAYWRIGHT_MODULE ? [environment.PLAYWRIGHT_MODULE] : ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core'];
    let chromium;
    for (const name of modules) { try { ({ chromium } = require(name)); if (chromium) break; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw new VerificationFailure('Existing Playwright module could not be loaded.'); } }
    if (!chromium) throw new VerificationFailure('Set PLAYWRIGHT_MODULE to an existing local Playwright installation.');
    report.coverage.axe = { status: 'unavailable; optional local axe-core only', runs: 0, incomplete: 0 };
    try {
      const axeModule = environment.AXE_MODULE || 'axe-core';
      if (/^https?:/i.test(axeModule)) throw new Error('local_only');
      const loaded = require(axeModule);
      if (typeof loaded.source !== 'string') throw new Error('invalid_module');
      axeSource = loaded.source;
      report.coverage.axe.status = 'local module loaded';
    } catch { if (environment.AXE_MODULE) throw new VerificationFailure('AXE_MODULE must be an existing local axe-core module exporting source.'); }
    artifactDir = environment.ARTIFACT_DIR ? resolve(environment.ARTIFACT_DIR) : mkdtempSync(resolve(tmpdir(), 'routenest-security-journeys-'));
    mkdirSync(artifactDir, { recursive: true, mode: 0o700 });
    currentStep = 'launch headless Chromium';
    browser = await chromium.launch({ headless: true, executablePath: environment.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined), timeout: 20000, args: ['--no-sandbox', '--disable-dev-shm-usage', '--disable-background-networking', '--no-proxy-server'] });
    deadlineTimer = setTimeout(() => { expired = true; void browser.close().catch(() => {}); }, RUN_TIMEOUT);
    deadlineTimer.unref();
    currentStep = 'first-paint authentication landing';
    for (const path of ['/admin', '/admin/']) {
      const firstPaint = await newContext({ javaScriptEnabled: false });
      try {
        const firstPage = await firstPaint.newPage();
        attach(firstPage);
        await firstPage.goto(new URL(path, base).href, { waitUntil: 'domcontentloaded' });
        const state = await firstPage.evaluate(() => ({
          pending: document.documentElement.dataset.adminAuth === 'pending',
          dashboardHidden: getComputedStyle(document.querySelector('.layout')).display === 'none',
          landingVisible: document.getElementById('loginOverlay').getClientRects().length > 0,
          credentialsHidden: getComputedStyle(document.getElementById('loginForm')).display === 'none',
          loadingVisible: document.getElementById('loginStatus').getClientRects().length > 0
        }));
        check(Object.values(state).every(Boolean), path + ': standalone loading page hides dashboard and credentials before scripts run', 'uiChecks', true);
      } finally { await firstPaint.close(); contexts.delete(firstPaint); }
    }
    setup = await newContext();
    currentStep = 'bootstrap or sign in to inspect disposable service';
    const status = success(await http(setup, '/auth/status'), 'auth status comes from real HTTP service');
    check(status.recoveryRequired === false && status.legacyRotationRequired === false, 'fresh/current identity only; no offline recovery or legacy migration', 'securityChecks', true);
    if (status.bootstrapRequired) {
      if (!environment.ADMIN_TEST_BOOTSTRAP_SECRET) throw new VerificationFailure('ADMIN_TEST_BOOTSTRAP_SECRET is required for an uninitialized disposable service.');
      success(await http(setup, '/auth/bootstrap', { method: 'POST', data: { secret: environment.ADMIN_TEST_BOOTSTRAP_SECRET, password: initialPassword }, retry: true }), 'secret-protected HTTP bootstrap succeeds');
      report.coverage.bootstrap = 'real HTTP bootstrap completed; recovery code is issued later through management UI';
    } else { await apiLogin(setup, initialPassword); report.coverage.bootstrap = 'already initialized; supplied password authenticated over real HTTP'; }
    const configuration = success(await http(setup, '/config'), 'read server pool configuration');
    check(isolatedPool(configuration?.poolPath), 'mandatory poolPath resolves to an existing file in a separate directory under system temporary storage', 'securityChecks', true);
    report.coverage.isolatedTemporaryPool = true;
    const users = success(await http(setup, '/auth/users'), 'inspect initial user inventory')?.users;
    check(Array.isArray(users) && users.length === 1 && users[0].id === 'break-glass', 'disposable service starts with only the reserved administrator', 'securityChecks', true);
    await track(setup, 'setup-password-session');
    await inventory(setup, 'initial');
    primary = await newContext();
    const page = await open(primary, 'security');
    currentStep = 'ordinary browser login/wait for canonical page';
    await waitLogin(page);
    currentStep = 'ordinary browser login/initial route focus';
    await authFocus(page, 'login', 'loginPassword');
    currentStep = 'ordinary browser login/verify anonymous session';
    await session(primary, false, 'a fresh ordinary browser context is unauthenticated');
    currentStep = 'ordinary browser login/reject anonymous device inventory';
    rejected(await http(primary, '/auth/trusted-devices'), [401], 'anonymous device inventory is protected');
    currentStep = 'ordinary browser login/browser-only controls';
    await browserTrustHidden(page);
    currentStep = 'ordinary browser login/clear password field';
    await page.locator('#loginPassword').fill('');
    currentStep = 'ordinary browser login/submit empty password';
    await page.locator('#loginSubmit').click();
    currentStep = 'ordinary browser login/wait for required-field error';
    await page.waitForFunction(() => !!document.getElementById('loginError').textContent.trim());
    currentStep = 'ordinary browser login/page semantics';
    await authPage(page, 'login');
    currentStep = 'ordinary browser login/keyboard flow';
    await keyboard(page, ['loginUsername', 'loginPassword', 'loginSubmit']);
    await authKeyboardFlow(page, 'login');
    currentStep = 'ordinary browser login/header preferences';
    await authPreferences(page);
    currentStep = 'ordinary browser login/responsive matrix';
    await matrix(page, 'login');
    currentStep = 'password UI sign-in';
    await uiLogin(page, initialPassword);
    await track(primary, 'primary-before-change');
    await selected(page, 'security');
    await inventory(primary, 'authenticated-browser', page);
    const fresh = await newContext();
    const freshPage = await open(fresh, 'security');
    await waitLogin(freshPage);
    await session(fresh, false, 'another fresh context cannot inherit the signed-in browser session');
    await fresh.close(); contexts.delete(fresh);
    currentStep = 'HTTP session boundary negatives';
    for (const path of ['/logout', '/auth/recovery-code', '/auth/trusted-devices/revoke-all']) {
      for (const variant of [{ csrfMode: 'missing', originMode: 'same', code: 'csrf_invalid' }, { csrfMode: 'wrong', originMode: 'same', code: 'csrf_invalid' }, ...['foreign', 'null', 'missing'].map(originMode => ({ csrfMode: 'valid', originMode, code: 'same_origin_required' }))]) {
        const reply = await http(primary, path, { method: 'POST', data: path === '/logout' ? {} : { currentPassword: activePassword }, ...variant });
        rejected(reply, [403], path + ': refuses ' + variant.originMode + ' Origin / ' + variant.csrfMode + ' CSRF', variant.code);
        await session(primary, true, 'rejected mutation leaves authenticated session intact');
      }
    }
    currentStep = 'native HTTP surfaces do not grant device authority';
    const anonymous = await newContext();
    for (const context of [anonymous, primary]) {
      const fabricated = randomBytes(32).toString('base64url'); secrets.add(fabricated);
      rejected(await http(context, '/auth/desktop-handoff', { method: 'POST', data: { token: fabricated } }), [403], 'HTTP handoff transport is refused with a fabricated well-shaped token', 'desktop_handoff_unavailable');
      report.coverage.nativeHandoff.fabricatedTokenProbes++;
      for (const path of ['/auth/trusted-devices/enroll', '/auth/trusted-devices/register', '/auth/desktop/enroll', '/auth/trusted-devices']) {
        rejected(await http(context, path, { method: 'POST', data: { currentPassword: activePassword, metadata: { name: 'HTTP negative probe', platform: 'browser' } } }), [403, 404, 405], 'candidate HTTP enrollment surface is absent or refused: ' + path);
      }
    }
    await session(anonymous, false, 'native HTTP negative probes do not establish browser authority');
    await anonymous.close(); contexts.delete(anonymous);
    await inventory(primary, 'after-boundary-negatives', page);
    currentStep = 'canonical navigation owner/hash smoke matrix';
    for (const cell of [{ width: 1440, theme: 'light', language: 'en' }, { width: 390, theme: 'dark', language: 'zh' }]) {
      await preference(page, cell.theme, cell.language);
      await page.setViewportSize({ width: cell.width, height: 1000 });
      equal(await page.locator('.nav-item[data-tab]').evaluateAll(items => items.map(item => item.dataset.tab)), TABS, 'all 13 canonical destinations are registered', 'navigationChecks');
      for (const tab of TABS) {
        currentStep = 'navigation/' + cell.width + '/' + tab;
        await dismissNotice(page);
        if (await page.locator('#mobileNavBtn').isVisible()) await page.locator('#mobileNavBtn').click();
        await page.locator('.nav-item[data-tab="' + tab + '"]').click();
        await selected(page, tab);
      }
      for (const [alias, owner] of Object.entries(ALIASES)) {
        currentStep = 'alias/' + cell.width + '/' + alias;
        await dismissNotice(page);
        await page.evaluate(hash => { location.hash = hash; }, alias);
        await selected(page, owner);
        if (['accounts', 'import'].includes(alias)) check(await page.locator('#tab-credentials [data-subpage="conns"]').isVisible(), 'legacy alias selects connected-account owner DOM', 'aliasChecks');
        if (alias === 'import') {
          await page.locator('#providerSetup.open').waitFor({ state: 'visible' });
          await page.locator('#loginImportBody').waitFor({ state: 'visible' });
          check(await page.locator('#loginImportBody').isVisible(), 'legacy import alias opens its actual import owner DOM without submitting provider actions', 'aliasChecks');
          await page.keyboard.press('Escape');
          await page.locator('#providerSetup.open').waitFor({ state: 'hidden' });
        }
        report.counters.aliasChecks++;
      }
    }
    await page.evaluate(() => { location.hash = 'security'; });
    await selected(page, 'security');
    await page.setViewportSize({ width: 1440, height: 1000 });
    await inventory(primary, 'before-password-change', page);
    currentStep = 'cross-user real HTTP fixture';
    userCreationAttempted = true;
    disposableUser = success(await http(primary, '/auth/users', { method: 'POST', data: { username: viewerName, password: viewerPassword, roles: ['viewer'] } }), 'create disposable local viewer to distinguish all-user recovery from owner password change', [201])?.user;
    check(!!disposableUser?.id, 'disposable viewer creation returns its actual identity', 'securityChecks', true);
    report.coverage.otherUserFixture = 'one disposable local viewer created by real authenticated HTTP; no device fixture';
    viewer = await newContext();
    await apiLogin(viewer, viewerPassword, viewerName);
    await track(viewer, 'viewer-before-recovery', disposableUser.id);
    rejected(await http(viewer, '/auth/recovery-code', { method: 'POST', data: { currentPassword: viewerPassword } }), [403], 'viewer session cannot issue recovery codes', 'permission_denied');
    const previous = await inventory(primary, 'password-change-source');
    const oldPrimary = await newContext();
    await oldPrimary.addCookies(await rememberCookies(primary));
    currentStep = 'change password through actual security form';
    passwordKnown = false;
    const rotation = await uiMutation(page, '/auth/password', async () => {
      await page.locator('#settingCurrentPassword').fill(activePassword);
      await page.locator('#settingPassword').fill(changed);
      await page.locator('#settingPasswordConfirm').fill(changed);
    }, () => page.locator('#saveAdminPassword').click());
    if (rotation.status === 200 && rotation.envelope?.success) { activePassword = changed; passwordKnown = true; changedPassword = true; }
    else if (rotation.status >= 400 && rotation.status < 500) passwordKnown = true;
    success(rotation, 'password change accepted over real HTTP');
    await session(setup, false, 'password change revokes independent previous admin session');
    await session(oldPrimary, false, 'password change revokes copied pre-change token, not only cleared browser cookies');
    await session(viewer, true, 'owner password change preserves the unrelated viewer session');
    await waitLogin(page);
    await uiLogin(page, initialPassword, false);
    await uiLogin(page, activePassword);
    await track(primary, 'primary-after-change');
    const afterChange = await inventory(primary, 'after-password-change', page);
    check(previous.sessions.filter(item => item.userID === 'break-glass').every(item => !afterChange.ids.has(item.id)), 'all previous administrator sessions are absent from the inventory', 'securityChecks', true);
    check(afterChange.sessions.length === 2 && afterChange.sessions.filter(item => item.userID !== 'break-glass').length === 1, 'password-change inventory is exactly one new admin plus the surviving viewer', 'securityChecks', true);
    currentStep = 'recovery-code management after password change';
    await issue(page, wrongPassword, false);
    await contrast(page, 'security');
    const issuedA = await issue(page, activePassword);
    await finishIssue(page);
    const issuedB = await issue(page, activePassword);
    check(issuedA !== issuedB, 'management rotation replaces the previous recovery credential', 'securityChecks', true);
    report.coverage.recoveryProof = 'issued twice through real management UI; retained privately in memory only';
    const afterIssue = await inventory(primary, 'after-code-issuance', page);
    equal([...afterIssue.ids].sort(), [...afterChange.ids].sort(), 'code issuance alone does not revoke or add sessions');
    const recoveryStatus = success(await http(primary, '/auth/status'), 'read recovery availability');
    check(recoveryStatus.recoveryCodeAvailable === true && !JSON.stringify(recoveryStatus).includes(issuedA) && !JSON.stringify(recoveryStatus).includes(issuedB) && !Object.hasOwn(recoveryStatus, 'recoveryCode'), 'public status advertises availability without returning the credential', 'securityChecks');
    await settleBootReads(page);
    await page.locator('#settingCurrentPassword').fill(activePassword);
    await page.locator('#settingPassword').fill(recovered);
    await page.locator('#settingPasswordConfirm').fill(replacementReset);
    await dismissNotice(page);
    await page.locator('#saveAdminPassword').click();
    await page.waitForFunction(() => !!document.getElementById('adminPasswordError').textContent.trim());
    for (const id of ['settingCurrentPassword', 'settingPassword', 'settingPasswordConfirm']) await page.locator('#' + id).fill('');
    await keyboard(page, ['settingCurrentPassword', 'settingPassword', 'settingPasswordConfirm', 'saveAdminPassword']);
    await page.evaluate(() => { void trustThisDesktop(); });
    await page.waitForFunction(() => !!document.getElementById('desktopSignInError').textContent.trim());
    currentStep = 'browser-only native guard and security accessibility';
    await matrix(page, 'security');
    await page.locator('#issuedRecoveryCodePanel').scrollIntoViewIfNeeded();
    await screenshot(page, 'issued-code-redacted');
    await finishIssue(page);
    await apiLogin(setup, activePassword);
    await track(setup, 'independent-before-recovery');
    const beforeRecovery = await inventory(primary, 'before-code-reset', page);
    check(beforeRecovery.sessions.length === 3 && beforeRecovery.sessions.some(item => item.userID === disposableUser.id), 'recovery starts with exactly two admin sessions and one other-user session', 'securityChecks', true);
    const recovering = await newContext();
    let recoverySubmissions = 0;
    recovering.on('request', request => { if (new URL(request.url()).pathname === '/admin/api/auth/recovery' && request.method() === 'POST') recoverySubmissions++; });
    const recoveryPage = await open(recovering, 'login');
    await waitLogin(recoveryPage);
    check(recoverySubmissions === 0, 'canonical signed-out #login load does not call the reset API', 'securityChecks');
    await authPage(recoveryPage, 'login');
    await openRecovery(recoveryPage);
    await authPage(recoveryPage, 'recover');
    await authPreferences(recoveryPage);
    await keyboard(recoveryPage, ['passwordRecoveryCode', 'passwordRecoveryNewPassword', 'passwordRecoveryConfirmPassword']);
    await authKeyboardFlow(recoveryPage, 'recover');
    await recoveryPage.keyboard.press('Escape');
    await waitAuthView(recoveryPage, 'recover');
    check(await recoveryPage.evaluate(() => location.hash === '#recover'), 'Escape does not dismiss the recovery page', 'accessibilityChecks');
    await recoveryPage.locator('#passwordRecoveryBack').click();
    await waitLogin(recoveryPage);
    await authFocus(recoveryPage, 'login', 'loginPassword');
    check(await recoveryPage.evaluate(() => location.hash === '#login'), 'Back control returns to canonical #login', 'navigationChecks');
    await recoveryPage.goBack();
    await waitAuthView(recoveryPage, 'recover');
    await authFocus(recoveryPage, 'recover', 'passwordRecoveryCode');
    check(await recoveryPage.evaluate(() => location.hash === '#recover'), 'browser Back restores the recovery page without a reset API', 'navigationChecks');
    await recoveryPage.goForward();
    await waitLogin(recoveryPage);
    check(await recoveryPage.evaluate(() => location.hash === '#login'), 'browser Forward restores the login page without a reset API', 'navigationChecks');
    await recoveryPage.goBack();
    await waitAuthView(recoveryPage, 'recover');
    await recoveryPage.reload({ waitUntil: 'domcontentloaded' });
    await waitAuthView(recoveryPage, 'recover');
    await session(recovering, false, 'direct #recover refresh remains signed out and does not grant authority');
    await authPage(recoveryPage, 'recover');
    await authFocus(recoveryPage, 'recover', 'passwordRecoveryCode');
    check(await recoveryPage.evaluate(() => location.hash === '#recover'), 'direct #recover refresh preserves the recovery route without a reset API', 'navigationChecks');
    report.coverage.authRecoveryRequestsBeforeSubmit = recoverySubmissions;
    check(recoverySubmissions === 0, '#login/#recover navigation, back/forward, and direct refresh do not call the reset API', 'securityChecks');
    await recoveryPage.locator('#passwordRecoverySubmit').click();
    await recoveryPage.waitForFunction(() => !!document.getElementById('passwordRecoveryError').textContent.trim());
    await matrix(recoveryPage, 'recovery-form');
    currentStep = 'management-replaced recovery credential rejection';
    await recover(recoveryPage, issuedA, recovered, false);
    currentStep = 'single-use recovery reset and all-user revocation';
    const replacement = await recover(recoveryPage, issuedB, recovered);
    for (const context of [primary, setup, viewer]) await session(context, false, 'successful recovery revokes every tracked previous session including another user');
    await waitAuthView(recoveryPage, 'recovery-result');
    check(await recoveryPage.evaluate(() => location.hash === '#recover'), 'successful recovery remains on canonical #recover while showing its result', 'navigationChecks');
    await authPage(recoveryPage, 'recovery-result');
    await authFocus(recoveryPage, 'recovery-result', 'passwordRecoveryReplacementCode');
    await recoveryPage.keyboard.press('Escape');
    await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
    check(await recoveryPage.evaluate(() => document.activeElement?.id === 'passwordRecoveryReplacementCode' && document.getElementById('passwordRecoveryReplacementCode').textContent.length === 43), 'Escape cannot discard an unacknowledged replacement recovery code, move result focus, or leave #recover', 'uiChecks');
    await authPreferences(recoveryPage);
    await recoveryPage.evaluate(() => closePasswordRecovery());
    await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
    check(await recoveryPage.evaluate(() => document.getElementById('passwordRecoveryReplacementCode').textContent.length === 43), 'direct close callback cannot discard an unacknowledged recovery result', 'uiChecks');
    await recoveryPage.evaluate(() => { location.hash = '#login'; });
    await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
    check(await recoveryPage.evaluate(() => document.getElementById('passwordRecoveryReplacementCode').textContent.length === 43), 'hash navigation cannot discard an unacknowledged recovery result and is reset to #recover', 'uiChecks');
    await recoveryPage.goBack();
    await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
    check(await recoveryPage.evaluate(() => document.getElementById('passwordRecoveryReplacementCode').textContent.length === 43), 'browser Back cannot discard an unacknowledged recovery result and remains on #recover', 'uiChecks');
    await recoveryPage.locator('#passwordRecoverySaved').check();
    check(await recoveryPage.locator('#passwordRecoveryFinish').isEnabled(), 'explicit acknowledgement enables the recovery result action', 'uiChecks');
    await recoveryPage.evaluate(() => { location.hash = '#login'; });
    await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
    await recoveryPage.goBack();
    await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
    check(await recoveryPage.locator('#passwordRecoveryReplacementCode').evaluate(element => element.textContent.length === 43), 'acknowledgement alone does not let hash or Back discard the result before Finish', 'uiChecks');
    await matrix(recoveryPage, 'recovery-result');
    await finishRecovery(recoveryPage);
    await page.evaluate(() => { location.hash = '#login'; });
    await page.reload({ waitUntil: 'domcontentloaded' });
    await waitLogin(page);
    await uiLogin(page, changed, false);
    await uiLogin(page, activePassword);
    await track(primary, 'after-first-recovery');
    await selected(page, 'security');
    const afterReset = await inventory(primary, 'after-first-code-reset', page);
    check(afterReset.sessions.length === 1 && beforeRecovery.sessions.every(item => !afterReset.ids.has(item.id)), 'post-recovery inventory contains only the new password session; all old users and sessions are absent', 'securityChecks', true);
    currentStep = 'used recovery credential cannot be replayed';
    await openRecovery(recoveryPage);
    await recover(recoveryPage, issuedB, replacementReset, false);
    await session(primary, true, 'replay rejection leaves the replacement password session intact');
    await recoveryStage(recoveryPage, 'replacement recovery/return to login', returnRecoveryToLogin);
    await recoveryStage(recoveryPage, 'replacement recovery/open form', openRecovery);
    const finalCode = await recoveryStage(recoveryPage, 'replacement recovery/submit credential', page => recover(page, replacement, replacementReset));
    check(finalCode !== issuedA && finalCode !== issuedB && finalCode !== replacement, 'second successful reset returns another distinct replacement', 'securityChecks');
    await session(primary, false, 'using the replacement code also revokes newly created sessions');
    await finishRecovery(recoveryPage);
    await uiLogin(recoveryPage, recovered, false);
    await uiLogin(recoveryPage, activePassword);
    await track(recovering, 'after-replacement-recovery');
    await selected(recoveryPage, 'security');
    const finalInventory = await inventory(recovering, 'after-replacement-code-reset', recoveryPage);
    check(finalInventory.sessions.length === 1 && finalInventory.devices.length === 0, 'replacement reset leaves exactly the newly authenticated session and zero unseeded devices', 'securityChecks', true);
    report.coverage.recoveryProof = 'management replacement, successful reset, used-code rejection and successful replacement-code reset verified through actual UI; codes never persisted by harness';
    completed = true;
  } catch (error) {
    if (!(error instanceof VerificationFailure) || !report.failures.some(failure => failure.step === currentStep && failure.check === error.message)) report.failures.push({ step: currentStep, check: error instanceof VerificationFailure ? error.message : expired ? 'bounded run deadline exceeded' : 'browser or HTTP operation stopped: ' + diagnosticMessage(error), errorType: error?.name === 'TimeoutError' ? 'TimeoutError' : error instanceof VerificationFailure ? 'VerificationFailure' : 'OperationError' });
  } finally {
    if (browser && report.coverage.isolatedTemporaryPool && passwordKnown && !restartRequired && !expired) {
      currentStep = 'restore disposable credentials and verify final authentication';
      try {
        const cleanup = await newContext();
        currentStep = 'cleanup/authenticate current password';
        await apiLogin(cleanup, activePassword);
        if (userCreationAttempted) {
          currentStep = 'cleanup/remove disposable viewer';
          const users = success(await http(cleanup, '/auth/users'), 'find only the disposable viewer during cleanup').users;
          const user = users.find(item => item.username === viewerName);
          if (user) success(await http(cleanup, '/auth/users/delete', { method: 'POST', data: { userID: user.id, revision: user.revision } }), 'delete only this harness disposable viewer');
        }
        if (changedPassword) {
          currentStep = 'cleanup/restore initial password';
          passwordKnown = false;
          const restored = await http(cleanup, '/auth/password', { method: 'POST', data: { oldPassword: activePassword, newPassword: initialPassword }, retry: true });
          if (restored.status === 200 && restored.envelope?.success) { activePassword = initialPassword; passwordKnown = true; }
          success(restored, 'restore ADMIN_TEST_PASSWORD on isolated service');
        } else success(await http(cleanup, '/logout', { method: 'POST', data: {} }), 'close cleanup-only session');
        if (completed) {
          currentStep = 'cleanup/fresh browser with restored password';
          const finalContext = await newContext();
          const finalPage = await open(finalContext, 'security');
          await waitLogin(finalPage);
          currentStep = 'cleanup/final password form login';
          await uiLogin(finalPage, initialPassword);
          currentStep = 'cleanup/final owner and live HTTP auth';
          await selected(finalPage, 'security');
          await track(finalContext, 'final-restored-password');
          currentStep = 'cleanup/final session and device inventory';
          const final = await inventory(finalContext, 'final-restored-password', finalPage);
          check(final.sessions.length === 1 && final.devices.length === 0, 'final live auth inventory has one known session and zero devices', 'securityChecks', true);
          success(await http(finalContext, '/logout', { method: 'POST', data: {} }), 'explicitly revoke final verification session');
          await session(finalContext, false, 'final verification session is signed out rather than left on server');
          report.coverage.finalAuthentication = 'restored password verified by real UI plus HTTP session/owner DOM, then explicitly signed out';
        }
        report.coverage.cleanup = 'test password restored/preserved; any harness-created viewer removed; ' + (report.coverage.recoveryProof === 'not issued' ? 'no recovery code issued; ' : 'recovery verifier intentionally rotated; ') + 'stop server and discard isolated data';
      } catch (error) { report.failures.push({ step: currentStep, check: 'cleanup or final authentication failed; stop server and discard its isolated data; no password guessing or mutation retry on unknown outcomes', errorType: error?.name === 'TimeoutError' ? 'TimeoutError' : 'OperationError' }); report.coverage.cleanup = 'failed; discard disposable data'; }
    } else if (report.coverage.isolatedTemporaryPool) report.coverage.cleanup = 'not attempted: uncertain mutation, restart-required outcome, or deadline; stop server and discard disposable data';
    if (deadlineTimer) clearTimeout(deadlineTimer);
    for (const context of contexts) await context.close().catch(() => {});
    if (browser) await browser.close().catch(() => {});
  }
  report.ok = completed && report.failures.length === 0;
  report.completedJourneys = completed;
  report.failures = report.failures.map(failure => ({ ...failure, step: redactedText(failure.step, secrets), check: redactedText(failure.check, secrets) }));
  return report;
}

export async function selfTests() {
  const tests = [];
  const test = (name, action) => tests.push({ name, action });
  test('IPv4, IPv6 and localhost exact administrator URLs', () => {
    for (const url of ['http://127.0.0.1:3467/admin/', 'http://[::1]:3467/admin/', 'https://localhost:3467/admin/']) assert.equal(validateBase(url).href, url);
    for (const url of [undefined, 'http://192.0.2.1/admin/', 'http://localhost.evil.invalid/admin/', 'file:///admin/', 'http://u:p@localhost/admin/', 'http://localhost/admin/?secret=value', 'http://localhost/admin/#security', 'http://localhost/admin', 'http://0.0.0.0/admin/']) assert.throws(() => validateBase(url));
  });
  test('permanent password Unicode and byte boundaries', () => {
    assert.equal(validPassword('a'.repeat(8)), true);
    assert.equal(validPassword('🔒'.repeat(8)), true);
    for (const value of ['', 'a'.repeat(7), ' '.repeat(8), '🔒'.repeat(257), undefined]) assert.equal(validPassword(value), false);
  });
  test('temporary pool guard excludes root, siblings and nonexistent paths', () => {
    assert.equal(strictDescendant('/tmp/test', '/tmp/test/pool'), true);
    for (const path of ['/tmp/test', '/tmp/testing/pool', '/tmp/test/../pool', 'pool']) assert.equal(strictDescendant('/tmp/test', path), false);
    assert.equal(isolatedPool('/etc/passwd'), false);
    assert.equal(isolatedPool(resolve(tmpdir(), 'nonexistent-security-journey', 'pool')), false);
  });
  test('realpath pool guard refuses a symlink escape', async () => {
    const { mkdtemp, writeFile, symlink, rm } = await import('node:fs/promises');
    const directory = await mkdtemp(resolve(tmpdir(), 'routenest-guard-selftest-'));
    try {
      const pool = resolve(directory, 'pool.json');
      await writeFile(pool, '{}', { mode: 0o600 });
      assert.equal(isolatedPool(pool), true);
      await symlink('/etc/passwd', resolve(directory, 'outside'));
      assert.equal(isolatedPool(resolve(directory, 'outside')), false);
    } finally { await rm(directory, { recursive: true, force: true }); }
  });
  test('Origin omission differs from literal null; CSRF omissions are not defaulted', () => {
    const cookies = [{ name: CSRF_COOKIE, value: 'private-selftest-csrf' }];
    assert.deepEqual(requestHeaders('http://[::1]:3467', cookies, { originMode: 'missing', csrfMode: 'missing' }), {});
    assert.equal(requestHeaders('http://[::1]:3467', cookies, { originMode: 'null' }).Origin, 'null');
    assert.equal(requestHeaders('http://[::1]:3467', cookies).Origin, 'http://[::1]:3467');
    assert.equal(requestHeaders('http://[::1]:3467', cookies, { csrfMode: 'wrong' })['X-RouteNest-CSRF'], 'intentionally-invalid-csrf');
    assert.equal(Object.hasOwn(requestHeaders('http://[::1]:3467', cookies, { method: 'GET' }), 'X-RouteNest-CSRF'), false);
  });
  test('contrast uses linear-light luminance and alpha-composited backgrounds', () => {
    assert.equal(contrastRatio([0, 0, 0, 1], [1, 1, 1, 1]), 21);
    assert.equal(contrastRatio([1, 1, 1, 1], [1, 1, 1, 1]), 1);
    assert.deepEqual(parseColor('rgba(255, 0, 0, 0.5)'), [1, 0, 0, .5]);
    assert.deepEqual(parseColor('color(srgb 1 0 0 / 50%)'), [1, 0, 0, .5]);
    assert.deepEqual(composite([1, 0, 0, .5], [1, 1, 1, 1]), [1, .5, .5, 1]);
    const ratio = computedContrast({ foreground: 'rgb(0, 0, 0)', backgrounds: ['rgba(255, 0, 0, 0.5)', 'rgb(255, 255, 255)'] });
    assert.equal(ratio, contrastRatio([0, 0, 0, 1], [1, .5, .5, 1]));
    assert.equal(computedContrast({ foreground: 'rgb(0, 0, 0)', backgrounds: ['transparent'] }), null);
    assert.equal(computedContrast({ unsupported: true, backgrounds: [] }), null);
    assert.equal(parseColor('linear-gradient(red, blue)'), null);
  });
  test('error redaction strips registered secrets, tokens and credential headers', () => {
    const value = redactedText('password=private-value X-RouteNest-CSRF: token-value\n' + 'A'.repeat(43), ['private-value']);
    assert.equal(value.includes('private-value'), false);
    assert.equal(value.includes('token-value'), false);
    assert.equal(value.includes('A'.repeat(43)), false);
  });
  test('diagnostics classify failures without returning URL/query/secret content', () => {
    assert.equal(requestCategory('http://[::1]:3467/admin/api/auth/recovery?code=private', 'http://[::1]:3467'), 'authentication');
    assert.equal(requestCategory('http://[::1]:3467/admin/api/models?token=private', 'http://[::1]:3467'), 'auxiliary');
    assert.equal(requestCategory('https://foreign.invalid/private', 'http://[::1]:3467'), 'external');
    assert.equal(requestCategory('not a URL', 'http://[::1]:3467'), 'other');
  });
  test('throttle retry is strictly bounded', () => {
    assert.equal(retryDelay('12'), 12100);
    for (const value of ['0', '16', '-1', '1.5', undefined, 'Wed, 1 Jan', 'Infinity']) assert.equal(retryDelay(value), null);
  });
  test('route inventory retains 13 routes, four canonical aliases, and real surface scopes', () => {
    assert.equal(TABS.length, 13);
    assert.equal(new Set(TABS).size, 13);
    assert.equal(Object.keys(ALIASES).length, 4);
    assert.ok(Object.values(ALIASES).every(value => TABS.includes(value)));
    assert.equal(SCOPES.security, '#tab-security');
    assert.match(String(runVerification), /security-overview-left/);
    assert.match(String(runVerification), /security-overview-right/);
    assert.doesNotMatch(String(runVerification), /access-account-card/);
    assert.match(String(runVerification), /replacement recovery\/return to login/);
    assert.match(String(runVerification), /replacement recovery\/open form/);
    assert.match(String(runVerification), /replacement recovery\/submit credential/);
  });
  test('screenshot redaction restores recovery text and inputs even after capture failure', async () => {
    const vm = await import('node:vm');
    const code = 'R'.repeat(43), csrf = 'C'.repeat(43), password = 'SelfTestPrivatePassword';
    const node = { nodeValue: code, parentElement: { tagName: 'CODE', closest: () => ({}) } };
    const other = { nodeValue: 'Status ' + csrf, parentElement: { tagName: 'DIV', closest: () => null } };
    const input = { type: 'password', value: password };
    const context = { window: {}, NodeFilter: { SHOW_TEXT: 4 }, document: { body: {}, createTreeWalker: () => { let index = -1; const nodes = [node, other]; return { nextNode() { index++; this.currentNode = nodes[index]; return index < nodes.length; } }; }, querySelectorAll: () => [input] } };
    const page = { evaluate: async (fn, values) => vm.runInNewContext('(' + fn.toString() + ')(values)', { ...context, values }) };
    let captured = false;
    await assert.rejects(withScreenshotMask(page, new Set([code, csrf, password]), async () => {
      captured = true;
      assert.equal(node.nodeValue.includes(code), false);
      assert.equal(other.nodeValue.includes(csrf), false);
      assert.equal(input.value, '');
      throw new Error('intentional capture failure');
    }));
    assert.equal(captured, true);
    assert.equal(node.nodeValue, code);
    assert.equal(other.nodeValue, 'Status ' + csrf);
    assert.equal(input.value, password);
    assert.equal(context.window.__securityScreenshotRestore, undefined);
  });
  test('missing mutation consent fails safely without loading browser or making HTTP requests', async () => {
    const report = await runVerification({ ADMIN_TEST_PASSWORD: 'SelfTestPrivatePassword' });
    assert.equal(report.ok, false);
    assert.equal(report.counters.httpChecks, 0);
    assert.equal(JSON.stringify(report).includes('SelfTestPrivatePassword'), false);
  });
  const failures = [];
  for (const item of tests) { try { await item.action(); } catch { failures.push(item.name); } }
  return { evidence: 'standalone harness self-tests; no browser, service, Wails or OS-runtime verification', selfTests: tests.length, passed: tests.length - failures.length, failures, ok: failures.length === 0 };
}

const invoked = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (invoked) {
  if (process.argv.includes('--help')) {
    process.stdout.write('Disposable real-HTTP browser authentication/security verification. No API mocks, native bridge injection, dependency installation, traces, HAR, or video.\nRequired: ROUTENEST_SECURITY_VERIFY_MUTATIONS=1, BASE_URL=http://[::1]:3467/admin/ (or another accepted loopback authority), ADMIN_TEST_PASSWORD. Fresh service: ADMIN_TEST_BOOTSTRAP_SECRET matching its private bootstrap input.\nOptional existing local installations: PLAYWRIGHT_MODULE, CHROMIUM_PATH, AXE_MODULE. ARTIFACT_DIR selects redacted screenshots; otherwise a new system-temporary directory is used. Disable DEBUG/PWDEBUG.\nRequires a locally accessible server config poolPath resolving inside a separate system-temporary directory and only the reserved administrator with no seeded devices. Never run concurrently with another mutating harness.\nRuns password changes and recovery twice, then restores ADMIN_TEST_PASSWORD, removes its disposable viewer, verifies final real UI/HTTP authentication and signs out. Recovery proof remains rotated: stop the test service and discard its data. Unknown persistence outcomes are not retried.\nnode tools/admin_security_journeys_verify.mjs --self-test\nResult: one JSON object with ok, completedJourneys, counters, diagnostics, inventory counts, coverage limitations, screenshotLabels and redacted failures. Nonzero exit on failed checks; targeted accessibility only, not WCAG conformance.\n');
  } else {
    const result = process.argv.includes('--self-test') ? await selfTests() : await runVerification();
    process.stdout.write(JSON.stringify(result) + '\n');
    process.exitCode = result.ok ? 0 : 1;
  }
}

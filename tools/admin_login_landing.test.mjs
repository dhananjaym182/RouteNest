import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { adminSecuritySimulation } from './admin_security_devices.test.mjs';

const head = readFileSync(new URL('../internal/admin/ui/head.html', import.meta.url), 'utf8');
const login = readFileSync(new URL('../internal/admin/ui/html/pages/login.html', import.meta.url), 'utf8');
const styles = readFileSync(new URL('../internal/admin/ui/css/50_components_a11y.css', import.meta.url), 'utf8');
const status = { initialized: true, bootstrapRequired: false, recoveryRequired: false, legacyRotationRequired: false, recoveryCodeAvailable: true, ssoProviders: [] };

function statusAPI(overrides) {
  return (method, path) => path === '/auth/status'
    ? { success: true, data: { ...status, ...overrides } }
    : { success: false, error: 'authentication_failed' };
}

test('administrator entry is a standalone pending landing before JavaScript or authentication completes', () => {
  assert.match(head, /<html\b[^>]*data-admin-auth="pending"/);
  assert.match(styles, /html:not\(\[data-admin-auth="authenticated"\]\) \.layout[^}]*display:none/);
  assert.match(styles, /#loginOverlay\{[^}]*background:var\(--bg\)[^}]*display:flex/);
  assert.match(styles, /html\[data-admin-auth="pending"\] #loginForm[^}]*display:none/);
  assert.match(styles, /html\[data-admin-auth="pending"\] #loginSSOProviders[^}]*display:none/);
  assert.match(login, /id="loginStatus"[^>]*role="status"/);
  assert.match(login, /id="loginPassword"[^>]*\bautofocus\b/);
});

test('a fresh browser setup stays on the landing and requires the owner bootstrap proof', async () => {
  const ui = await adminSecuritySimulation({ browser: true, load: false });
  ui.context.fetch = async (url) => new Response(JSON.stringify(statusAPI({ initialized: false, bootstrapRequired: true })('GET', url.slice('/admin/api'.length))), { status: 200 });
  assert.equal(await ui.run('initializeAdminAuth()'), false);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'signed-out');
  assert.equal(ui.run('_adminAuthMode'), 'bootstrap');
  assert.equal(ui.element('bootstrapSecret').disabled, false);
  assert.equal(ui.element('loginNewPassword').disabled, false);
  assert.equal(ui.element('desktopTrustLoginRow').hidden, true);
});

test('a fresh browser cannot inherit a desktop session and remains on the password landing', async () => {
  const ui = await adminSecuritySimulation({ browser: true, load: false });
  ui.context.fetch = async (url) => new Response(JSON.stringify(statusAPI()('GET', url.slice('/admin/api'.length))), { status: url.endsWith('/auth/session') ? 401 : 200 });
  assert.equal(await ui.run('initializeAdminAuth()'), false);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'signed-out');
  assert.equal(ui.run('_adminPrincipal'), null);
  assert.equal(ui.element('loginOverlay').style.display, 'flex');
  assert.equal(ui.element('desktopTrustLoginRow').hidden, true);
});

test('a verified ordinary session reveals the dashboard and hides the initial landing', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'authenticated');
  assert.equal(ui.element('loginOverlay').style.display, 'none');
  assert.equal(ui.run('_adminPrincipal.username'), 'admin');
});

test('an expired session returns to the landing and clears stale identity and permissions', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  ui.transport.authenticated = false;
  await assert.rejects(ui.run('api("GET", "/auth/session")'));
  assert.equal(ui.run('_adminPrincipal'), null);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'signed-out');
  assert.equal(ui.element('loginOverlay').style.display, 'flex');
});

test('repeated unauthorized responses do not steal focus from the visible login form', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
  ui.element('loginPassword').closest = () => null;
  ui.context.setTimeout = callback => callback();
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  ui.transport.authenticated = false;
  ui.element('loginUsername').focus();
  await assert.rejects(ui.run('api("GET", "/auth/session")'), /Login required/);
  assert.equal(ui.document.activeElement.id, 'loginPassword');
  ui.element('loginUsername').focus();
  await assert.rejects(ui.run('api("GET", "/auth/session")'), /Login required/);
  assert.equal(ui.document.activeElement.id, 'loginUsername');
});

test('security-service failures never reveal the dashboard', async () => {
  const ui = await adminSecuritySimulation({ load: false, methods: { AdminRequest: () => { throw new Error('security_unavailable'); } } });
  assert.equal(await ui.run('initializeAdminAuth()'), false);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'signed-out');
  assert.equal(ui.run('_adminPrincipal'), null);
  assert.match(ui.element('loginError').textContent, /security_unavailable/);
});

test('a malformed successful session envelope cannot open the dashboard', async () => {
  const ui = await adminSecuritySimulation({ load: false, api: (method, path) => ({ success: true, data: path === '/auth/status' ? status : {} }) });
  assert.equal(await ui.run('initializeAdminAuth()'), false);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'signed-out');
  assert.equal(ui.run('_adminPrincipal'), null);
});

test('the sign-in shell stays invisible during the pending check, so a reload does not flash it', () => {
  assert.match(styles, /html\[data-admin-auth="pending"\] \.auth-page\{[^}]*visibility:hidden[^}]*animation:auth-pending-reveal/);
  assert.match(styles, /@keyframes auth-pending-reveal\{to\{visibility:visible\}\}/);
});

test('the tab title is not "Sign in" while pending or after a verified session', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
  ui.document.documentElement.dataset.adminAuth = 'pending';
  ui.run('updateAdminAuthPageTitle()');
  assert.doesNotMatch(ui.document.title, /Sign in|登录/i);
  ui.document.title = 'Sign In | RouteNest'; // what a stale title looked like before the fix
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  assert.equal(ui.document.title, 'RouteNest Admin'); // no page chosen yet
});

test('the tab title names the current page, in the selected language', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  // The security/login fixtures do not include the app shell, so stand in for its top-bar title.
  const pageTitle = { textContent: '' };
  ui.run('globalThis.__lookup = _; globalThis._ = id => id === "currentPageTitle" ? globalThis.__pageTitle : globalThis.__lookup(id)');
  ui.context.__pageTitle = pageTitle;
  pageTitle.textContent = 'Quick Start';
  ui.run('updateAdminAuthPageTitle()');
  assert.equal(ui.document.title, 'Quick Start | RouteNest');
  pageTitle.textContent = '快速开始';
  ui.run('updateAdminAuthPageTitle()');
  assert.equal(ui.document.title, '快速开始 | RouteNest');
});

test('the tab title returns to "Sign in" once the session is gone', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  ui.run('renderAdminPrincipal(null)');
  assert.match(ui.document.title, /Sign in|登录/i);
});

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';
import { adminSecuritySimulation } from './admin_security_devices.test.mjs';

const login = readFileSync(new URL('../internal/admin/ui/html/pages/login.html', import.meta.url), 'utf8');
const recovery = readFileSync(new URL('../internal/admin/ui/html/dialogs/password_recovery.html', import.meta.url), 'utf8');
const shell = readFileSync(new URL('../internal/admin/ui/js/03_shell.js', import.meta.url), 'utf8');
const replacement = 'A'.repeat(43);
const status = { initialized: true, bootstrapRequired: false, recoveryRequired: false, legacyRotationRequired: false, recoveryCodeAvailable: true, ssoProviders: [] };

async function page(options = {}) {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true, authenticated: false, ...options });
  ui.context.nativeDesktopAvailable = () => false;
  ui.document.cookie = '';
  ui.context.fetch = async (url, request = {}) => {
    const method = request.method || 'GET';
    const path = url.slice('/admin/api'.length);
    ui.apiCalls.push([method, path]);
    const envelope = await (options.api || recoveryAPI)(method, path);
    return new Response(JSON.stringify(envelope), { status: envelope.success === false ? 401 : 200 });
  };
  assert.equal(await ui.run('initializeAdminAuth()'), false);
  return ui;
}

function fillRecovery(ui) {
  ui.element('passwordRecoveryCode').value = 'B'.repeat(43);
  ui.element('passwordRecoveryNewPassword').value = 'RecoveryPage8!';
  ui.element('passwordRecoveryConfirmPassword').value = 'RecoveryPage8!';
}

function recoveryAPI(method, path) {
  if (path === '/auth/status') return { success: true, data: status };
  if (path === '/auth/session') return { success: false, error: 'authentication_failed' };
  if (path === '/auth/recovery') return { success: true, data: { recoveryCode: replacement, shownOnce: true, loginRequired: true } };
  return { success: true, data: {} };
}

test('login and recovery are semantic page sections, not popup dialogs', () => {
  assert.match(login, /id="adminAuthPage"[^>]*class="auth-page"/);
  assert.match(login, /<header[^>]*class="auth-header"/);
  assert.match(login, /<main[^>]*id="adminAuthContent"/);
  assert.match(login, /<h1[^>]*id="loginTitle"/);
  assert.match(recovery, /<h1[^>]*id="passwordRecoveryTitle"/);
  assert.match(recovery, /id="passwordRecoveryBack"/);
  assert.doesNotMatch(login + recovery, /role="dialog"|aria-modal="true"|class="overlay-modal"|class="modal-close"/);
  assert.doesNotMatch(shell.match(/const _modalOverlays[^;]+;/)?.[0] || '', /#loginOverlay/);
});

test('first login has a shareable login route and only one active page', async () => {
  const ui = await page();
  assert.equal(ui.context.location.hash, '#login');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'login');
  assert.equal(ui.element('loginOverlay').style.display, 'flex');
  assert.equal(ui.element('passwordRecoveryOverlay').style.display, 'none');
  assert.equal(ui.element('adminAuthPage').hidden, false);
});

test('forgot password navigates to recovery instead of placing a modal over login', async () => {
  const ui = await page();
  ui.run('openPasswordRecovery()');
  assert.equal(ui.context.location.hash, '#recover');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recover');
  assert.equal(ui.element('loginOverlay').style.display, 'none');
  assert.equal(ui.element('passwordRecoveryOverlay').style.display, 'flex');
  assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
});

test('direct recovery navigation survives initialization without granting a session', async () => {
  const ui = await page({ hash: '#recover' });
  assert.equal(ui.context.location.hash, '#recover');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recover');
  assert.equal(ui.run('_adminPrincipal'), null);
  assert.equal(ui.apiCalls.filter(call => call[1] === '/auth/recovery').length, 0);
});

test('recovery is unavailable before setup or without a configured recovery credential', async () => {
  const ui = await page({ hash: '#recover', api: (method, path) => path === '/auth/status' ? { success: true, data: { ...status, recoveryCodeAvailable: false } } : { success: false, error: 'authentication_failed' } });
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'login');
  assert.equal(ui.context.location.hash, '#login');
  // The link stays (it explains how to recover); no reset page opens and nothing is revealed yet.
  assert.equal(ui.element('forgotPasswordButton').hidden, false);
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
});

test('back to sign in clears entered recovery secrets and does not change authority', async () => {
  const ui = await page();
  ui.run('openPasswordRecovery()');
  fillRecovery(ui);
  ui.run('closePasswordRecovery()');
  assert.equal(ui.context.location.hash, '#login');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'login');
  for (const id of ['passwordRecoveryCode', 'passwordRecoveryNewPassword', 'passwordRecoveryConfirmPassword']) assert.equal(ui.element(id).value, '');
  assert.equal(ui.apiCalls.filter(call => call[0] !== 'GET').length, 0);
});

test('browser back and forward switch pages without replaying recovery or preserving secret inputs', async () => {
  const ui = await page();
  ui.run('openPasswordRecovery()');
  fillRecovery(ui);
  ui.context.location.hash = '#login';
  ui.run('syncAdminAuthRoute()');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'login');
  assert.equal(ui.element('passwordRecoveryCode').value, '');
  ui.context.location.hash = '#recover';
  ui.run('syncAdminAuthRoute()');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recover');
  assert.equal(ui.element('passwordRecoveryCode').value, '');
  assert.equal(ui.apiCalls.filter(call => call[1] === '/auth/recovery').length, 0);
});

test('a completed reset remains on the recovery result page until save acknowledgement and Finish', async () => {
  const ui = await page({ api: recoveryAPI });
  ui.run('openPasswordRecovery()');
  fillRecovery(ui);
  await ui.run('submitPasswordRecovery()');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, replacement);
  assert.equal(ui.element('passwordRecoveryFinish').disabled, true);
  ui.context.location.hash = '#login';
  ui.run('syncAdminAuthRoute()');
  ui.run('closePasswordRecovery()');
  assert.equal(ui.context.location.hash, '#recover');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, replacement);
  ui.element('passwordRecoverySaved').checked = true;
  ui.run('updatePasswordRecoveryFinish()');
  ui.run('closePasswordRecovery()');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, replacement);
  ui.run('finishPasswordRecovery()');
  assert.equal(ui.context.location.hash, '#login');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, '');
  assert.equal(ui.run('_adminPrincipal'), null);
});

test('an in-flight recovery cannot be double-submitted or abandoned by history navigation', async () => {
  let release;
  const waiting = new Promise(resolve => { release = resolve; });
  const ui = await page({ api: (method, path) => path === '/auth/recovery' ? waiting : recoveryAPI(method, path) });
  ui.run('openPasswordRecovery()');
  fillRecovery(ui);
  const pending = ui.run('submitPasswordRecovery()');
  await ui.run('submitPasswordRecovery()');
  ui.context.location.hash = '#login';
  ui.run('syncAdminAuthRoute()');
  ui.run('closePasswordRecovery()');
  assert.equal(ui.context.location.hash, '#recover');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recover');
  assert.equal(ui.apiCalls.filter(call => call[1] === '/auth/recovery').length, 1);
  release(recoveryAPI('POST', '/auth/recovery'));
  await pending;
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
});

test('authentication restores the requested canonical destination without keeping login history state', async () => {
  const ui = await page({ hash: '#security' });
  assert.equal(ui.context.location.hash, '#login');
  ui.transport.authenticated = true;
  ui.context.fetch = async url => new Response(JSON.stringify({ success: true, data: url.endsWith('/auth/status') ? status : { principal: { userID: 'break-glass', username: 'admin', method: 'local', roles: ['break_glass'] } } }), { status: 200 });
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  assert.equal(ui.context.location.hash, '#security');
  assert.equal(ui.element('adminAuthPage').hidden, true);
  assert.equal(ui.document.documentElement.dataset.adminAuth, 'authenticated');
});

test('reloaded login restores only the remembered approved destination', async () => {
  const ui = await page({ hash: '#security' });
  ui.run('_adminAuthReturnTab = "dashboard"');
  ui.transport.authenticated = true;
  ui.context.fetch = async url => new Response(JSON.stringify({ success: true, data: url.endsWith('/auth/status') ? status : { principal: { userID: 'break-glass', username: 'admin', method: 'local', roles: ['break_glass'] } } }), { status: 200 });
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  assert.equal(ui.context.location.hash, '#security');
});

test('SSO remembers the requested administrator destination instead of the login page', async () => {
  const ui = await page({ hash: '#providers' });
  const body = { ...ui.element('loginSSOButtons'), querySelectorAll: () => [] };
  const getElementById = ui.document.getElementById.bind(ui.document);
  ui.document.getElementById = id => id === 'loginSSOButtons' ? body : getElementById(id);
  let request;
  ui.context.location.assign = () => {};
  ui.context.fetch = async (url, options) => { request = JSON.parse(options.body); return new Response(JSON.stringify({ success: true, data: { redirectURL: 'https://identity.example.test/authorize' } }), { status: 200 }); };
  await ui.run('startSSOLogin({id:"oidc-test",type:"oidc",enabled:true,ready:true})');
  assert.equal(request.returnLocation, '/admin/#providers');
});

test('native handoff retains a safe requested console destination across navigation', async () => {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true, authenticated: false, handoffToken: 'simulation-handoff', hash: '#desktop-handoff=simulation-handoff' });
  ui.context.history.state = { routenestAuthReturnTab: 'security' };
  assert.equal(await ui.run('initializeAdminAuth()'), true);
  assert.equal(ui.context.location.hash, '#security');
});

test('page navigation and browser history contain only non-secret presentation state', async () => {
  const ui = await page({ api: recoveryAPI });
  ui.run('openPasswordRecovery()');
  fillRecovery(ui);
  await ui.run('submitPasswordRecovery()');
  const history = JSON.stringify(ui.historyWrites) + JSON.stringify(ui.context.history.state);
  assert.doesNotMatch(history, /RecoveryPage8!|AAAAAA|BBBBBB|password|recoveryCode|csrf/i);
  assert.equal(ui.storageWrites.length, 0);
});

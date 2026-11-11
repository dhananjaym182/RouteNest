import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { adminSecuritySimulation } from './admin_security_devices.test.mjs';

const CODE = 'R'.repeat(43);
const base = { initialized: true, bootstrapRequired: false, recoveryRequired: false, legacyRotationRequired: false, recoveryCodeAvailable: true, ssoProviders: [] };
const principal = { userID: 'break-glass', username: 'admin', roles: ['break_glass'], method: 'local' };

// api(method, path) -> { status, body } ; defaults model a healthy server
async function page({ status = base, bootstrap } = {}) {
  const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true, authenticated: false });
  ui.context.nativeDesktopAvailable = () => false;
  ui.document.cookie = '';
  ui.posted = [];
  ui.context.fetch = async (url, request = {}) => {
    const method = request.method || 'GET';
    const path = url.slice('/admin/api'.length);
    if (method === 'POST') ui.posted.push([path, request.body ? JSON.parse(request.body) : null]);
    let reply = { status: 200, body: { success: true, data: {} } };
    if (path === '/auth/status') reply = { status: 200, body: { success: true, data: status } };
    else if (path === '/auth/session') reply = { status: 401, body: { success: false, error: 'authentication_failed' } };
    else if (path === '/auth/bootstrap' && bootstrap) reply = bootstrap;
    return new Response(JSON.stringify(reply.body), { status: reply.status });
  };
  assert.equal(await ui.run('initializeAdminAuth()'), false);
  return ui;
}
const setupStatus = { ...base, initialized: false, bootstrapRequired: true, recoveryCodeAvailable: false };
const setupOK = { status: 200, body: { success: true, data: { principal, recoveryCode: CODE, shownOnce: true } } };

function fillSetup(ui, secret = 'one-time-secret-value') {
  ui.element('bootstrapSecret').value = secret;
  ui.element('loginNewPassword').value = 'PermanentAdmin8!';
  ui.element('loginConfirmPassword').value = 'PermanentAdmin8!';
}

// ---- Fix 2: the code issued with the password is shown once and gated behind confirmation

test('web setup shows the recovery code once and only continues after the user confirms saving it', async () => {
  const ui = await page({ status: setupStatus, bootstrap: setupOK });
  assert.equal(ui.run('_adminAuthMode'), 'bootstrap');
  fillSetup(ui);
  await ui.run('submitLogin()');

  assert.deepEqual(ui.posted.map(call => call[0]), ['/auth/bootstrap']);
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, CODE);
  assert.equal(ui.element('passwordRecoveryResultTitle').textContent, 'Save your recovery code');
  assert.match(ui.element('passwordRecoveryResultIntro').textContent, /shown only once.*reset a forgotten password/);
  assert.equal(ui.element('passwordRecoveryFinish').textContent, 'Continue to the console');
  assert.equal(ui.element('passwordRecoveryFinish').disabled, true);
  assert.equal(ui.context.reloads, 0, 'the page must not reload (and lose the code) before confirmation');
  assert.equal(ui.element('passwordRecoveryRestartNotice').hidden, true);
  assert.match(ui.document.title, /Save your recovery code/);

  // typed secrets are wiped as soon as the code is shown
  for (const id of ['bootstrapSecret', 'loginNewPassword', 'loginConfirmPassword', 'loginPassword']) assert.equal(ui.element(id).value, '', id);

  ui.run('finishPasswordRecovery()'); // unchecked: must be a no-op
  assert.equal(ui.context.reloads, 0);
  ui.element('passwordRecoverySaved').checked = true;
  ui.run('updatePasswordRecoveryFinish()');
  assert.equal(ui.element('passwordRecoveryFinish').disabled, false);
  ui.run('finishPasswordRecovery()');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, '', 'the code is cleared from the page on continue');
  assert.equal(ui.context.reloads, 1, 'continuing reloads into the authenticated console');
});

test('navigating away cannot dismiss the one-time code screen before confirmation', async () => {
  const ui = await page({ status: setupStatus, bootstrap: setupOK });
  fillSetup(ui);
  await ui.run('submitLogin()');
  ui.context.location.hash = '#login';
  ui.run('syncAdminAuthRoute()');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, CODE);
});

test('a restart-required setup still delivers the code and explains the restart', async () => {
  const restart = { status: 503, body: { success: false, error: 'security_restart_required', data: { recoveryCode: CODE, shownOnce: true, restartRequired: true } } };
  const ui = await page({ status: setupStatus, bootstrap: restart });
  fillSetup(ui);
  await ui.run('submitLogin()');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, CODE);
  assert.equal(ui.element('passwordRecoveryRestartNotice').hidden, false);
  assert.match(ui.element('passwordRecoveryRestartNotice').textContent, /restart RouteNest, then reload this page and sign in/);
  assert.equal(ui.element('passwordRecoveryFinish').textContent, 'I restarted RouteNest, reload this page');
  ui.element('passwordRecoverySaved').checked = true;
  ui.run('updatePasswordRecoveryFinish()');
  ui.run('finishPasswordRecovery()');
  assert.equal(ui.context.reloads, 1);
});

test('a failed setup shows the error and never reveals a code screen', async () => {
  const ui = await page({ status: setupStatus, bootstrap: { status: 401, body: { success: false, error: 'authentication_failed' } } });
  fillSetup(ui, 'wrong');
  await ui.run('submitLogin()');
  assert.equal(ui.element('loginError').textContent, 'authentication_failed');
  assert.notEqual(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
  assert.equal(ui.element('passwordRecoveryReplacementCode').textContent, '');
  assert.equal(ui.context.reloads, 0);
});

test('a server that returns no code still signs in as before', async () => {
  const ui = await page({ status: setupStatus, bootstrap: { status: 200, body: { success: true, data: { principal } } } });
  fillSetup(ui);
  await ui.run('submitLogin()');
  assert.equal(ui.context.reloads, 1);
  assert.notEqual(ui.document.documentElement.dataset.adminAuthView, 'recovery-result');
});

test('a later password reset goes back to the reset wording, not the setup wording', async () => {
  const ui = await page({ status: base });
  ui.run('_passwordRecoverySetup = true');
  ui.run('openPasswordRecovery()');
  ui.element('passwordRecoveryCode').value = 'B'.repeat(43);
  ui.element('passwordRecoveryNewPassword').value = 'RecoveryPage8!';
  ui.element('passwordRecoveryConfirmPassword').value = 'RecoveryPage8!';
  const fetchBefore = ui.context.fetch;
  ui.context.fetch = async (url, request = {}) => url.endsWith('/auth/recovery')
    ? new Response(JSON.stringify({ success: true, data: { recoveryCode: CODE, shownOnce: true, loginRequired: true } }), { status: 200 })
    : fetchBefore(url, request);
  await ui.run('submitPasswordRecovery()');
  assert.equal(ui.element('passwordRecoveryResultTitle').textContent, 'Save the new recovery code');
  assert.match(ui.element('passwordRecoveryResultIntro').textContent, /old recovery code is no longer valid/i);
  assert.equal(ui.element('passwordRecoveryFinish').textContent, 'Return to sign in');
});

// ---- Fix 1: recovery guidance exists, but only appears when the user asks via "Forgot password?"

test('with no recovery code the guidance stays hidden until Forgot password is selected', async () => {
  const ui = await page({ status: { ...base, recoveryCodeAvailable: false } });
  assert.equal(ui.element('forgotPasswordButton').hidden, false, 'the link is how the user asks');
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true, 'nothing is exposed up front');
  assert.equal(ui.element('noRecoveryCodeHint').textContent, '', 'the text is not even in the page yet');
  assert.equal(ui.element('forgotPasswordButton').getAttribute('aria-expanded'), 'false');

  ui.run('onForgotPassword()');
  assert.equal(ui.element('noRecoveryCodeHint').hidden, false);
  const text = ui.element('noRecoveryCodeHint').textContent;
  assert.match(text, /No recovery code is set up/);
  assert.match(text, /security administrator/);
  assert.match(text, /routenest auth recover/);
  assert.doesNotMatch(text, /^Forgot your password/, 'the user just asked; do not repeat the question');
  assert.equal(ui.element('forgotPasswordButton').getAttribute('aria-expanded'), 'true');
  assert.notEqual(ui.document.documentElement.dataset.adminAuthView, 'recover', 'no reset page exists to open');

  ui.run('onForgotPassword()'); // selecting it again tucks the guidance away
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
  assert.equal(ui.element('noRecoveryCodeHint').textContent, '');
  assert.equal(ui.element('forgotPasswordButton').getAttribute('aria-expanded'), 'false');
});

test('with a recovery code Forgot password opens the reset page and never shows the guidance', async () => {
  const ui = await page({ status: base });
  assert.equal(ui.element('forgotPasswordButton').hidden, false);
  assert.equal(ui.element('forgotPasswordButton').getAttribute('aria-expanded'), null, 'it is a page link, not a disclosure');
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
  ui.run('onForgotPassword()');
  assert.equal(ui.document.documentElement.dataset.adminAuthView, 'recover');
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
  assert.equal(ui.element('noRecoveryCodeHint').textContent, '');
});

test('the guidance and the link never appear on the first-run setup page', async () => {
  const ui = await page({ status: setupStatus });
  assert.equal(ui.element('forgotPasswordButton').hidden, true);
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
  ui.run('onForgotPassword()'); // even if invoked, setup mode reveals nothing
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
});

test('revealed guidance is dismissed when the page re-renders its mode', async () => {
  const ui = await page({ status: { ...base, recoveryCodeAvailable: false } });
  ui.run('onForgotPassword()');
  assert.equal(ui.element('noRecoveryCodeHint').hidden, false);
  ui.run("setAdminAuthMode('login')");
  assert.equal(ui.element('noRecoveryCodeHint').hidden, true);
  assert.equal(ui.element('noRecoveryCodeHint').textContent, '');
});

// ---- Fix 3: where the one-time key comes from, per platform

test('first-run setup explains where to find the one-time key on every platform', async () => {
  const ui = await page({ status: setupStatus });
  const help = ui.element('bootstrapSecretHelp');
  assert.equal(help.hidden, false);
  const text = help.textContent || '';
  assert.match(text, /Where do I find this key\?/);
  assert.match(text, /terminal or console window that started it/);
  assert.match(text, /Windows, macOS, Linux/);
  assert.match(text, /ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD/);
  assert.match(text, /desktop app does not need this key/);
  assert.equal(ui.element('loginHelp').textContent, 'Enter the one-time key shown when RouteNest started, and set a permanent password.');
  assert.doesNotMatch(ui.element('loginHelp').textContent, /desktop dialog/);
});

test('the key-location help is hidden on the normal sign-in page', async () => {
  const ui = await page({ status: base });
  assert.equal(ui.element('bootstrapSecretHelp').hidden, true);
  assert.equal(ui.element('bootstrapSecretHelp').textContent, '');
});

test('the new login elements exist, are hidden by default, and are described for assistive tech', () => {
  const html = readFileSync(new URL('../internal/admin/ui/html/pages/login.html', import.meta.url), 'utf8');
  assert.match(html, /id="noRecoveryCodeHint"[^>]*\bhidden\b/);
  assert.match(html, /id="forgotPasswordButton"[^>]*aria-controls="noRecoveryCodeHint"[^>]*onclick="[^"]*onForgotPassword\(\)/);
  assert.match(html, /id="bootstrapSecretHelp"[^>]*\bhidden\b/);
  assert.match(html, /id="bootstrapSecret"[^>]*aria-describedby="bootstrapSecretHelp loginError"/);
  const recovery = readFileSync(new URL('../internal/admin/ui/html/dialogs/password_recovery.html', import.meta.url), 'utf8');
  for (const id of ['passwordRecoveryResultIntro', 'passwordRecoverySavedLabel']) assert.match(recovery, new RegExp('id="' + id + '"'));
});

// ---- Layout: expanding the guidance must not make the page jump

test('the sign-in layout is anchored so expanding content grows downward without moving the panels', () => {
  const css = readFileSync(new URL('../internal/admin/ui/css/100_ui_optimization.css', import.meta.url), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
  // card is top-anchored, not re-centred when its height changes
  assert.match(css, /\.auth-main\{[^}]*justify-content:flex-start/);
  assert.match(css, /\.auth-page>\.auth-main\{[^}]*align-items:flex-start/);
  // brand panel is pinned to the viewport, so a taller form never stretches or slides it
  assert.match(css, /\.auth-brand-panel\{[^}]*position:sticky[^}]*top:0[^}]*height:100dvh/);
  // a scrollbar appearing does not shift the layout sideways
  assert.match(css, /scrollbar-gutter:stable/);
});

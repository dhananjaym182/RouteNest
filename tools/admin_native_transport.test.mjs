import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

function nativeConsole({ native = true, bridgeMethod = true } = {}) {
  const calls = [];
  const elements = new Map();
  for (const id of ['loginSubmit', 'loginUsername', 'loginPassword', 'bootstrapSecret', 'loginNewPassword', 'loginConfirmPassword', 'loginError', 'desktopTrustLoginCheck', 'loginOverlay', 'desktopSignInError']) {
    elements.set(id, { value: '', textContent: '', disabled: false, checked: false, style: {}, focus() {} });
  }
  elements.get('loginUsername').value = 'operator';
  elements.get('loginPassword').value = ' exact password bytes ';
  const bridge = bridgeMethod ? {
    async AdminRequest(request) {
      calls.push(['AdminRequest', request]);
      return { status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, data: {} }), csrf: 'ephemeral-csrf' };
    }
  } : {};
  const context = vm.createContext({
    API: '/admin/api',
    _: id => elements.get(id) || null,
    document: { cookie: 'routenest_admin_csrf=browser-csrf' },
    window: { location: { protocol: 'wails:', host: 'wails' } },
    location: { replace: value => calls.push(['replace', value]), reload: () => calls.push(['reload']) },
    URL, Response,
    t: value => value, tx: value => value,
    nativeDesktopAvailable: () => native,
    nativeDesktopBridge: () => bridge,
    nativeDesktopCall: async (method, ...args) => {
      calls.push([method, ...args]);
      if (method === 'SignIn') return { adminURL: '/admin/', handoffToken: 'one-use-handoff' };
      return { success: true };
    },
    desktopSecurityErrorText: reason => String(reason?.message || reason?.code || reason),
    fetch: async (...args) => {
      calls.push(['fetch', ...args]);
      return new Response(JSON.stringify({ success: true, data: {} }), { status: 200 });
    },
    toast: (...args) => calls.push(['toast', ...args]),
    setTimeout: callback => callback(),
    navigator: {},
  });
  const source = readFileSync(new URL('../internal/admin/ui/js/03_shell.js', import.meta.url), 'utf8');
  vm.runInContext(source.slice(0, source.indexOf('async function openExternal')), context);
  return { context, elements, calls, run: source => vm.runInContext(source, context) };
}

function plain(value) {
  return JSON.parse(JSON.stringify(value));
}

test('native administrator requests retain only ephemeral CSRF and use the private bound transport', async () => {
  const ui = nativeConsole();
  const read = await ui.run("adminFetch('GET', '/auth/session')");
  assert.equal(read.status, 200);
  await ui.run("adminFetch('POST', '/auth/password', {oldPassword:'old',newPassword:'new password'})");
  const requests = ui.calls.filter(call => call[0] === 'AdminRequest');
  assert.equal(requests.length, 2);
  assert.equal(requests[0][1].path, '/admin/api/auth/session');
  assert.deepEqual(plain(requests[1][1]), { method: 'POST', path: '/admin/api/auth/password', body: '{"oldPassword":"old","newPassword":"new password"}', csrf: 'ephemeral-csrf' });
  assert.equal(ui.calls.some(call => call[0] === 'fetch'), false);
});

test('denied native mutation retains the current CSRF for retry, while expired sessions clear it', async () => {
  const ui = nativeConsole();
  await ui.run("adminFetch('GET', '/auth/session')");
  let status = 403;
  ui.context.nativeDesktopBridge = () => ({ async AdminRequest(request) {
    ui.calls.push(['AdminRequest', request]);
    return { status, body: '{"error":"permission_denied"}', contentType: 'application/json' };
  } });
  await ui.run("adminFetch('POST', '/auth/users', {})");
  await ui.run("adminFetch('POST', '/auth/users', {})");
  let calls = ui.calls.filter(call => call[0] === 'AdminRequest');
  assert.equal(calls[2][1].csrf, 'ephemeral-csrf');
  status = 401;
  await ui.run("adminFetch('GET', '/auth/session')");
  await ui.run("adminFetch('POST', '/logout', {})");
  calls = ui.calls.filter(call => call[0] === 'AdminRequest');
  assert.equal(calls.at(-1)[1].csrf, '');
});

test('native transport failure never falls back to the ordinary HTTP listener', async () => {
  const ui = nativeConsole({ bridgeMethod: false });
  await assert.rejects(ui.run("adminFetch('GET', '/auth/session')"));
  assert.equal(ui.calls.some(call => call[0] === 'fetch'), false);
});

test('ordinary browsers retain independent session cookies and CSRF', async () => {
  const ui = nativeConsole({ native: false });
  await ui.run("adminFetch('POST', '/logout', {})");
  const request = ui.calls.find(call => call[0] === 'fetch');
  assert.equal(request[1], '/admin/api/logout');
  assert.equal(request[2].headers['X-RouteNest-CSRF'], 'browser-csrf');
  assert.equal(ui.calls.some(call => call[0] === 'AdminRequest'), false);
});

test('unchecked native trust still synchronizes the requested local administrator session', async () => {
  const ui = nativeConsole();
  await ui.run('submitLogin()');
  const login = ui.calls.find(call => call[0] === 'SignIn');
  assert.ok(login);
  assert.deepEqual(plain(login[1]), { username: 'operator', password: ' exact password bytes ', trustDevice: false });
  assert.deepEqual(ui.calls.find(call => call[0] === 'replace'), ['replace', '/admin/#desktop-handoff=one-use-handoff']);
  assert.equal(ui.calls.some(call => call[0] === 'fetch'), false);
});

test('native sign-out uses the zero-argument atomic transition without requiring a password', async () => {
  const ui = nativeConsole();
  await ui.run('logoutAdmin()');
  assert.deepEqual(ui.calls.find(call => call[0] === 'SignOutDesktop'), ['SignOutDesktop']);
  assert.equal(ui.calls.some(call => call[0] === 'SetDesktopAutoSignIn' || call[0] === 'fetch'), false);
});

test('native gateway examples use the actual owned gateway, never the Wails asset scheme', async () => {
  const ui = nativeConsole();
  ui.context.nativeDesktopCall = async method => method === 'DesktopStatus' ? { gatewayURL: 'http://127.0.0.1:3467/v1' } : {};
  await ui.run('initializeNativeConsoleTransport()');
  assert.equal(ui.run('gatewayV1URL()'), 'http://127.0.0.1:3467/v1');
});

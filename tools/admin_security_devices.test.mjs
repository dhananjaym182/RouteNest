import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import vm from 'node:vm';
import { simulationDocumentFromHTML } from './desktop_auth.test.mjs';

const moduleSource = readFileSync(new URL('../internal/admin/ui/js/06_security_devices.js', import.meta.url), 'utf8');
const securityHTML = readFileSync(new URL('../internal/admin/ui/html/pages/security.html', import.meta.url), 'utf8');
const shellSource = readFileSync(new URL('../internal/admin/ui/js/03_shell.js', import.meta.url), 'utf8');
const authShellSource = shellSource.slice(0, shellSource.indexOf('async function openExternal('));
const recoverySource = readFileSync(new URL('../internal/admin/ui/js/03_recovery.js', import.meta.url), 'utf8');
const loginHTML = readFileSync(new URL('../internal/admin/ui/html/pages/login.html', import.meta.url), 'utf8');
const recoveryHTML = readFileSync(new URL('../internal/admin/ui/html/dialogs/password_recovery.html', import.meta.url), 'utf8');
const dictionarySource = readFileSync(new URL('../internal/admin/ui/js/01_i18n.js', import.meta.url), 'utf8');
const clone = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));

export const simulationPendingTranslations = {
  '登录已失效，请使用密码重新登录。': 'Your session has expired. Sign in again with your password.',
  '此操作需要安全管理员权限。': 'This action requires security administrator permissions.',
  '缺少本机桌面凭据。请使用密码登录，并按需信任此设备。': 'The local desktop credential is missing. Sign in with your password and trust this device only if needed.',
  '此设备已被撤销。请使用密码登录；仅在确认设备安全后重新信任。': 'This device was revoked. Sign in with your password; trust it again only after verifying that the device is secure.',
  '此设备凭据已过期。请使用密码重新信任此设备。': 'This device credential has expired. Use your password to trust this device again.',
  '自动登录已暂停。密码登录不会自动恢复；请在此明确开启。': 'Automatic sign-in is paused. Password sign-in will not resume it; turn it on here explicitly.',
  '请明确确认删除本机桌面凭据。': 'Explicitly confirm removal of the local desktop credential.',
  '安全更新需要重启 RouteNest 后完成。': 'Restart RouteNest to complete the security update.',
  '安全服务不可用，请保留现有数据目录并重试。': 'The security service is unavailable. Keep the existing data directory and try again.',
  '尝试次数过多，请稍后重试。': 'Too many attempts. Please wait and try again.',
  '无法验证设备状态。请刷新后重试；密码登录仍可使用。': 'Device status could not be verified. Refresh and try again; password sign-in is still available.',
  '已撤销': 'Revoked',
  '有效期未知': 'Expiry unknown',
  '撤销此设备并删除本机桌面凭据？': 'Revoke this device and delete its local desktop credential?',
  '撤销所有受信任桌面设备并删除本机桌面凭据？': 'Revoke all trusted desktop devices and delete the local desktop credential?',
  '删除本机桌面凭据？服务器上的设备记录不会自动撤销。': 'Delete the local desktop credential? Device records on the server will not be revoked automatically.',
  '本机桌面凭据已删除。请检查设备列表，并按需撤销服务器上的记录。': 'The local desktop credential was deleted. Review the device list and revoke server records if needed.'
};

export function simulationTrustedDevice(overrides = {}) {
  return {
    id: 'simulation-device', userID: 'break-glass',
    metadata: { name: 'Simulation workstation', platform: 'Windows' },
    createdAt: '2026-01-01T10:00:00Z', expiresAt: '2099-04-01T10:00:00Z',
    lastUsedAt: '0001-01-01T00:00:00Z', automaticSignIn: true,
    ...overrides
  };
}

export function validateSimulationAdminRequest(args) {
  assert.equal(args.length, 1, 'Simulation AdminRequest requires exactly one argument');
  const request = args[0];
  assert.ok(request && typeof request === 'object' && !Array.isArray(request), 'Simulation AdminRequest requires an object');
  assert.ok(['GET', 'HEAD', 'OPTIONS', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method), 'Simulation AdminRequest requires a supported uppercase method');
  assert.equal(typeof request.path, 'string', 'Simulation AdminRequest path must be a string');
  assert.ok(request.path.startsWith('/admin/api/') && !request.path.includes('\\') && !request.path.includes('#'), 'Simulation AdminRequest requires an administrator API path');
  for (const field of Object.keys(request)) {
    assert.ok(['method', 'path', 'body', 'csrf'].includes(field), 'Simulation AdminRequest contains an unexpected field');
    assert.equal(typeof request[field], 'string', 'Simulation AdminRequest fields must be strings');
  }
  if (Object.hasOwn(request, 'body')) assert.doesNotThrow(() => JSON.parse(request.body), 'Simulation AdminRequest body must be serialized JSON');
  return request;
}

export async function adminSecuritySimulation(options = {}) {
  const document = simulationDocumentFromHTML(securityHTML + loginHTML + recoveryHTML);
  const calls = [];
  const apiCalls = [];
  const navigations = [];
  const confirmations = [];
  const toasts = [];
  const storageWrites = [];
  const fetchCalls = [];
  const historyWrites = [];
  const transport = { authenticated: options.authenticated !== false, csrf: 'simulation-session-csrf', pendingHandoff: options.handoffToken || '' };
  const devices = clone(options.devices || [simulationTrustedDevice()]);
  const nativeState = clone(options.native || {
    deviceID: 'simulation-device', storage: 'stored', automaticSignIn: true, deviceStatus: 'trusted',
    credentialStorage: { available: true, stored: true, state: 'stored', automaticSignIn: true }
  });
  const bridge = {};
  const helpers = { document, calls, devices, nativeState, transport };
  const simulatedAPI = async (...args) => {
    apiCalls.push(clone(args));
    if (options.api) return options.api(...args, helpers);
    const [method, path, body] = args;
    if (method === 'GET' && path === '/auth/status') return { success: true, data: { initialized: true, bootstrapRequired: false, recoveryRequired: false, legacyRotationRequired: false, recoveryCodeAvailable: true, ssoProviders: [] } };
    if (method === 'GET' && path === '/auth/session') return transport.authenticated
      ? { success: true, data: { principal: { userID: 'break-glass', username: 'admin', method: 'local', roles: ['break_glass'] } } }
      : { success: false, error: 'authentication_failed' };
    if (method === 'POST' && path === '/auth/desktop-handoff') {
      if (!transport.pendingHandoff || body?.token !== transport.pendingHandoff) return { success: false, error: 'desktop_handoff_failed' };
      transport.pendingHandoff = '';
      transport.authenticated = true;
      return { success: true, data: {} };
    }
    if (method === 'GET' && path === '/auth/trusted-devices') return { success: true, data: { devices: clone(devices) } };
    if (path === '/auth/trusted-devices/revoke') {
      const index = devices.findIndex(device => device.id === body.deviceID);
      if (index >= 0) devices.splice(index, 1);
    }
    if (path === '/auth/trusted-devices/revoke-all') devices.splice(0, devices.length);
    return { success: true, data: {} };
  };
  const defaults = {
    AdminRequest: async request => {
      const path = request.path.slice('/admin/api'.length);
      if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method) && path !== '/auth/desktop-handoff' && request.csrf !== transport.csrf) return { status: 403, body: JSON.stringify({ success: false, error: 'csrf_failed' }), contentType: 'application/json', csrf: transport.authenticated ? transport.csrf : '' };
      const args = [request.method, path];
      if (Object.hasOwn(request, 'body')) args.push(JSON.parse(request.body));
      const envelope = await simulatedAPI(...args);
      const errorCode = typeof envelope?.error === 'string' ? envelope.error : envelope?.error?.code;
      const status = envelope?.success === false ? ({ authentication_failed: 401, desktop_handoff_failed: 401, permission_denied: 403, rate_limited: 429, security_unavailable: 503 })[errorCode] || 400 : 200;
      return { status, body: JSON.stringify(envelope), contentType: 'application/json', csrf: transport.authenticated ? transport.csrf : '' };
    },
    DesktopStatus: () => ({ gatewayURL: 'http://127.0.0.1:3467/v1', ...clone(nativeState) }),
    TrustThisDevice: () => {
      Object.assign(nativeState, { deviceID: 'simulation-device', storage: 'stored', automaticSignIn: true, deviceStatus: 'trusted', credentialStorage: { available: true, stored: true, state: 'stored', automaticSignIn: true } });
      if (!devices.some(device => device.id === 'simulation-device')) devices.push(simulationTrustedDevice());
      return clone(nativeState);
    },
    SetDesktopAutoSignIn: request => {
      nativeState.automaticSignIn = request.enabled;
      nativeState.deviceStatus = request.enabled ? 'trusted' : 'paused';
      nativeState.credentialStorage.automaticSignIn = request.enabled;
      devices.find(device => device.id === nativeState.deviceID).automaticSignIn = request.enabled;
      return clone(nativeState);
    },
    ForgetThisDevice: () => {
      devices.splice(0, devices.length);
      Object.assign(nativeState, { deviceID: '', storage: 'missing', deviceStatus: 'missing', automaticSignIn: false, credentialStorage: { available: true, stored: false, state: 'missing', automaticSignIn: false } });
      return clone(nativeState);
    },
    RemoveLocalDesktopCredential: () => {
      Object.assign(nativeState, { deviceID: '', storage: 'missing', deviceStatus: 'missing', automaticSignIn: false, credentialStorage: { available: true, stored: false, state: 'missing', automaticSignIn: false } });
      return clone(nativeState);
    },
    SignOutDesktop: () => {
      transport.authenticated = false;
      transport.pendingHandoff = '';
      nativeState.automaticSignIn = false;
      if (nativeState.credentialStorage) nativeState.credentialStorage.automaticSignIn = false;
      const current = devices.find(device => device.id === nativeState.deviceID);
      if (current) current.automaticSignIn = false;
      return { signedOut: true, automaticSignIn: false };
    },
    LockDesktop: () => { transport.authenticated = false; transport.pendingHandoff = ''; return { locked: true }; },
    SignIn: () => { transport.authenticated = true; transport.pendingHandoff = 'simulation-shell-handoff'; return { handoffToken: transport.pendingHandoff, adminURL: '/admin/' }; },
    AutomaticSignIn: () => { throw new Error('Simulation: automatic sign-in must not be called from the dashboard'); }
  };
  const objectFields = {
    TrustThisDevice: ['currentPassword'], SetDesktopAutoSignIn: ['currentPassword', 'enabled'],
    ForgetThisDevice: ['currentPassword', 'confirmRemoval'], RemoveLocalDesktopCredential: ['confirmRemoval'],
    SignIn: ['username', 'password', 'trustDevice']
  };
  for (const method of Object.keys(defaults)) bridge[method] = async (...args) => {
    calls.push({ name: method, args: clone(args) });
    if (method === 'AdminRequest') validateSimulationAdminRequest(args);
    else if (objectFields[method]) {
      assert.equal(args.length, 1, 'Simulation ' + method + ' requires exactly one argument');
      assert.ok(args[0] && typeof args[0] === 'object' && !Array.isArray(args[0]), 'Simulation ' + method + ' requires an object');
      assert.deepEqual(Object.keys(args[0]).sort(), [...objectFields[method]].sort(), 'Simulation ' + method + ' fields differ');
      for (const [field, value] of Object.entries(args[0])) assert.equal(typeof value, ['enabled', 'confirmRemoval', 'trustDevice'].includes(field) ? 'boolean' : 'string', 'Simulation ' + method + ' field type differs');
    } else assert.equal(args.length, 0, 'Simulation ' + method + ' requires no arguments');
    return (options.methods?.[method] || defaults[method])(...args, helpers);
  };
  const context = vm.createContext({
    document,
    _: id => document.getElementById(id),
    LANG: options.language || 'en',
    adminHasPermission: () => options.permission !== false,
    formatDateTime: value => new Date(value).toLocaleString(context.LANG === 'en' ? 'en-US' : 'zh-CN'),
    location: { href: 'http://127.0.0.1/admin/', origin: 'http://127.0.0.1', protocol: 'http:', host: '127.0.0.1', pathname: '/admin/', search: '', hash: options.hash || '', reload: () => { context.reloads++; }, replace: target => navigations.push(target) },
    history: {
      state: null,
      replaceState: (state, title, target) => { historyWrites.push(target); context.history.state = state; context.location.hash = new URL(target, context.location.href).hash; },
      pushState: (state, title, target) => { historyWrites.push(target); context.history.state = state; context.location.hash = new URL(target, context.location.href).hash; }
    },
    addEventListener: () => {},
    localStorage: { getItem: () => null, setItem: (...args) => storageWrites.push(['localStorage', ...args]) },
    sessionStorage: { getItem: () => null, setItem: (...args) => storageWrites.push(['sessionStorage', ...args]) },
    fetch: async (...args) => { fetchCalls.push(clone(args)); throw new Error('Simulation: native transport must not fall back to browser fetch'); },
    reloads: 0,
    API: '/admin/api',
    Response, URL,
    setTimeout: () => 0,
    go: options.browser ? undefined : { main: { DesktopSetupBridge: bridge } },
    toast: (message, tone) => toasts.push({ message, tone }),
    showConfirm: async message => { confirmations.push(message); return options.confirm !== false; },
    api: simulatedAPI
  });
  context.window = context;
  vm.runInContext(dictionarySource, context);
  context.pendingTranslations = simulationPendingTranslations;
  vm.runInContext('for (const [key, value] of Object.entries(pendingTranslations)) if (!I18N[key]) I18N[key] = value; function t(key) { return LANG === "en" ? I18N[key] || key : key; }', context);
  vm.runInContext(moduleSource, context, { filename: fileURLToPath(new URL('../internal/admin/ui/js/06_security_devices.js', import.meta.url)) });
  const run = source => vm.runInContext(source, context);
  const simulatedPermission = context.adminHasPermission;
  vm.runInContext(authShellSource, context);
  vm.runInContext(recoverySource, context);
  if (options.useNativeAPI !== true) context.api = simulatedAPI;
  context.adminHasPermission = simulatedPermission;
  if (options.load !== false) await run('loadTrustedDevices()');
  return {
    document, calls, apiCalls, navigations, confirmations, toasts, nativeState, devices, context, run, transport, storageWrites, fetchCalls, historyWrites,
    element: id => document.getElementById(id),
    callsFor: name => calls.filter(call => call.name === name),
    password: value => { document.getElementById('desktopTrustCurrentPassword').value = value; }
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  test('simulation: AdminRequest validates and records its real arity, method, fields, and JSON body', async () => {
    const ui = await adminSecuritySimulation({ load: false });
    const invalid = [
      [], [null], ['GET', '/admin/api/auth/status'],
      [{ method: 'get', path: '/admin/api/auth/status' }],
      [{ method: 'TRACE', path: '/admin/api/auth/status' }],
      [{ method: 'GET', path: '/admin/api/auth/status', extra: 'forbidden' }],
      [{ method: 'GET', path: 'https://example.invalid/admin/api/auth/status' }],
      [{ method: 'POST', path: '/admin/api/auth/trusted-devices/revoke', body: {} }],
      [{ method: 'POST', path: '/admin/api/auth/trusted-devices/revoke', body: '{' }],
      [{ method: 'POST', path: '/admin/api/auth/trusted-devices/revoke', body: '{}', csrf: 7 }]
    ];
    for (const args of invalid) {
      await assert.rejects(ui.context.go.main.DesktopSetupBridge.AdminRequest(...args), /Simulation AdminRequest/);
      assert.deepEqual(ui.callsFor('AdminRequest').at(-1).args, args);
    }
    assert.equal(ui.apiCalls.length, 0);
    const reply = await ui.context.go.main.DesktopSetupBridge.AdminRequest({ method: 'GET', path: '/admin/api/auth/status' });
    assert.equal(reply.status, 200);
    assert.equal(reply.contentType, 'application/json');
    assert.equal(reply.csrf, 'simulation-session-csrf');
    assert.equal(JSON.parse(reply.body).success, true);
    assert.deepEqual(ui.apiCalls, [['GET', '/auth/status']]);
  });

  test('simulation: shipped adminFetch sends one AdminRequest and carries only ephemeral returned CSRF', async () => {
    const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
    const first = await ui.run('adminFetch("GET", "/auth/trusted-devices")');
    assert.equal(first.status, 200);
    assert.equal((await first.json()).data.devices[0].id, 'simulation-device');
    assert.equal(ui.run('_nativeAdminCSRF'), 'simulation-session-csrf');
    await ui.run('adminFetch("POST", "/auth/trusted-devices/revoke", { deviceID: "simulation-device", currentPassword: "SimulationCurrent8!" })');
    assert.deepEqual(ui.callsFor('AdminRequest').map(call => call.args), [
      [{ method: 'GET', path: '/admin/api/auth/trusted-devices' }],
      [{ method: 'POST', path: '/admin/api/auth/trusted-devices/revoke', body: '{"deviceID":"simulation-device","currentPassword":"SimulationCurrent8!"}', csrf: 'simulation-session-csrf' }]
    ]);
    assert.deepEqual(ui.apiCalls.at(-1), ['POST', '/auth/trusted-devices/revoke', { deviceID: 'simulation-device', currentPassword: 'SimulationCurrent8!' }]);
    assert.equal(ui.fetchCalls.length, 0);
    assert.equal(ui.storageWrites.length, 0);
  });

  test('simulation: native initialization redeems a handoff once and loads status and session through AdminRequest', async () => {
    const token = 'simulation-handoff+/=';
    const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true, authenticated: false, handoffToken: token, hash: '#desktop-handoff=' + encodeURIComponent(token) });
    assert.equal(await ui.run('initializeAdminAuth()'), true);
    assert.deepEqual(ui.callsFor('DesktopStatus')[0].args, []);
    assert.deepEqual(ui.callsFor('AdminRequest').map(call => call.args), [
      [{ method: 'POST', path: '/admin/api/auth/desktop-handoff', body: JSON.stringify({ token }), csrf: '' }],
      [{ method: 'GET', path: '/admin/api/auth/status' }],
      [{ method: 'GET', path: '/admin/api/auth/session' }]
    ]);
    assert.deepEqual(ui.historyWrites, ['/admin/#dashboard']);
    assert.equal(ui.context.location.hash, '#dashboard');
    assert.equal(ui.run('_adminPrincipal.username'), 'admin');
    assert.equal(ui.run('gatewayV1URL()'), 'http://127.0.0.1:3467/v1');
    assert.equal(ui.run('_nativeAdminCSRF'), 'simulation-session-csrf');
    await ui.run('initializeAdminAuth()');
    assert.equal(ui.apiCalls.filter(call => call[1] === '/auth/desktop-handoff').length, 1);
    assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
    assert.equal(ui.storageWrites.length, 0);
    assert.equal(ui.fetchCalls.length, 0);
  });

  test('simulation: sign-out clears CSRF and reinitialization remains logged out without automatic sign-in', async () => {
    const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true });
    assert.equal(await ui.run('initializeAdminAuth()'), true);
    await ui.run('logoutAdmin()');
    assert.equal(ui.run('_nativeAdminCSRF'), '');
    assert.equal(ui.run('_adminPrincipal'), null);
    assert.equal(ui.context.reloads, 1);
    assert.equal(await ui.run('initializeAdminAuth()'), false);
    assert.equal(ui.element('loginOverlay').style.display, 'flex');
    assert.equal(ui.run('_nativeAdminCSRF'), '');
    assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
    assert.equal(ui.storageWrites.length, 0);
  });

  test('simulation: AdminRequest status and error envelopes survive the Response adapter', async () => {
    const ui = await adminSecuritySimulation({ load: false, useNativeAPI: true, api: () => ({ success: false, error: 'security_unavailable' }) });
    const response = await ui.run('adminFetch("GET", "/auth/status")');
    assert.equal(response.status, 503);
    assert.equal(response.ok, false);
    assert.deepEqual(await response.json(), { success: false, error: 'security_unavailable' });
    await assert.rejects(ui.run('api("GET", "/auth/status")'), /security_unavailable/);
    assert.equal(await ui.run('initializeAdminAuth()'), false);
    assert.match(ui.element('loginError').textContent, /security_unavailable/);
    assert.equal(ui.fetchCalls.length, 0);
  });

  test('simulation: malformed and rejected AdminRequest replies never fall back to browser fetch', async () => {
    for (const reply of [null, { status: '200', body: '{}' }, { status: 200, body: {} }, { status: 99, body: '{}' }]) {
      const ui = await adminSecuritySimulation({ load: false, methods: { AdminRequest: () => reply } });
      await assert.rejects(ui.run('adminFetch("GET", "/auth/status")'), /invalid_response/);
      assert.equal(ui.fetchCalls.length, 0);
    }
    const ui = await adminSecuritySimulation({ load: false, methods: { AdminRequest: () => { throw new Error('simulation-native-transport-failed'); } } });
    await assert.rejects(ui.run('adminFetch("GET", "/auth/status")'), /simulation-native-transport-failed/);
    assert.equal(ui.fetchCalls.length, 0);
    assert.equal(ui.storageWrites.length, 0);
  });

  test('simulation: a response without CSRF clears the previous ephemeral token', async () => {
    const ui = await adminSecuritySimulation({ load: false, methods: { AdminRequest: () => ({ status: 200, body: '{"success":true}', contentType: 'application/json' }) } });
    ui.run('_nativeAdminCSRF = "simulation-stale-csrf"');
    await ui.run('adminFetch("GET", "/auth/status")');
    assert.equal(ui.run('_nativeAdminCSRF'), '');
    assert.equal(ui.storageWrites.length, 0);
  });

  test('simulation: loading trusted devices uses no-argument DesktopStatus and never automatic sign-in', async () => {
    const ui = await adminSecuritySimulation();
    await ui.run('loadTrustedDevices()');
    assert.deepEqual(ui.callsFor('DesktopStatus'), [{ name: 'DesktopStatus', args: [] }, { name: 'DesktopStatus', args: [] }]);
    assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
    assert.equal(ui.element('trustThisDeviceButton').hidden, true);
    assert.equal(ui.element('toggleDesktopAutoSignInButton').hidden, false);
    assert.equal(ui.element('toggleDesktopAutoSignInButton').getAttribute('aria-pressed'), 'true');
  });

  test('simulation: the effective toggle combines server and local flags, and sends the displayed target state', async () => {
    for (const [serverEnabled, localEnabled] of [[true, true], [true, false], [false, true], [false, false]]) {
      const ui = await adminSecuritySimulation({ devices: [simulationTrustedDevice({ automaticSignIn: serverEnabled })], native: { deviceID: 'simulation-device', deviceStatus: 'trusted', credentialStorage: { available: true, stored: true, state: 'stored', automaticSignIn: localEnabled } } });
      const effective = serverEnabled && localEnabled;
      assert.equal(ui.element('toggleDesktopAutoSignInButton').getAttribute('aria-pressed'), String(effective));
      assert.equal(ui.element('toggleDesktopAutoSignInButton').textContent, effective ? 'Turn off automatic sign-in' : 'Turn on automatic sign-in');
      assert.equal(ui.element('trustThisDeviceButton').hidden, true);
      ui.password('SimulationCurrent8!');
      await ui.run('toggleDesktopAutoSignIn()');
      assert.deepEqual(ui.callsFor('SetDesktopAutoSignIn')[0].args, [{ currentPassword: 'SimulationCurrent8!', enabled: !effective }]);
      assert.equal(ui.element('toggleDesktopAutoSignInButton').getAttribute('aria-pressed'), String(!effective));
      assert.equal(ui.element('desktopTrustCurrentPassword').value, '');
    }
  });

  test('simulation: expiration, revocation, missing storage, corrupt storage, unavailable storage, and paused state stay distinct', async () => {
    const notices = [];
    const cases = [
      ['expired', [simulationTrustedDevice({ expiresAt: '2000-01-01T00:00:00Z' })], 'stored', true],
      ['revoked', [], 'stored', true],
      ['missing', [], 'missing', false],
      ['corrupt', [], 'corrupt', false],
      ['unavailable', [], 'unavailable', false],
      ['paused', [simulationTrustedDevice()], 'stored', true]
    ];
    for (const [status, devices, storage, stored] of cases) {
      const ui = await adminSecuritySimulation({ devices, native: { deviceID: stored ? 'simulation-device' : '', credentialStorage: { available: status !== 'unavailable', state: storage, stored, automaticSignIn: status !== 'paused' } } });
      assert.equal(ui.run('trustedDesktopModel().status'), status);
      notices.push(ui.element('desktopSignInGuidance').textContent);
      assert.equal(ui.element('toggleDesktopAutoSignInButton').hidden, status !== 'paused');
      if (['corrupt', 'unavailable'].includes(status)) {
        assert.equal(ui.element('trustThisDeviceButton').disabled, true);
        assert.equal(ui.element('forgetThisDeviceButton').hidden, false);
      }
    }
    assert.equal(new Set(notices).size, cases.length);
  });

  test('simulation: device rows render metadata, expiry, creation and safe zero-date labels without HTML injection', async () => {
    const ui = await adminSecuritySimulation({ devices: [simulationTrustedDevice({ metadata: { name: '<img src=x onerror=alert(1)>', platform: 'Simulation Linux' } }), simulationTrustedDevice({ id: 'expired-device', metadata: { name: 'Expired simulator', platform: 'macOS' }, expiresAt: '2000-01-01T00:00:00Z', lastUsedAt: '2020-01-01T00:00:00Z' })] });
    const list = ui.element('trustedDevicesList');
    assert.equal(list.getAttribute('role'), 'list');
    assert.equal(list.children.length, 2);
    assert.equal(list.children[0].getAttribute('role'), 'listitem');
    assert.match(list.textContent, /<img src=x onerror=alert\(1\)>/);
    assert.equal(list.querySelectorAll('img').length, 0);
    assert.match(list.textContent, /Simulation Linux/);
    assert.match(list.textContent, /Never used/);
    assert.match(list.textContent, /Expires/);
    assert.match(list.textContent, /2099/);
    assert.match(list.textContent, /Expired/);
    assert.doesNotMatch(list.textContent, /0001|Invalid Date/);
    assert.match(list.querySelectorAll('button')[0].getAttribute('aria-label'), /Revoke · <img/);
    assert.match(ui.element('desktopLastUsed').textContent, /Never used/);
    assert.equal(list.querySelectorAll('time').length, 5);
  });

  test('simulation: missing or invalid expiry never presents a trusted device as valid', async () => {
    for (const expiresAt of ['', '0001-01-01T00:00:00Z', 'invalid-date']) {
      const ui = await adminSecuritySimulation({ devices: [simulationTrustedDevice({ expiresAt })] });
      assert.equal(ui.run('trustedDesktopModel().trusted'), false);
      assert.equal(ui.element('toggleDesktopAutoSignInButton').hidden, true);
      assert.match(ui.element('trustedDevicesList').textContent, /Expiry unknown/);
      assert.doesNotMatch(ui.element('trustedDevicesList').textContent, /Invalid Date|0001/);
    }
  });

  test('simulation: native trust sends only the current password and does not silently remove corrupt credentials', async () => {
    const ui = await adminSecuritySimulation({ devices: [], native: { storage: 'missing', deviceID: '', credentialStorage: { available: true, stored: false, state: 'missing', automaticSignIn: false } } });
    ui.password('SimulationCurrent8!');
    ui.element('trustThisDeviceButton').focus();
    await ui.run('trustThisDesktop()');
    assert.deepEqual(ui.callsFor('TrustThisDevice')[0].args, [{ currentPassword: 'SimulationCurrent8!' }]);
    assert.equal(ui.callsFor('RemoveLocalDesktopCredential').length, 0);
    assert.equal(ui.element('trustThisDeviceButton').hidden, true);
    assert.equal(ui.document.activeElement.id, 'toggleDesktopAutoSignInButton');
    for (const storage of ['corrupt', 'unavailable']) {
      const blocked = await adminSecuritySimulation({ devices: [], native: { storage, credentialStorage: { available: storage !== 'unavailable', stored: false, state: storage } } });
      blocked.password('SimulationCurrent8!');
      await blocked.run('trustThisDesktop()');
      assert.equal(blocked.callsFor('TrustThisDevice').length, 0);
      assert.equal(blocked.callsFor('RemoveLocalDesktopCredential').length, 0);
      assert.equal(blocked.confirmations.length, 0);
    }
  });

  test('simulation: revoked-device forgetting authenticates, while corrupt or unavailable cleanup explicitly removes only the local credential', async () => {
    for (const state of ['corrupt', 'unavailable', 'revoked']) {
      const ui = await adminSecuritySimulation({ devices: [], native: { storage: state === 'revoked' ? 'stored' : state, deviceID: state === 'revoked' ? 'simulation-revoked' : '', credentialStorage: { available: state !== 'unavailable', stored: state === 'revoked', state: state === 'revoked' ? 'stored' : state } } });
      if (state === 'revoked') ui.password('SimulationCurrent8!');
      await ui.run('forgetThisDesktop()');
      assert.equal(ui.confirmations.length, 1);
      assert.match(ui.confirmations[0], /delete/i);
      if (state === 'revoked') {
        assert.deepEqual(ui.callsFor('ForgetThisDevice')[0].args, [{ currentPassword: 'SimulationCurrent8!', confirmRemoval: true }]);
        assert.equal(ui.callsFor('RemoveLocalDesktopCredential').length, 0);
      } else {
        assert.deepEqual(ui.callsFor('RemoveLocalDesktopCredential')[0].args, [{ confirmRemoval: true }]);
        assert.equal(ui.callsFor('ForgetThisDevice').length, 0);
        assert.match(ui.confirmations[0], /will not be revoked automatically/);
        assert.match(ui.toasts[0].message, /Review the device list/);
      }
      assert.equal(ui.run('trustedDesktopModel().status'), 'missing');
    }
  });

  test('simulation: cancelling corrupt local cleanup leaves both local and server credentials untouched', async () => {
    const ui = await adminSecuritySimulation({ confirm: false, native: { storage: 'corrupt', credentialStorage: { available: true, stored: false, state: 'corrupt' } } });
    await ui.run('forgetThisDesktop()');
    assert.equal(ui.confirmations.length, 1);
    assert.equal(ui.callsFor('RemoveLocalDesktopCredential').length, 0);
    assert.equal(ui.callsFor('ForgetThisDevice').length, 0);
    assert.equal(ui.apiCalls.filter(call => call[0] === 'POST').length, 0);
  });

  test('simulation: cancelling device deletion never invokes cleanup or the server mutation', async () => {
    for (const action of ['forgetThisDesktop()', 'revokeTrustedDevice("simulation-device", true)', 'revokeAllTrustedDevices()']) {
      const ui = await adminSecuritySimulation({ confirm: false });
      ui.password('SimulationCurrent8!');
      await ui.run(action);
      assert.equal(ui.callsFor('ForgetThisDevice').length, 0);
      assert.equal(ui.callsFor('RemoveLocalDesktopCredential').length, 0);
      assert.equal(ui.apiCalls.filter(call => call[0] === 'POST').length, 0);
      assert.equal(ui.element('desktopTrustCurrentPassword').value, '');
    }
  });

  test('simulation: revoking local or all devices explicitly authorizes local removal, remote revocation does not', async () => {
    for (const [action, cleanup, path] of [
      ['revokeTrustedDevice("simulation-device", true)', true, '/auth/trusted-devices/revoke'],
      ['revokeTrustedDevice("remote-device", false)', false, '/auth/trusted-devices/revoke'],
      ['revokeAllTrustedDevices()', true, '/auth/trusted-devices/revoke-all']
    ]) {
      const ui = await adminSecuritySimulation({ devices: [simulationTrustedDevice(), simulationTrustedDevice({ id: 'remote-device' })] });
      ui.password('SimulationCurrent8!');
      await ui.run(action);
      assert.equal(ui.apiCalls.find(call => call[0] === 'POST')[1], path);
      if (cleanup) {
        assert.deepEqual(ui.callsFor('RemoveLocalDesktopCredential')[0].args, [{ confirmRemoval: true }]);
        assert.match(ui.confirmations[0], /delete/);
      } else assert.equal(ui.callsFor('RemoveLocalDesktopCredential').length, 0);
    }
  });

  test('simulation: failed server revocation never deletes the local credential', async () => {
    const ui = await adminSecuritySimulation({ api: (method, path, body, helpers) => {
      if (method === 'POST') throw { error: { code: 'authentication_failed' } };
      const data = body || helpers;
      return { success: true, data: { devices: clone(data.devices) } };
    } });
    ui.password('wrong-password');
    await ui.run('revokeTrustedDevice("simulation-device", true)');
    assert.equal(ui.callsFor('RemoveLocalDesktopCredential').length, 0);
    assert.match(ui.element('desktopSignInError').textContent, /current password is incorrect/);
    assert.equal(ui.element('desktopTrustCurrentPassword').value, '');
  });

  test('simulation: cleanup failure remains visible without falsely reporting full success', async () => {
    const ui = await adminSecuritySimulation({ methods: { RemoveLocalDesktopCredential: () => { throw 'desktop_storage_unavailable'; } } });
    ui.password('SimulationCurrent8!');
    await ui.run('revokeTrustedDevice("simulation-device", true)');
    assert.match(ui.element('desktopSignInError').textContent, /could not be deleted/);
    assert.equal(ui.toasts.filter(toast => toast.tone === 'success').length, 0);
    assert.equal(ui.run('trustedDesktopModel().status'), 'revoked');
  });

  test('simulation: desktop lock and sign-out need no password and never automatically sign back in after reload', async () => {
    for (const [action, method, automatic] of [['logoutAdmin()', 'SignOutDesktop', false], ['lockAdmin()', 'LockDesktop', true]]) {
      const ui = await adminSecuritySimulation();
      ui.run('_nativeAdminCSRF = "simulation-active-csrf"; renderAdminPrincipal({ userID: "break-glass", username: "admin", method: "local", roles: ["break_glass"] })');
      await ui.run(action);
      assert.equal(ui.run('_nativeAdminCSRF'), '');
      assert.equal(ui.run('_adminPrincipal'), null);
      assert.deepEqual(ui.callsFor(method), [{ name: method, args: [] }]);
      assert.equal(ui.context.reloads, 1);
      assert.equal(ui.nativeState.credentialStorage.automaticSignIn, automatic);
      assert.equal(ui.apiCalls.filter(call => call[0] === 'POST').length, 0);
      assert.equal(ui.callsFor('SetDesktopAutoSignIn').length, 0);
      const reloaded = await adminSecuritySimulation({ native: ui.nativeState, devices: ui.devices });
      await reloaded.run('loadTrustedDevices()');
      assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
      assert.equal(reloaded.callsFor('AutomaticSignIn').length, 0);
    }
  });

  test('simulation: failed sign-out reports the bridge reason instead of reloading or swallowing it', async () => {
    const ui = await adminSecuritySimulation({ methods: { SignOutDesktop: () => { throw { reason: 'Simulation store is locked' }; } } });
    await ui.run('logoutAdmin()');
    assert.equal(ui.context.reloads, 0);
    assert.match(ui.element('desktopSignInError').textContent, /Simulation store is locked/);
    assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
  });

  test('simulation: browser mode hides native actions and uses only the ordinary logout API', async () => {
    const ui = await adminSecuritySimulation({ browser: true });
    assert.equal(ui.element('trustThisDeviceButton').hidden, true);
    assert.equal(ui.element('toggleDesktopAutoSignInButton').hidden, true);
    assert.equal(ui.element('forgetThisDeviceButton').hidden, true);
    assert.equal(ui.element('desktopCredentialStorageStatus').textContent, 'Browser only');
    await ui.run('lockAdmin()');
    assert.deepEqual(ui.apiCalls.at(-1), ['POST', '/logout', {}]);
    assert.equal(ui.calls.length, 0);
    assert.equal(ui.context.reloads, 1);
  });

  test('simulation: missing password focuses the labelled input and performs no mutation', async () => {
    const ui = await adminSecuritySimulation();
    await ui.run('toggleDesktopAutoSignIn()');
    assert.equal(ui.callsFor('SetDesktopAutoSignIn').length, 0);
    assert.equal(ui.document.activeElement.id, 'desktopTrustCurrentPassword');
    assert.equal(ui.element('desktopTrustCurrentPassword').getAttribute('aria-invalid'), 'true');
    ui.password('SimulationCurrent8!');
    await ui.element('desktopTrustCurrentPassword').emit('input');
    assert.equal(ui.element('desktopTrustCurrentPassword').getAttribute('aria-invalid'), null);
  });

  test('simulation: overlapping clicks execute one mutation and controls recover after a structured failure', async () => {
    let rejectCall;
    const ui = await adminSecuritySimulation({ methods: { SetDesktopAutoSignIn: () => new Promise((resolve, reject) => { rejectCall = reject; }) } });
    ui.password('SimulationCurrent8!');
    const first = ui.run('toggleDesktopAutoSignIn()');
    assert.equal(ui.element('toggleDesktopAutoSignInButton').disabled, true);
    await ui.run('toggleDesktopAutoSignIn()');
    assert.equal(ui.callsFor('SetDesktopAutoSignIn').length, 1);
    rejectCall({ error: { code: 'authentication_failed' } });
    await first;
    assert.equal(ui.element('toggleDesktopAutoSignInButton').disabled, false);
    assert.equal(ui.element('toggleDesktopAutoSignInButton').getAttribute('aria-pressed'), 'true');
    assert.match(ui.element('desktopSignInError').textContent, /current password is incorrect/);
    assert.equal(ui.element('desktopTrustCurrentPassword').value, '');
  });

  test('simulation: returned errors, JSON errors, and raw reasons are mapped, not stringified as objects', async () => {
    const cases = [
      ['{"code":"desktop_storage_corrupt"}', /corrupted/],
      [{ error: { code: 'authentication_failed' } }, /current password is incorrect/],
      [{ reason: 'Simulation native error' }, /Simulation native error/],
      ['Simulation raw reason', /Simulation raw reason/],
      [{ success: false, error: 'rate_limited' }, /Too many attempts/]
    ];
    for (const [reason, pattern] of cases) {
      const ui = await adminSecuritySimulation({ methods: { SetDesktopAutoSignIn: () => { if (reason.success === false) return reason; throw reason; } } });
      ui.password('SimulationCurrent8!');
      await ui.run('toggleDesktopAutoSignIn()');
      assert.match(ui.element('desktopSignInError').textContent, pattern);
      assert.doesNotMatch(ui.element('desktopSignInError').textContent, /\[object Object\]/);
    }
  });

  test('simulation: shipped dashboard sign-in passes the local username and explicit trust choice without automatic login', async () => {
    for (const trustDevice of [false, true]) {
      const ui = await adminSecuritySimulation();
      ui.element('loginUsername').value = 'simulation-local-user';
      ui.element('loginPassword').value = 'SimulationLogin8!';
      ui.element('desktopTrustLoginCheck').checked = trustDevice;
      await ui.run('submitLogin()');
      assert.deepEqual(ui.callsFor('SignIn')[0].args, [{ username: 'simulation-local-user', password: 'SimulationLogin8!', trustDevice }]);
      assert.equal(ui.element('loginPassword').value, '');
      assert.deepEqual(ui.navigations, ['/admin/#desktop-handoff=simulation-shell-handoff']);
      assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
    }
  });

  test('simulation: shipped dashboard sign-in maps native errors and retains a safe password retry', async () => {
    const ui = await adminSecuritySimulation({ methods: { SignIn: () => { throw { code: 'authentication_failed' }; } } });
    ui.element('loginPassword').value = 'SimulationWrong8!';
    await ui.run('submitLogin()');
    assert.equal(ui.element('loginError').textContent, 'Login failed');
    assert.equal(ui.element('loginSubmit').disabled, false);
    assert.equal(ui.navigations.length, 0);
  });

  test('simulation: changing credential storage after load disables stale trust controls on failure', async () => {
    const ui = await adminSecuritySimulation({ methods: { SetDesktopAutoSignIn: () => { throw { code: 'desktop_storage_corrupt' }; } } });
    ui.password('SimulationCurrent8!');
    await ui.run('toggleDesktopAutoSignIn()');
    assert.equal(ui.run('trustedDesktopModel().status'), 'corrupt');
    assert.equal(ui.element('trustThisDeviceButton').disabled, true);
    assert.equal(ui.element('toggleDesktopAutoSignInButton').hidden, true);
    assert.equal(ui.element('forgetThisDeviceButton').hidden, false);
  });

  test('simulation: native status failure keeps the server list but disables trust and toggles', async () => {
    const ui = await adminSecuritySimulation({ methods: { DesktopStatus: () => { throw 'Simulation native status unavailable'; } } });
    assert.equal(ui.element('trustedDevicesList').children.length, 1);
    assert.equal(ui.element('trustThisDeviceButton').disabled, true);
    assert.equal(ui.element('toggleDesktopAutoSignInButton').hidden, true);
    assert.match(ui.element('desktopSignInError').textContent, /native status unavailable/);
    assert.equal(ui.run('trustedDesktopModel().automatic'), false);
  });

  test('simulation: a failed list load cannot retain a stale trusted-state toggle, and a retry clears the load error', async () => {
    let fail = true;
    const ui = await adminSecuritySimulation({ api: () => { if (fail) throw 'Simulation list failed'; return { success: true, data: { devices: [simulationTrustedDevice()] } }; } });
    assert.equal(ui.run('trustedDesktopModel().trusted'), false);
    assert.equal(ui.element('trustThisDeviceButton').disabled, true);
    assert.match(ui.element('desktopSignInError').textContent, /Simulation list failed/);
    fail = false;
    await ui.run('loadTrustedDevices()');
    assert.equal(ui.run('trustedDesktopModel().trusted'), true);
    assert.equal(ui.element('desktopSignInError').textContent, '');
  });

  test('simulation: changing language rerenders row names, status, guidance and accessible action names', async () => {
    const ui = await adminSecuritySimulation();
    assert.equal(ui.element('trustedDevicesList').querySelector('button').getAttribute('aria-label'), 'Revoke · Simulation workstation');
    ui.run('LANG = "zh"; renderTrustedDevices()');
    assert.equal(ui.element('trustedDevicesList').querySelector('button').getAttribute('aria-label'), '撤销 · Simulation workstation');
    assert.equal(ui.element('desktopAutoSignInStatus').textContent, '开启');
    assert.match(ui.element('desktopLastUsed').textContent, /尚未使用/);
    ui.run('LANG = "en"; renderTrustedDevices()');
    assert.equal(ui.element('desktopAutoSignInStatus').textContent, 'On');
    assert.match(ui.element('desktopLastUsed').textContent, /Never used/);
  });
}

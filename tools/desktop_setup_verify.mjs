import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import vm from 'node:vm';
import { simulationPendingTranslations, simulationTrustedDevice } from './admin_security_devices.test.mjs';

const require = createRequire(import.meta.url);
const candidates = process.env.PLAYWRIGHT_MODULE ? [process.env.PLAYWRIGHT_MODULE] : ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core'];
let chromium;
for (const name of candidates) {
  try { ({ chromium } = require(name)); break; }
  catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
}
if (!chromium) throw new Error('Set PLAYWRIGHT_MODULE to an existing Playwright installation.');

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8');
const dictionarySource = read('internal/admin/ui/js/01_i18n.js');
const dictionary = vm.runInNewContext(dictionarySource + '\nI18N');
const pendingTranslationKeys = Object.keys(simulationPendingTranslations).filter(key => !dictionary[key]);
const artifactDir = process.env.ARTIFACT_DIR ? resolve(process.env.ARTIFACT_DIR) : mkdtempSync(join(tmpdir(), 'routenest-desktop-ui-simulation-'));
mkdirSync(artifactDir, { recursive: true });
const widths = (process.env.DESKTOP_VERIFY_WIDTHS || '1440,1280,1024,768,390,360').split(',').map(Number);
const languages = (process.env.DESKTOP_VERIFY_LANGUAGES || 'en-US,zh-CN').split(',');
const schemes = (process.env.DESKTOP_VERIFY_SCHEMES || 'light,dark').split(',');
if (widths.some(width => !Number.isInteger(width) || width < 320 || width > 2560)) throw new Error('DESKTOP_VERIFY_WIDTHS must contain widths between 320 and 2560.');
const result = {
  simulation: true,
  scope: 'Shipped HTML, CSS and JavaScript with a simulated native bridge and API. This does not validate Wails binding delivery, OS credential storage, Go authentication, or real sessions.',
  pendingTranslationKeys,
  translationSimulation: pendingTranslationKeys.length > 0,
  artifactDir,
  cases: 0, assertions: 0, screenshots: 0,
  errorContrast: [],
  diagnostics: { pageErrors: [], consoleErrors: [] }, failures: []
};
const css = ['00_base.css', '30_components.css', '50_components_a11y.css', '99_access_settings.css'].map(name => read('internal/admin/ui/css/' + name)).join('\n');
const shellSource = read('internal/admin/ui/js/03_shell.js');
const authShellSource = shellSource.slice(0, shellSource.indexOf('async function openExternal('));
const fixtureHTML = '<!doctype html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Simulated desktop security UI</title><link rel="stylesheet" href="/simulation/admin.css"></head><body><main class="main"><div class="content-shell"><p id="simulationLabel" class="access-footnote">UI simulation · No real credentials or sessions</p>' + read('internal/admin/ui/html/pages/security.html') + '</div></main>' + read('internal/admin/ui/html/pages/login.html') + '<script src="/simulation/dictionary.js"></script><script src="/simulation/core.js"></script><script src="/simulation/auth-shell.js"></script><script src="/simulation/security.js"></script><script src="/simulation/recovery.js"></script><script src="/simulation/fixture.js"></script></body></html>';
const fixtureJS = `
window.formatDateTime = value => new Date(value).toLocaleString(LANG === 'en' ? 'en-US' : 'zh-CN');
window.showConfirm = async message => { window.__desktopSimulation.confirmations.push(message); window.__persistSimulation(); return true; };
window.toast = (message, tone) => { window.__desktopSimulation.toasts.push({ message, tone }); window.__persistSimulation(); };
document.documentElement.dataset.theme = window.__desktopSimulation.colorScheme;
document.getElementById('tab-security').style.display = 'block';
applyLang();
window.__fixtureReady = (async () => { if (await initializeAdminAuth()) await loadTrustedDevices(); })();
`;
const assets = new Map([
  ['/', ['text/html', read('desktop/setup/index.html')]],
  ['/index.html', ['text/html', read('desktop/setup/index.html')]],
  ['/setup.js', ['text/javascript', read('desktop/setup/setup.js')]],
  ['/setup.css', ['text/css', read('desktop/setup/setup.css')]],
  ['/admin/', ['text/html', fixtureHTML]],
  ['/simulation/admin.css', ['text/css', css]],
  ['/simulation/dictionary.js', ['text/javascript', dictionarySource + '\nfor (const [key, value] of Object.entries(' + JSON.stringify(simulationPendingTranslations) + ')) if (!I18N[key]) I18N[key] = value;']],
  ['/simulation/core.js', ['text/javascript', read('internal/admin/ui/js/01_core.js')]],
  ['/simulation/auth-shell.js', ['text/javascript', authShellSource]],
  ['/simulation/security.js', ['text/javascript', read('internal/admin/ui/js/06_security_devices.js')]],
  ['/simulation/recovery.js', ['text/javascript', read('internal/admin/ui/js/03_recovery.js')]],
  ['/simulation/fixture.js', ['text/javascript', fixtureJS]],
  ['/favicon.ico', ['image/x-icon', '']]
]);
const base = new URL(process.env.DESKTOP_SETUP_URL || 'http://127.0.0.1:8765/');
if (!['http:', 'https:'].includes(base.protocol) || !['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname) || !['/', '/index.html'].includes(base.pathname) || base.search || base.hash || base.username || base.password) {
  throw new Error('DESKTOP_SETUP_URL must be a loopback setup URL without credentials, query, or fragment.');
}

function check(actual, expected, label) {
  result.assertions++;
  assert.deepEqual(actual, expected, label);
}

function installSimulatedBridge(config) {
  const key = 'routenest_desktop_ui_simulation';
  const previous = sessionStorage.getItem(key);
  const data = previous ? JSON.parse(previous) : {
    ...config, calls: [], confirmations: [], toasts: [], setupComplete: false, automaticUsed: false, signedIn: config.mode === 'admin',
    native: config.native || { deviceID: '', deviceStatus: 'missing', credentialStorage: { available: true, stored: false, state: 'missing', automaticSignIn: false } },
    devices: config.devices || []
  };
  window.__desktopSimulation = data;
  const persist = () => sessionStorage.setItem(key, JSON.stringify(data));
  window.__persistSimulation = persist;
  const record = (name, args) => {
    data.calls.push({ name, args: JSON.parse(JSON.stringify(args)), saved: !!document.getElementById('savedCheck')?.checked, recoveryCodeCleared: document.getElementById('recoveryCode')?.textContent === '' });
    persist();
  };
  const zero = (name, args) => { record(name, args); if (args.length !== 0) throw new Error('Simulation arity mismatch: ' + name); };
  const object = (name, args, keys) => {
    record(name, args);
    if (args.length !== 1 || !args[0] || Object.keys(args[0]).sort().join(',') !== keys.sort().join(',')) throw new Error('Simulation request mismatch: ' + name);
    return args[0];
  };
  const nativeStatus = () => ({ gatewayURL: 'http://127.0.0.1:3467/v1', ...JSON.parse(JSON.stringify(data.native)) });
  const simulationCSRF = 'simulation-session-csrf';
  const trust = () => {
    data.native = { deviceID: 'simulation-current', deviceStatus: 'trusted', credentialStorage: { available: true, stored: true, state: 'stored', automaticSignIn: true } };
    if (!data.devices.some(device => device.id === 'simulation-current')) data.devices.push({ id: 'simulation-current', metadata: { name: 'Simulation workstation', platform: 'Windows' }, createdAt: '2026-01-01T10:00:00Z', expiresAt: '2099-04-01T10:00:00Z', lastUsedAt: '0001-01-01T00:00:00Z', automaticSignIn: true });
    persist();
  };
  const remove = () => {
    data.native = { deviceID: '', deviceStatus: 'missing', credentialStorage: { available: true, stored: false, state: 'missing', automaticSignIn: false } };
    persist();
  };
  window.runtime = { Quit: (...args) => { zero('Quit', args); data.quit = true; persist(); } };
  window.go = { main: { DesktopSetupBridge: {
    AdminRequest: async (...args) => {
      record('AdminRequest', args);
      const request = args[0];
      if (args.length !== 1 || !request || typeof request !== 'object' || Array.isArray(request)) throw new Error('Simulation AdminRequest requires exactly one object');
      if (!['GET', 'HEAD', 'OPTIONS', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method)) throw new Error('Simulation AdminRequest requires a supported uppercase method');
      if (typeof request.path !== 'string' || !request.path.startsWith('/admin/api/') || request.path.includes('\\') || request.path.includes('#')) throw new Error('Simulation AdminRequest requires an administrator API path');
      if (Object.keys(request).some(key => !['method', 'path', 'body', 'csrf'].includes(key) || typeof request[key] !== 'string')) throw new Error('Simulation AdminRequest fields must be supported strings');
      let body;
      if (Object.hasOwn(request, 'body')) {
        try { body = JSON.parse(request.body); }
        catch { throw new Error('Simulation AdminRequest body must be serialized JSON'); }
      }
      const path = request.path.slice('/admin/api'.length);
      if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method) && path !== '/auth/desktop-handoff' && request.csrf !== simulationCSRF) return { status: 403, body: JSON.stringify({ success: false, error: 'csrf_failed' }), csrf: data.signedIn ? simulationCSRF : '', contentType: 'application/json' };
      const apiArgs = [request.method, path];
      if (body !== undefined) apiArgs.push(body);
      const envelope = await window.__simulateAdminAPI(...apiArgs);
      const status = envelope.success === false ? ({ authentication_failed: 401, desktop_handoff_failed: 401, rate_limited: 429, security_unavailable: 503 })[envelope.error] || 400 : 200;
      return { status, body: JSON.stringify(envelope), csrf: data.signedIn ? simulationCSRF : '', contentType: 'application/json' };
    },
    State: async (...args) => { zero('State', args); return { mode: data.mode, adminURL: '/admin/', credentialStorage: data.native.credentialStorage, deviceStatus: data.native.deviceStatus }; },
    AutomaticSignIn: async (...args) => {
      zero('AutomaticSignIn', args);
      if (data.automaticUsed) throw new Error('Simulation automatic sign-in replay');
      data.automaticUsed = true;
      persist();
      if (data.mode === 'setup') throw 'automatic_sign_in_unavailable';
      return config.automaticHandoff ? { handoffToken: 'simulation-automatic-handoff', adminURL: '/admin/' } : { status: data.native.deviceStatus };
    },
    Complete: async (...args) => {
      record('Complete', args);
      if (args.length !== 3 || args[0] !== args[1] || typeof args[2] !== 'boolean' || data.setupComplete) throw new Error('Simulation Complete contract violation');
      data.setupComplete = true;
      if (args[2]) trust();
      persist();
      return { recoveryCode: 'SIMULATION-SETUP-CODE-8D7K-4M9Q', trustStatus: args[2] ? 'trusted' : 'not_requested', restartRequired: config.restartRequired === true, adminURL: '/admin/' };
    },
    FinishSetup: async (...args) => {
      zero('FinishSetup', args);
      if (!data.setupComplete || !document.getElementById('savedCheck').checked || document.getElementById('recoveryCode').textContent) throw new Error('Simulation acknowledgement missing');
      data.signedIn = true;
      persist();
      return { handoffToken: 'simulation-finish-handoff', adminURL: '/admin/' };
    },
    SignIn: async (...args) => {
      const request = object('SignIn', args, ['username', 'password', 'trustDevice']);
      if (data.signInError) { const reason = data.signInError; data.signInError = null; persist(); throw reason; }
      if (request.trustDevice) trust();
      data.signedIn = true;
      persist();
      return { handoffToken: 'simulation-password-handoff', adminURL: '/admin/', trustStatus: request.trustDevice ? 'trusted' : 'not_requested' };
    },
    RecoverPassword: async (...args) => {
      object('RecoverPassword', args, ['recoveryCode', 'newPassword', 'confirmation']);
      data.signedIn = false;
      data.native.credentialStorage.automaticSignIn = false;
      data.native.deviceStatus = 'revoked';
      data.devices = [];
      persist();
      return { recoveryCode: 'SIMULATION-REPLACEMENT-CODE-7H2R-6W5X' };
    },
    DesktopStatus: async (...args) => { zero('DesktopStatus', args); return nativeStatus(); },
    TrustThisDevice: async (...args) => { object('TrustThisDevice', args, ['currentPassword']); trust(); return nativeStatus(); },
    SetDesktopAutoSignIn: async (...args) => {
      const request = object('SetDesktopAutoSignIn', args, ['currentPassword', 'enabled']);
      if (data.toggleError) { const reason = data.toggleError; data.toggleError = null; persist(); throw reason; }
      data.native.credentialStorage.automaticSignIn = request.enabled;
      data.native.deviceStatus = request.enabled ? 'trusted' : 'paused';
      data.devices.find(device => device.id === data.native.deviceID).automaticSignIn = request.enabled;
      persist();
      return nativeStatus();
    },
    ForgetThisDevice: async (...args) => {
      const request = object('ForgetThisDevice', args, ['currentPassword', 'confirmRemoval']);
      if (request.confirmRemoval !== true) throw new Error('Simulation cleanup consent missing');
      data.devices = data.devices.filter(device => device.id !== data.native.deviceID);
      remove();
      return nativeStatus();
    },
    RemoveLocalDesktopCredential: async (...args) => {
      const request = object('RemoveLocalDesktopCredential', args, ['confirmRemoval']);
      if (request.confirmRemoval !== true) throw new Error('Simulation cleanup consent missing');
      remove();
      return nativeStatus();
    },
    SignOutDesktop: async (...args) => {
      zero('SignOutDesktop', args);
      data.native.credentialStorage.automaticSignIn = false;
      data.native.deviceStatus = 'paused';
      const current = data.devices.find(device => device.id === data.native.deviceID);
      if (current) current.automaticSignIn = false;
      data.signedIn = false;
      persist();
      return { signedOut: true, automaticSignIn: false };
    },
    LockDesktop: async (...args) => { zero('LockDesktop', args); data.signedIn = false; persist(); return { locked: true }; }
  } } };
  window.__simulateAdminAPI = async (...args) => {
    record('API', args);
    if (args[0] === 'GET' && args[1] === '/auth/status') return { success: true, data: { initialized: true, bootstrapRequired: false, recoveryRequired: false, legacyRotationRequired: false, recoveryCodeAvailable: true, ssoProviders: [] } };
    if (args[0] === 'GET' && args[1] === '/auth/session') return data.signedIn
      ? { success: true, data: { principal: { userID: 'break-glass', username: 'admin', method: 'local', roles: ['break_glass'] } } }
      : { success: false, error: 'authentication_failed' };
    if (args[1] === '/auth/desktop-handoff' && args[0] === 'POST') {
      if (!args[2]?.token || data.handoffConsumed) return { success: false, error: 'desktop_handoff_failed' };
      data.handoffConsumed = true;
      data.signedIn = true;
      persist();
      return { success: true, data: {} };
    }
    if (args[0] === 'GET') return { success: true, data: { devices: JSON.parse(JSON.stringify(data.devices)) } };
    if (args[1] === '/auth/trusted-devices/revoke') data.devices = data.devices.filter(device => device.id !== args[2].deviceID);
    if (args[1] === '/auth/trusted-devices/revoke-all') data.devices = [];
    persist();
    return { success: true, data: {} };
  };
  localStorage.setItem('cline_admin_lang', config.language.startsWith('zh') ? 'zh' : 'en');
  persist();
}

async function openSimulation(browser, config, path = '/') {
  const context = await browser.newContext({ viewport: { width: config.width, height: 1000 }, locale: config.language, colorScheme: config.colorScheme, reducedMotion: 'reduce' });
  await context.addInitScript(installSimulatedBridge, config);
  await context.route(base.origin + '/**', route => {
    const pathname = new URL(route.request().url()).pathname;
    const fixture = (!process.env.DESKTOP_SETUP_URL || pathname === '/admin/' || pathname.startsWith('/simulation/')) && assets.get(pathname);
    return fixture ? route.fulfill({ status: 200, contentType: fixture[0], body: fixture[1] }) : route.continue();
  });
  const page = await context.newPage();
  page.setDefaultTimeout(7000);
  page.on('pageerror', error => result.diagnostics.pageErrors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') result.diagnostics.consoleErrors.push(message.text()); });
  await page.goto(new URL(path, base).href, { waitUntil: 'networkidle' });
  return { context, page };
}

async function calls(page, name) {
  return page.evaluate(name => window.__desktopSimulation.calls.filter(call => call.name === name), name);
}

async function screenshot(page, label, selector) {
  const target = selector ? page.locator(selector) : page;
  await target.screenshot({ path: resolve(artifactDir, label + '.png'), animations: 'disabled', ...(selector ? {} : { fullPage: true }) });
  result.screenshots++;
}

async function layout(page, selector, label) {
  const measured = await page.locator(selector).evaluate(card => {
    const visible = element => element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden';
    const controls = [...card.querySelectorAll('input,button')].filter(visible);
    const box = card.getBoundingClientRect();
    return {
      overflow: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > innerWidth + 1,
      contained: box.left >= 0 && box.right <= innerWidth + 1 && controls.every(control => { const rect = control.getBoundingClientRect(); return rect.left >= box.left && rect.right <= box.right + 1; }),
      labelled: controls.filter(control => control.tagName === 'INPUT').every(input => input.labels?.length || input.getAttribute('aria-label')),
      named: controls.filter(control => control.tagName === 'BUTTON').every(button => button.textContent.trim() || button.getAttribute('aria-label')),
      touch: controls.every(control => control.type === 'checkbox' || control.getBoundingClientRect().height >= (innerWidth <= 600 ? 43 : 35)),
      statusContained: [...card.querySelectorAll('.desktop-signin-status > div')].every(status => status.scrollWidth <= status.clientWidth + 1)
    };
  });
  check(measured.overflow, false, label + ': no page overflow');
  check(measured.contained, true, label + ': controls fit the card');
  check(measured.labelled, true, label + ': inputs have labels');
  check(measured.named, true, label + ': buttons have accessible names');
  check(measured.touch, true, label + ': controls have comfortable target sizes');
  check(measured.statusContained, true, label + ': device status does not overflow');
}

async function errorContrast(page, selector, label) {
  const ratio = await page.locator(selector).evaluate(element => {
    const rgba = value => { const parts = value.match(/[\d.]+/g).map(Number); return [parts[0], parts[1], parts[2], parts[3] ?? 1]; };
    const mix = (top, bottom) => [0, 1, 2].map(index => top[index] * top[3] + bottom[index] * (1 - top[3])).concat(1);
    const ancestors = [];
    for (let current = element; current; current = current.parentElement) ancestors.push(current);
    let background = [255, 255, 255, 1];
    for (const ancestor of ancestors.reverse()) background = mix(rgba(getComputedStyle(ancestor).backgroundColor), background);
    const foreground = mix(rgba(getComputedStyle(element).color), background);
    const luminance = color => color.slice(0, 3).map(value => { const n = value / 255; return n <= .04045 ? n / 12.92 : ((n + .055) / 1.055) ** 2.4; }).reduce((sum, value, index) => sum + value * [.2126, .7152, .0722][index], 0);
    const light = Math.max(luminance(foreground), luminance(background));
    const dark = Math.min(luminance(foreground), luminance(background));
    return (light + .05) / (dark + .05);
  });
  result.errorContrast.push({ label, ratio: Number(ratio.toFixed(2)) });
  check(ratio >= 4.5, true, label + ': error contrast is at least 4.5:1 (' + ratio.toFixed(2) + ')');
}

async function setupCase(browser, config, label) {
  const { context, page } = await openSimulation(browser, { ...config, mode: 'setup' });
  try {
    await page.locator('#welcomeView').waitFor({ state: 'visible' });
    check((await calls(page, 'State'))[0].args, [], label + ': State has no arguments');
    check((await calls(page, 'AutomaticSignIn')).length, 1, label + ': one initial automatic attempt');
    await layout(page, '#welcomeView', label);
    await screenshot(page, label + '-welcome');
    await page.locator('#beginButton').click();
    check(await page.evaluate(() => document.activeElement.id), 'passwordInput', label + ': password focus');
    check(await page.locator('#trustDeviceCheck').isChecked(), false, label + ': trust is opt-in');
    await page.locator('#passwordInput').fill('SimulationSetup8!');
    await page.locator('#confirmInput').fill('SimulationSetup8!');
    await layout(page, '#passwordView', label);
    await screenshot(page, label + '-password');
    await page.locator('#confirmInput').press('Enter');
    await page.locator('#recoveryView').waitFor({ state: 'visible' });
    check((await calls(page, 'Complete'))[0].args, ['SimulationSetup8!', 'SimulationSetup8!', false], label + ': Complete actual arguments');
    check(await page.locator('#recoveryCode').textContent(), 'SIMULATION-SETUP-CODE-8D7K-4M9Q', label + ': recovery code returned by simulation');
    check(await page.locator('#finishButton').isDisabled(), true, label + ': save acknowledgement gates finish');
    check((await calls(page, 'FinishSetup')).length, 0, label + ': no early handoff');
    await layout(page, '#recoveryView', label);
    await screenshot(page, label + '-recovery');
    await page.locator('#savedCheck').focus();
    await page.keyboard.press('Space');
    await page.keyboard.press('Tab');
    check(await page.evaluate(() => document.activeElement.id), 'finishButton', label + ': keyboard acknowledgement reaches finish');
    await page.keyboard.press('Enter');
    await page.waitForURL(url => url.pathname === '/admin/' && url.hash === '#dashboard');
    await page.waitForFunction(() => typeof _adminPrincipal !== 'undefined' && _adminPrincipal);
    check((await calls(page, 'API')).filter(call => call.args[1] === '/auth/desktop-handoff').map(call => call.args), [['POST', '/auth/desktop-handoff', { token: 'simulation-finish-handoff' }]], label + ': shipped consumer redeems the handoff once');
    check((await calls(page, 'AdminRequest')).find(call => call.args[0].path === '/admin/api/auth/desktop-handoff').args, [{ method: 'POST', path: '/admin/api/auth/desktop-handoff', body: '{"token":"simulation-finish-handoff"}', csrf: '' }], label + ': handoff uses one native request object');
    check(await page.evaluate(() => _nativeAdminCSRF), 'simulation-session-csrf', label + ': handoff initializes ephemeral CSRF');
    const finish = await calls(page, 'FinishSetup');
    check(finish.length, 1, label + ': single FinishSetup invocation');
    check(finish[0].args, [], label + ': FinishSetup has no arguments');
    check([finish[0].saved, finish[0].recoveryCodeCleared], [true, true], label + ': acknowledgement and code clearance precede handoff');
  } finally { await context.close(); }
}

async function loginCase(browser, config, label) {
  const { context, page } = await openSimulation(browser, { ...config, mode: 'login' });
  try {
    await page.locator('#loginView').waitFor({ state: 'visible' });
    check(await page.locator('#desktopTrustDeviceCheck').isChecked(), false, label + ': login trust is opt-in');
    await layout(page, '#loginView', label);
    await screenshot(page, label + '-login');
    await page.locator('#desktopForgotButton').click();
    check(new URL(page.url()).pathname, '/', label + ': forgot password stays on the current route');
    check(await page.evaluate(() => document.activeElement.id), 'recoveryCodeInput', label + ': recovery form focuses the code');
    await layout(page, '#forgotView', label);
    await screenshot(page, label + '-forgot-password');
    await page.locator('#recoveryCodeInput').fill('SIMULATION-OLD-CODE');
    await page.locator('#recoveryPasswordInput').fill('SimulationReplacement8!');
    await page.locator('#recoveryConfirmInput').fill('SimulationReplacement8!');
    await page.locator('#recoveryConfirmInput').press('Enter');
    await page.locator('#recoveryView').waitFor({ state: 'visible' });
    check((await calls(page, 'RecoverPassword'))[0].args, [{ recoveryCode: 'SIMULATION-OLD-CODE', newPassword: 'SimulationReplacement8!', confirmation: 'SimulationReplacement8!' }], label + ': recovery actual object argument');
    check(await page.locator('#recoveryCode').textContent(), 'SIMULATION-REPLACEMENT-CODE-7H2R-6W5X', label + ': replacement code shown');
    check(await page.locator('#finishButton').isDisabled(), true, label + ': replacement must be saved');
    await layout(page, '#recoveryView', label);
    await screenshot(page, label + '-replacement');
    await page.locator('#savedCheck').check();
    await page.locator('#finishButton').click();
    await page.locator('#loginView').waitFor({ state: 'visible' });
    check(await page.locator('#recoveryCode').textContent(), '', label + ': replacement code cleared');
    check((await calls(page, 'FinishSetup')).length, 0, label + ': recovery never invokes setup finish');
    check((await calls(page, 'AutomaticSignIn')).length, 1, label + ': recovery never automatically signs in');
    await page.evaluate(() => { window.__desktopSimulation.signInError = { error: { code: 'authentication_failed' } }; window.__persistSimulation(); });
    await page.locator('#desktopUsernameInput').fill('simulation-operator');
    await page.locator('#desktopPasswordInput').fill('SimulationReplacement8!');
    await page.locator('#desktopPasswordInput').press('Enter');
    await page.locator('#desktopLoginError').waitFor({ state: 'visible' });
    check(await page.locator('#desktopLoginError').textContent(), config.language.startsWith('zh') ? '登录失败，请检查用户名和密码。' : 'Sign-in failed. Check your username and password.', label + ': structured rejection translated');
    await errorContrast(page, '#desktopLoginError', label);
    await screenshot(page, label + '-login-error');
    await page.locator('#desktopPasswordInput').fill('SimulationReplacement8!');
    await page.locator('#desktopTrustDeviceCheck').check();
    await page.locator('#desktopSignInButton').click();
    await page.waitForURL(url => url.pathname === '/admin/' && url.hash === '#dashboard');
    await page.waitForFunction(() => typeof _adminPrincipal !== 'undefined' && _adminPrincipal);
    check(await page.evaluate(() => _nativeAdminCSRF), 'simulation-session-csrf', label + ': native sign-in captures session CSRF');
    const signIn = (await calls(page, 'SignIn')).at(-1);
    check(signIn.args, [{ username: 'simulation-operator', password: 'SimulationReplacement8!', trustDevice: true }], label + ': supplied username and consent preserved');
    await page.waitForFunction(() => typeof logoutAdmin === 'function');
    const navigation = page.waitForEvent('load');
    await page.evaluate(() => logoutAdmin());
    await navigation;
    await page.waitForFunction(() => document.documentElement.dataset.adminAuth === 'signed-out' && typeof _adminAuthStatus !== 'undefined' && _adminAuthStatus !== null);
    await page.locator('#loginForm').waitFor({ state: 'visible' });
    check(await page.locator('.layout').isVisible(), false, label + ': logged-out desktop console hides dashboard');
    check(await page.evaluate(() => _nativeAdminCSRF), '', label + ': logout reload leaves no session CSRF');
    check(await page.evaluate(() => _adminPrincipal), null, label + ': logout reload leaves no principal');
    check((await calls(page, 'SignOutDesktop')).at(-1).args, [], label + ': sign out is passwordless and argumentless');
    check((await calls(page, 'AutomaticSignIn')).length, 1, label + ': dashboard reload after logout never retries automatic sign-in');
    check((await calls(page, 'API')).filter(call => call.args[1] === '/auth/desktop-handoff').length, 1, label + ': logout reload never replays a handoff');
    check(await page.evaluate(() => window.__desktopSimulation.native.credentialStorage.automaticSignIn), false, label + ': simulated sign out paused automatic sign-in');
  } finally { await context.close(); }
}

async function adminCase(browser, config, label) {
  const devices = [
    simulationTrustedDevice({ id: 'simulation-current', metadata: { name: 'Simulation workstation · current device', platform: 'Windows' } }),
    simulationTrustedDevice({ id: 'simulation-old', metadata: { name: 'Simulation laptop · expired', platform: 'macOS' }, expiresAt: '2020-01-01T10:00:00Z' })
  ];
  const { context, page } = await openSimulation(browser, { ...config, mode: 'admin', devices, native: { deviceID: 'simulation-current', deviceStatus: 'paused', credentialStorage: { available: true, stored: true, state: 'stored', automaticSignIn: false } } }, '/admin/');
  try {
    await page.locator('#toggleDesktopAutoSignInButton').waitFor({ state: 'visible' });
    check(await page.locator('#trustThisDeviceButton').isVisible(), false, label + ': no duplicate trust control');
    check(await page.locator('#toggleDesktopAutoSignInButton').getAttribute('aria-pressed'), 'false', label + ': local pause overrides server enabled');
    check((await calls(page, 'DesktopStatus'))[0].args, [], label + ': DesktopStatus has no arguments');
    check((await calls(page, 'AdminRequest')).some(call => call.args.length === 1 && call.args[0].method === 'GET' && call.args[0].path === '/admin/api/auth/trusted-devices'), true, label + ': shipped API list uses AdminRequest');
    check((await calls(page, 'AutomaticSignIn')).length, 0, label + ': no automatic sign-in on dashboard');
    check((await page.locator('#trustedDevicesList').textContent()).includes('0001'), false, label + ': zero dates are not displayed');
    await screenshot(page, label + '-security', '#desktopSignInCard');
    await layout(page, '#desktopSignInCard', label);
    await page.locator('#desktopTrustCurrentPassword').fill('SimulationCurrent8!');
    await page.locator('#toggleDesktopAutoSignInButton').focus();
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => document.getElementById('toggleDesktopAutoSignInButton').getAttribute('aria-pressed') === 'true');
    check((await calls(page, 'SetDesktopAutoSignIn')).at(-1).args, [{ currentPassword: 'SimulationCurrent8!', enabled: true }], label + ': displayed enable target matches payload');
    await page.evaluate(() => { window.__desktopSimulation.toggleError = 'authentication_failed'; window.__persistSimulation(); });
    await page.locator('#desktopTrustCurrentPassword').fill('simulation-wrong-password');
    await page.locator('#toggleDesktopAutoSignInButton').click();
    await page.locator('#desktopSignInError').waitFor({ state: 'visible' });
    await errorContrast(page, '#desktopSignInError', label);
    await screenshot(page, label + '-security-error', '#desktopSignInCard');
    check(await page.locator('#toggleDesktopAutoSignInButton').getAttribute('aria-pressed'), 'true', label + ': failed disable is not shown as successful');
    await page.locator('#desktopTrustCurrentPassword').fill('SimulationCurrent8!');
    await page.locator('#forgetThisDeviceButton').click();
    await page.waitForFunction(() => window.__desktopSimulation.calls.some(call => call.name === 'ForgetThisDevice'));
    check((await calls(page, 'ForgetThisDevice')).at(-1).args, [{ currentPassword: 'SimulationCurrent8!', confirmRemoval: true }], label + ': forget includes explicit removal authorization');
    check((await calls(page, 'RemoveLocalDesktopCredential')).length, 0, label + ': no implicit removal call');
  } finally { await context.close(); }
}

async function runCase(label, action) {
  try { await action(); result.cases++; }
  catch (error) { result.failures.push({ label, message: String(error.message || error).split('\n').slice(0, 4).join(' ') }); }
}

let browser;
try {
  browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined), args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  for (const language of languages) {
    for (const colorScheme of schemes) {
      for (const width of widths) {
        const config = { width, language, colorScheme };
        const suffix = `${language}-${colorScheme}-${width}`;
        await runCase('setup-' + suffix, () => setupCase(browser, config, 'simulation-setup-' + suffix));
        await runCase('login-' + suffix, () => loginCase(browser, config, 'simulation-login-' + suffix));
        await runCase('admin-' + suffix, () => adminCase(browser, config, 'simulation-admin-' + suffix));
      }
    }
  }
  for (const status of ['unavailable', 'corrupt', 'revoked', 'expired', 'paused']) {
    await runCase('state-' + status, async () => {
      const native = { deviceID: status === 'paused' ? 'simulation-current' : '', deviceStatus: status, credentialStorage: { available: status !== 'unavailable', stored: status === 'paused', state: ['corrupt', 'unavailable'].includes(status) ? status : 'stored', automaticSignIn: false } };
      const { context, page } = await openSimulation(browser, { mode: 'login', width: 390, language: 'en-US', colorScheme: 'dark', native });
      try {
        await page.locator('#loginView').waitFor({ state: 'visible' });
        if (['corrupt', 'unavailable'].includes(status)) check(await page.locator('#desktopTrustDeviceCheck').isDisabled(), true, status + ': trust disabled');
        if (status === 'paused') check(await page.locator('#desktopTrustOption').isVisible(), false, status + ': existing trust not duplicated');
        await layout(page, '#loginView', status);
        await screenshot(page, 'simulation-state-' + status);
      } finally { await context.close(); }
    });
  }
  for (const storage of ['corrupt', 'unavailable']) {
    await runCase('local-cleanup-' + storage, async () => {
      const { context, page } = await openSimulation(browser, { mode: 'admin', width: 390, language: 'en-US', colorScheme: 'dark', native: { deviceID: '', credentialStorage: { available: storage !== 'unavailable', stored: false, state: storage, automaticSignIn: false } }, devices: [simulationTrustedDevice({ id: 'simulation-retained-server-record' })] }, '/admin/');
      try {
        await page.locator('#forgetThisDeviceButton').waitFor({ state: 'visible' });
        await screenshot(page, 'simulation-local-cleanup-' + storage, '#desktopSignInCard');
        await page.locator('#forgetThisDeviceButton').click();
        await page.waitForFunction(() => window.__desktopSimulation.calls.some(call => call.name === 'RemoveLocalDesktopCredential'));
        check((await calls(page, 'RemoveLocalDesktopCredential')).at(-1).args, [{ confirmRemoval: true }], storage + ': cleanup is explicitly authorized');
        check((await calls(page, 'ForgetThisDevice')).length, 0, storage + ': cleanup never attempts to read an unusable device credential');
        check(await page.evaluate(() => window.__desktopSimulation.devices.length), 1, storage + ': local removal does not falsely revoke server records');
        check(await page.evaluate(() => window.__desktopSimulation.confirmations[0].includes('will not be revoked automatically')), true, storage + ': confirmation explains local-only removal');
      } finally { await context.close(); }
    });
  }
  await runCase('restart', async () => {
    const { context, page } = await openSimulation(browser, { mode: 'setup', width: 390, language: 'zh-CN', colorScheme: 'dark', restartRequired: true });
    try {
      await page.locator('#beginButton').click();
      await page.locator('#passwordInput').fill('SimulationRestart8!');
      await page.locator('#confirmInput').fill('SimulationRestart8!');
      await page.locator('#createButton').click();
      await page.locator('#recoveryView').waitFor({ state: 'visible' });
      check(await page.locator('#restartNotice').isVisible(), true, 'restart: notice shown before acknowledgement');
      check((await calls(page, 'Quit')).length, 0, 'restart: not closed before save');
      await screenshot(page, 'simulation-restart-recovery-zh-dark-390');
      await page.locator('#savedCheck').check();
      await page.locator('#finishButton').click();
      check((await calls(page, 'Quit')).length, 1, 'restart: closed after acknowledgement');
      check(await page.locator('#recoveryCode').textContent(), '', 'restart: recovery code cleared');
      check((await calls(page, 'FinishSetup')).length, 0, 'restart: no unusable handoff requested');
    } finally { await context.close(); }
  });
} catch (error) {
  result.failures.push({ label: 'browser execution', message: error.message });
} finally {
  if (browser) await browser.close();
}
if (result.diagnostics.pageErrors.length || result.diagnostics.consoleErrors.length) result.failures.push({ label: 'browser diagnostics', message: 'Browser errors were reported; inspect diagnostics.' });
console.log(JSON.stringify(result, null, 2));
if (result.failures.length) process.exitCode = 1;

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import vm from 'node:vm';

const setupSource = readFileSync(new URL('../desktop/setup/setup.js', import.meta.url), 'utf8');
const setupHTML = readFileSync(new URL('../desktop/setup/index.html', import.meta.url), 'utf8');
const clone = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));

export class SimulationElement {
  constructor(tag, document, attributes = {}) {
    this.tagName = tag.toUpperCase();
    this.ownerDocument = document;
    this.attributes = {};
    this.dataset = {};
    this.children = [];
    this.listeners = new Map();
    this.hidden = false;
    this.disabled = false;
    this.checked = false;
    this.value = '';
    this.type = '';
    this.id = '';
    this._text = '';
    this.className = '';
    this.style = {};
    this.classList = {
      toggle: (name, enabled) => {
        const classes = new Set(this.className.split(/\s+/).filter(Boolean));
        if (enabled) classes.add(name); else classes.delete(name);
        this.className = [...classes].join(' ');
      },
      contains: name => this.className.split(/\s+/).includes(name)
    };
    for (const [name, value] of Object.entries(attributes)) this.setAttribute(name, value);
    this.defaultValue = this.value;
    this.defaultChecked = this.checked;
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
    if (name === 'id' || name === 'type' || name === 'value') this[name] = String(value);
    if (name === 'class') this.className = String(value);
    if (['hidden', 'disabled', 'checked'].includes(name)) this[name] = true;
    if (name.startsWith('data-')) this.dataset[name.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())] = String(value);
  }
  getAttribute(name) { return Object.hasOwn(this.attributes, name) ? this.attributes[name] : null; }
  removeAttribute(name) { delete this.attributes[name]; }
  appendChild(element) { this.children.push(element); element.parentElement = this; return element; }
  get textContent() { return this._text + this.children.map(child => child.textContent).join(''); }
  set textContent(value) { this._text = String(value); this.children = []; }
  get innerHTML() { return this.textContent; }
  set innerHTML(value) { this.textContent = value; }
  querySelectorAll(selector) {
    const nodes = [];
    const matches = element => {
      if (selector.startsWith('.')) return element.classList.contains(selector.slice(1));
      if (selector.startsWith('[')) return element.getAttribute(selector.slice(1, -1)) !== null;
      return element.tagName === selector.toUpperCase();
    };
    const walk = element => {
      for (const child of element.children) {
        if (matches(child)) nodes.push(child);
        walk(child);
      }
    };
    walk(this);
    return nodes;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  addEventListener(name, listener) {
    if (!this.listeners.has(name)) this.listeners.set(name, []);
    this.listeners.get(name).push(listener);
  }
  async emit(name, options = {}) {
    const event = { target: this, preventDefault() {}, ...options };
    for (const listener of this.listeners.get(name) || []) await listener(event);
  }
  focus() { this.ownerDocument.activeElement = this; }
  reset() {
    for (const input of this.querySelectorAll('input')) {
      input.value = input.defaultValue;
      input.checked = input.defaultChecked;
    }
  }
}

export function simulationDocumentFromHTML(html) {
  const document = {
    activeElement: null,
    title: '',
    listeners: new Map(),
    addEventListener: SimulationElement.prototype.addEventListener,
    emit: SimulationElement.prototype.emit,
    createElement(tag) { return new SimulationElement(tag, document); },
    getElementById(id) { return all().find(element => element.id === id) || null; },
    querySelectorAll(selector) { return root.querySelectorAll(selector); },
    querySelector(selector) { return root.querySelector(selector); }
  };
  const root = document.createElement('document');
  const stack = [root];
  const tokens = html.match(/<[^>]+>|[^<]+/g) || [];
  for (const token of tokens) {
    if (token.startsWith('<!')) continue;
    if (token.startsWith('</')) { stack.pop(); continue; }
    if (token.startsWith('<')) {
      const match = token.match(/^<([\w-]+)/);
      if (!match) continue;
      const attributes = {};
      const source = token.slice(match[0].length).replace(/\/?\s*>$/, '');
      for (const attr of source.matchAll(/([\w:-]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g)) attributes[attr[1]] = attr[2] ?? attr[3] ?? attr[4] ?? '';
      const element = new SimulationElement(match[1], document, attributes);
      stack.at(-1).appendChild(element);
      if (!['meta', 'link', 'input', 'br', 'hr', 'img'].includes(match[1]) && !token.endsWith('/>')) stack.push(element);
    } else stack.at(-1)._text += token;
  }
  const all = () => {
    const result = [];
    const walk = node => { result.push(node); node.children.forEach(walk); };
    walk(root);
    return result;
  };
  document.documentElement = root.querySelector('html') || root;
  document.body = root.querySelector('body') || root;
  return document;
}

export async function desktopAuthSimulation(options = {}) {
  const document = simulationDocumentFromHTML(setupHTML);
  const calls = [];
  const navigations = [];
  const storageWrites = [];
  const windowListeners = new Map();
  const state = {
    mode: 'login', adminURL: '/admin/', deviceStatus: 'missing',
    credentialStorage: { available: true, stored: false, automaticSignIn: false, state: 'missing' },
    ...clone(options.state)
  };
  const defaults = {
    State: () => clone(state),
    AutomaticSignIn: () => { if (state.mode === 'setup') throw 'automatic_sign_in_unavailable'; return { status: state.deviceStatus }; },
    Complete: () => ({ recoveryCode: 'simulation-setup-recovery', trustStatus: 'not_requested', adminURL: '/admin/' }),
    FinishSetup: () => ({ handoffToken: 'simulation-finish-handoff', adminURL: '/admin/' }),
    SignIn: () => ({ handoffToken: 'simulation-password-handoff', adminURL: '/admin/' }),
    RecoverPassword: () => ({ recoveryCode: 'simulation-replacement-code' })
  };
  const bridge = {};
  for (const name of Object.keys(defaults)) {
    bridge[name] = async (...args) => {
      calls.push({ name, args: clone(args) });
      return (options.methods?.[name] || defaults[name])(...args, { document, calls, state });
    };
  }
  const context = vm.createContext({
    document,
    navigator: { language: options.language || 'en-US', clipboard: { writeText: async value => { context.copied = value; } } },
    localStorage: { getItem: () => null, setItem: (...args) => storageWrites.push(args) },
    location: { pathname: options.pathname || '/', replace: value => navigations.push(value) },
    go: { main: { DesktopSetupBridge: bridge } },
    addEventListener: (name, listener) => windowListeners.set(name, listener),
    runtime: { Quit: () => { context.quitCalls++; } },
    quitCalls: 0,
    close: () => { context.quitCalls++; },
    TextEncoder, URL, setTimeout, clearTimeout
  });
  context.window = context;
  vm.runInContext(setupSource, context, { filename: fileURLToPath(new URL('../desktop/setup/setup.js', import.meta.url)) });
  await vm.runInContext('setupReady', context);
  return {
    document, calls, navigations, storageWrites, context, state,
    element: id => document.getElementById(id),
    run: source => vm.runInContext(source, context),
    callsFor: name => calls.filter(call => call.name === name),
    windowEvent: (name, event = {}) => windowListeners.get(name)?.(event)
  };
}

async function fillSetup(ui, password = 'DesktopSetup8!') {
  await ui.element('beginButton').emit('click');
  ui.element('passwordInput').value = password;
  ui.element('confirmInput').value = password;
}

async function acknowledge(ui) {
  ui.element('savedCheck').checked = true;
  await ui.element('savedCheck').emit('change');
  await ui.element('finishButton').emit('click');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  test('simulation: initial native login calls State and AutomaticSignIn once with zero arguments', async () => {
    const ui = await desktopAuthSimulation();
    assert.deepEqual(ui.calls, [{ name: 'State', args: [] }, { name: 'AutomaticSignIn', args: [] }]);
    assert.equal(ui.element('loginView').hidden, false);
    assert.equal(ui.document.activeElement.id, 'desktopPasswordInput');
    await ui.run('loadState()');
    await ui.element('desktopForgotButton').emit('click');
    await ui.element('recoveryBackButton').emit('click');
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
    assert.equal(ui.callsFor('State').length, 1);
    assert.equal(ui.navigations.length, 0);
  });

  test('simulation: native rejection of automatic sign-in during fresh setup is nonfatal and never retried', async () => {
    const ui = await desktopAuthSimulation({ state: { mode: 'setup' } });
    assert.equal(ui.element('welcomeView').hidden, false);
    assert.equal(ui.element('errorView').hidden, true);
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
    await ui.run('loadState()');
    await fillSetup(ui);
    await ui.element('passwordForm').emit('submit');
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
    assert.equal(ui.element('recoveryView').hidden, false);
  });

  test('simulation: setup and sign-in trust consent defaults off, with complete bilingual labels', async () => {
    const ui = await desktopAuthSimulation();
    assert.equal(ui.element('trustDeviceCheck').checked, false);
    assert.equal(ui.element('desktopTrustDeviceCheck').checked, false);
    assert.deepEqual(ui.run('Object.keys(setupCopy.en).sort()').join(','), ui.run('Object.keys(setupCopy.zh).sort()').join(','));
    const languageButton = ui.element('languageButton');
    for (const lang of ['en', 'zh', 'en']) {
      ui.run(`setupLang = '${lang}'; applyCopy()`);
      for (const node of ui.document.querySelectorAll('[data-copy]')) assert.notEqual(node.textContent, node.dataset.copy);
      for (const button of ui.document.querySelectorAll('.visibility-button')) {
        assert.match(button.getAttribute('aria-label'), lang === 'zh' ? /显示|隐藏/ : /Show|Hide/);
      }
    }
    await languageButton.emit('click');
    assert.equal(languageButton.getAttribute('aria-label'), '切换为英语');
    const visibility = ui.document.querySelectorAll('.visibility-button').find(button => button.dataset.target === 'desktopPasswordInput');
    await visibility.emit('click');
    assert.equal(visibility.getAttribute('aria-pressed'), 'true');
    assert.equal(visibility.getAttribute('aria-label'), '隐藏密码');
    assert.deepEqual(ui.storageWrites, [['cline_admin_lang', 'zh']]);
  });

  test('simulation: automatic sign-in handoff stays on the embedded origin and ready never repeats it', async () => {
    const ui = await desktopAuthSimulation({ methods: { AutomaticSignIn: () => ({ handoffToken: 'simulation+/handoff', adminURL: '/admin/' }) } });
    assert.deepEqual(ui.navigations, ['/admin/#desktop-handoff=simulation%2B%2Fhandoff']);
    assert.equal(ui.callsFor('SignIn').length, 0);
    const ready = await desktopAuthSimulation({ state: { mode: 'ready' } });
    assert.equal(ready.callsFor('AutomaticSignIn').length, 0);
    assert.deepEqual(ready.navigations, ['/admin/']);
    const reload = await desktopAuthSimulation({ pathname: '/admin/' });
    assert.equal(reload.callsFor('AutomaticSignIn').length, 0);
  });

  test('simulation: setup is two-stage and FinishSetup runs once only after code-save acknowledgement', async () => {
    let finishDocument;
    const ui = await desktopAuthSimulation({
      state: { mode: 'setup' },
      methods: { FinishSetup: ({ document }) => {
        finishDocument = document;
        assert.equal(document.getElementById('recoveryCode').textContent, '');
        return { handoffToken: 'simulation-finish-handoff', adminURL: '/admin/' };
      } }
    });
    await fillSetup(ui);
    await ui.element('passwordForm').emit('submit');
    assert.deepEqual(ui.callsFor('Complete')[0].args, ['DesktopSetup8!', 'DesktopSetup8!', false]);
    assert.equal(ui.element('recoveryView').hidden, false);
    assert.equal(ui.element('recoveryCode').textContent, 'simulation-setup-recovery');
    assert.equal(ui.element('passwordInput').value, '');
    assert.equal(ui.element('confirmInput').value, '');
    assert.equal(ui.element('finishButton').disabled, true);
    assert.equal(ui.callsFor('FinishSetup').length, 0);
    assert.equal(ui.navigations.length, 0);
    await ui.element('finishButton').emit('click');
    assert.equal(ui.callsFor('FinishSetup').length, 0);
    await acknowledge(ui);
    await ui.element('finishButton').emit('click');
    assert.equal(finishDocument, ui.document);
    assert.deepEqual(ui.callsFor('FinishSetup'), [{ name: 'FinishSetup', args: [] }]);
    assert.equal(ui.run('setupResult'), null);
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.deepEqual(ui.navigations, ['/admin/#desktop-handoff=simulation-finish-handoff']);
  });

  test('simulation: setup sends explicit consent and enforces Unicode length, byte length, and confirmation', async () => {
    const ui = await desktopAuthSimulation({ state: { mode: 'setup' } });
    await fillSetup(ui, '🔒'.repeat(7));
    await ui.element('passwordForm').emit('submit');
    assert.equal(ui.callsFor('Complete').length, 0);
    assert.equal(ui.element('passwordInput').getAttribute('aria-invalid'), 'true');
    await fillSetup(ui, '🔒'.repeat(257));
    await ui.element('passwordForm').emit('submit');
    assert.match(ui.element('setupError').textContent, /1024/);
    assert.equal(ui.callsFor('Complete').length, 0);
    await fillSetup(ui, '🔒'.repeat(8));
    ui.element('confirmInput').value = 'different';
    await ui.element('passwordForm').emit('submit');
    assert.match(ui.element('setupError').textContent, /do not match/);
    ui.element('confirmInput').value = '🔒'.repeat(8);
    ui.element('trustDeviceCheck').checked = true;
    await ui.element('passwordForm').emit('submit');
    assert.deepEqual(ui.callsFor('Complete')[0].args, ['🔒'.repeat(8), '🔒'.repeat(8), true]);
  });

  test('simulation: concurrent FinishSetup activations cannot request two handoffs', async () => {
    let resolveFinish;
    const ui = await desktopAuthSimulation({ state: { mode: 'setup' }, methods: { FinishSetup: () => new Promise(resolve => { resolveFinish = resolve; }) } });
    await fillSetup(ui);
    await ui.element('passwordForm').emit('submit');
    ui.element('savedCheck').checked = true;
    const first = ui.element('finishButton').emit('click');
    await ui.element('finishButton').emit('click');
    assert.equal(ui.callsFor('FinishSetup').length, 1);
    resolveFinish({ handoffToken: 'simulation-single-use', adminURL: '/admin/' });
    await first;
    assert.equal(ui.navigations.length, 1);
  });

  test('simulation: password sign-in preserves the supplied local username and sends one object argument', async () => {
    for (const [username, expected, trustDevice] of [[' operator ', 'operator', true], ['', 'admin', false]]) {
      const ui = await desktopAuthSimulation();
      ui.element('desktopUsernameInput').value = username;
      ui.element('desktopPasswordInput').value = ' unchanged password ';
      ui.element('desktopTrustDeviceCheck').checked = trustDevice;
      await ui.element('desktopLoginForm').emit('submit');
      assert.deepEqual(ui.callsFor('SignIn')[0].args, [{ username: expected, password: ' unchanged password ', trustDevice }]);
      assert.equal(ui.element('desktopPasswordInput').value, '');
      assert.deepEqual(ui.storageWrites, []);
    }
  });

  test('simulation: unavailable or corrupt credential storage disables consent but not password sign-in', async () => {
    for (const state of ['unavailable', 'corrupt']) {
      for (const mode of ['login', 'setup']) {
        const ui = await desktopAuthSimulation({ state: { mode, credentialStorage: { available: state !== 'unavailable', stored: false, state } } });
        const checkbox = ui.element(mode === 'login' ? 'desktopTrustDeviceCheck' : 'trustDeviceCheck');
        assert.equal(checkbox.disabled, true);
        checkbox.checked = true;
        if (mode === 'setup') { await fillSetup(ui); await ui.element('passwordForm').emit('submit'); }
        else { ui.element('desktopPasswordInput').value = 'DesktopSetup8!'; await ui.element('desktopLoginForm').emit('submit'); }
        const call = ui.callsFor(mode === 'setup' ? 'Complete' : 'SignIn')[0];
        assert.equal(mode === 'setup' ? call.args[2] : call.args[0].trustDevice, false);
      }
    }
  });

  test('simulation: device notices distinguish revoked, expired, missing, unavailable, corrupt, and paused', async () => {
    const notices = [];
    for (const status of ['revoked', 'expired', 'missing', 'unavailable', 'corrupt', 'paused']) {
      const ui = await desktopAuthSimulation({ state: { deviceStatus: status, credentialStorage: { available: status !== 'unavailable', stored: !['missing', 'unavailable', 'corrupt'].includes(status), automaticSignIn: false, state: status === 'paused' ? 'stored' : status } } });
      notices.push(ui.element('desktopDeviceNotice').textContent);
      if (status === 'paused') assert.equal(ui.element('desktopTrustOption').hidden, true);
    }
    assert.equal(new Set(notices).size, 6);
    assert.ok(notices.every(notice => /password/i.test(notice)));
  });

  test('simulation: trust-storage failures disable stale consent and keep a password-only retry available', async () => {
    let attempts = 0;
    const ui = await desktopAuthSimulation({ methods: { SignIn: () => {
      if (++attempts === 1) throw { code: 'desktop_storage_unavailable' };
      return { handoffToken: 'simulation-fallback-handoff', adminURL: '/admin/' };
    } } });
    ui.element('desktopPasswordInput').value = 'DesktopSetup8!';
    ui.element('desktopTrustDeviceCheck').checked = true;
    await ui.element('desktopLoginForm').emit('submit');
    assert.equal(ui.element('desktopTrustDeviceCheck').disabled, true);
    assert.equal(ui.element('desktopTrustDeviceCheck').checked, false);
    ui.element('desktopPasswordInput').value = 'DesktopSetup8!';
    await ui.element('desktopLoginForm').emit('submit');
    assert.equal(ui.callsFor('SignIn')[0].args[0].trustDevice, true);
    assert.equal(ui.callsFor('SignIn')[1].args[0].trustDevice, false);
    assert.deepEqual(ui.navigations, ['/admin/#desktop-handoff=simulation-fallback-handoff']);
  });

  test('simulation: bridge errors support rejected strings, nested errors, JSON codes, and returned failures', async () => {
    const cases = [
      ['credential store locked by policy', /credential store locked by policy/],
      [{ code: 'authentication_failed' }, /username and password/],
      ['{"code":"rate_limited"}', /Too many attempts/],
      [{ error: { code: 'desktop_credential_corrupt' } }, /corrupted/],
      [{ reason: 'The native store is locked' }, /native store is locked/]
    ];
    for (const [reason, expected] of cases) {
      const ui = await desktopAuthSimulation({ methods: { SignIn: () => { throw reason; } } });
      ui.element('desktopPasswordInput').value = 'DesktopSetup8!';
      await ui.element('desktopLoginForm').emit('submit');
      assert.match(ui.element('desktopLoginError').textContent, expected);
      assert.doesNotMatch(ui.element('desktopLoginError').textContent, /\[object Object\]/);
      assert.equal(ui.navigations.length, 0);
      assert.equal(ui.element('desktopLoginFields').disabled, false);
    }
    const ui = await desktopAuthSimulation({ language: 'zh-CN', methods: { SignIn: () => ({ success: false, error: { code: 'authentication_failed' } }) } });
    ui.element('desktopPasswordInput').value = 'DesktopSetup8!';
    await ui.element('desktopLoginForm').emit('submit');
    assert.match(ui.element('desktopLoginError').textContent, /用户名和密码/);
  });

  test('simulation: native forgot-password stays on this page, uses confirmation, and requires saving a replacement', async () => {
    const ui = await desktopAuthSimulation();
    await ui.element('desktopForgotButton').emit('click');
    assert.equal(ui.element('forgotView').hidden, false);
    assert.equal(ui.document.activeElement.id, 'recoveryCodeInput');
    assert.equal(ui.navigations.length, 0);
    ui.element('recoveryCodeInput').value = ' simulation-old-code ';
    ui.element('recoveryPasswordInput').value = 'Replacement8!';
    ui.element('recoveryConfirmInput').value = 'Replacement8!';
    await ui.element('desktopRecoveryForm').emit('submit');
    assert.deepEqual(ui.callsFor('RecoverPassword')[0].args, [{ recoveryCode: 'simulation-old-code', newPassword: 'Replacement8!', confirmation: 'Replacement8!' }]);
    assert.equal(ui.element('recoveryCode').textContent, 'simulation-replacement-code');
    assert.match(ui.element('recoveryTitle').textContent, /replacement/);
    assert.equal(ui.element('recoveryCodeInput').value, '');
    assert.equal(ui.element('recoveryPasswordInput').value, '');
    assert.equal(ui.element('recoveryConfirmInput').value, '');
    await ui.element('finishButton').emit('click');
    assert.equal(ui.element('recoveryView').hidden, false);
    assert.equal(ui.callsFor('FinishSetup').length, 0);
    await acknowledge(ui);
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.equal(ui.element('loginView').hidden, false);
    assert.equal(ui.element('desktopPasswordInput').value, '');
    assert.match(ui.element('desktopLoginNotice').textContent, /not been resumed/);
    assert.equal(ui.callsFor('SignIn').length, 0);
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
    assert.equal(ui.callsFor('FinishSetup').length, 0);
    assert.equal(ui.navigations.length, 0);
  });

  test('simulation: recovery rejection maps authentication failure without claiming password reset', async () => {
    const ui = await desktopAuthSimulation({ methods: { RecoverPassword: () => { throw 'authentication_failed'; } } });
    await ui.element('desktopForgotButton').emit('click');
    ui.element('recoveryCodeInput').value = 'simulation-wrong-code';
    ui.element('recoveryPasswordInput').value = 'Replacement8!';
    ui.element('recoveryConfirmInput').value = 'Replacement8!';
    await ui.element('desktopRecoveryForm').emit('submit');
    assert.match(ui.element('desktopRecoveryError').textContent, /invalid, already used, or replaced/);
    assert.equal(ui.element('forgotView').hidden, false);
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.equal(ui.navigations.length, 0);
  });

  test('simulation: a failed FinishSetup clears the code and offers manual sign-in without retrying the capability', async () => {
    const ui = await desktopAuthSimulation({ state: { mode: 'setup' }, methods: { FinishSetup: () => { throw { code: 'desktop_handoff_unavailable' }; } } });
    await fillSetup(ui);
    await ui.element('passwordForm').emit('submit');
    await acknowledge(ui);
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.equal(ui.element('loginView').hidden, false);
    assert.match(ui.element('desktopLoginError').textContent, /password/);
    await ui.element('finishButton').emit('click');
    assert.equal(ui.callsFor('FinishSetup').length, 1);
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
  });

  test('simulation: restart recovery is saved before quitting, with no FinishSetup or sign-in', async () => {
    const ui = await desktopAuthSimulation({ state: { mode: 'setup' }, methods: { Complete: () => ({ recoveryCode: 'simulation-restart-code', restartRequired: true, trustStatus: 'not_requested' }) } });
    await fillSetup(ui);
    await ui.element('passwordForm').emit('submit');
    await ui.element('finishButton').emit('click');
    assert.equal(ui.context.quitCalls, 0);
    assert.equal(ui.element('restartNotice').hidden, false);
    await acknowledge(ui);
    assert.equal(ui.context.quitCalls, 1);
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.equal(ui.callsFor('FinishSetup').length, 0);
    assert.equal(ui.navigations.length, 0);
  });

  test('simulation: legacy-password rejection offers the existing upgrade workflow without another automatic attempt', async () => {
    const ui = await desktopAuthSimulation({ methods: { SignIn: () => { throw 'password_rotation_required'; } } });
    ui.element('desktopPasswordInput').value = 'SimulationOld8!';
    await ui.element('desktopLoginForm').emit('submit');
    assert.equal(ui.element('continuationView').hidden, false);
    assert.equal(ui.element('desktopPasswordInput').value, '');
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
    await ui.element('continueButton').emit('click');
    assert.deepEqual(ui.navigations, ['/admin/']);
  });

  test('simulation: prepared recovery with a replacement code is saved before a required restart', async () => {
    const ui = await desktopAuthSimulation({ methods: { RecoverPassword: () => ({ success: false, error: 'security_restart_required', data: { recoveryCode: 'simulation-prepared-replacement' } }) } });
    await ui.element('desktopForgotButton').emit('click');
    ui.element('recoveryCodeInput').value = 'simulation-old-code';
    ui.element('recoveryPasswordInput').value = 'SimulationReset8!';
    ui.element('recoveryConfirmInput').value = 'SimulationReset8!';
    await ui.element('desktopRecoveryForm').emit('submit');
    assert.equal(ui.element('recoveryCode').textContent, 'simulation-prepared-replacement');
    assert.equal(ui.element('restartNotice').hidden, false);
    assert.equal(ui.context.quitCalls, 0);
    await acknowledge(ui);
    assert.equal(ui.context.quitCalls, 1);
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.equal(ui.callsFor('SignIn').length, 0);
  });

  test('simulation: legacy and offline recovery continue only to the existing embedded administrator workflow', async () => {
    for (const mode of ['legacy', 'recovery']) {
      const ui = await desktopAuthSimulation({ state: { mode } });
      assert.equal(ui.element('continuationView').hidden, false);
      assert.equal(ui.callsFor('AutomaticSignIn').length, 0);
      assert.equal(ui.navigations.length, 0);
      await ui.element('continueButton').emit('click');
      assert.deepEqual(ui.navigations, ['/admin/']);
    }
  });

  test('simulation: remote handoff URLs are refused, and pagehide removes recovery and password secrets', async () => {
    const blocked = await desktopAuthSimulation({ methods: { SignIn: () => ({ adminURL: 'http://127.0.0.1:9999/admin/', handoffToken: 'simulation-token' }) } });
    blocked.element('desktopPasswordInput').value = 'DesktopSetup8!';
    await blocked.element('desktopLoginForm').emit('submit');
    assert.equal(blocked.navigations.length, 0);
    assert.match(blocked.element('desktopLoginError').textContent, /embedded/);
    const ui = await desktopAuthSimulation({ state: { mode: 'setup' } });
    await fillSetup(ui);
    await ui.element('passwordForm').emit('submit');
    let prevented = false;
    ui.windowEvent('beforeunload', { preventDefault: () => { prevented = true; } });
    assert.equal(prevented, true);
    ui.windowEvent('pagehide');
    assert.equal(ui.element('recoveryCode').textContent, '');
    assert.equal(ui.run('setupResult'), null);
    ui.windowEvent('pageshow', { persisted: true });
    assert.equal(ui.callsFor('AutomaticSignIn').length, 1);
    assert.equal(ui.element('loginView').hidden, false);
  });
}

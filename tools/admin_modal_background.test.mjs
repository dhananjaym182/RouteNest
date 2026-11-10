import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

function element({ visible = true, children = [] } = {}) {
  const attributes = new Map();
  return {
    children,
    hidden: !visible,
    isConnected: true,
    style: { display: visible ? 'flex' : 'none' },
    contains(node) { return node === this || this.children.some(child => child.contains(node)); },
    querySelector() { return this; },
    setAttribute(name, value) { attributes.set(name, value); },
    removeAttribute(name) { attributes.delete(name); },
    hasAttribute(name) { return attributes.has(name); },
    focus() {},
  };
}

function modalUI() {
  const layout = element();
  const login = element({ visible: false });
  const recovery = element({ visible: false });
  const confirm = element({ visible: false });
  const drawer = element({ visible: false });
  const nodes = [layout, login, recovery, confirm, drawer];
  const context = vm.createContext({
    document: { body: { children: nodes }, activeElement: layout, querySelectorAll: () => [login, recovery, confirm] },
    getComputedStyle: node => ({ display: node.style.display }),
    requestAnimationFrame: () => {},
    setTimeout: callback => callback(),
    visibleFocusable: () => [],
    MutationObserver: class { observe() {} },
  });
  const source = readFileSync(new URL('../internal/admin/ui/js/03_shell.js', import.meta.url), 'utf8');
  const start = source.indexOf('const _modalStack');
  const end = source.indexOf('document.addEventListener', start);
  vm.runInContext(source.slice(start, end), context);
  const sync = node => { context.node = node; vm.runInContext('syncModalFocusState(node)', context); };
  return { layout, login, recovery, confirm, drawer, sync };
}

test('login and nested recovery exclude background and lower dialogs from keyboard and accessibility trees', () => {
  const ui = modalUI();
  ui.login.style.display = 'flex'; ui.login.hidden = false; ui.sync(ui.login);
  assert.equal(ui.layout.hasAttribute('inert'), true);
  assert.equal(ui.login.hasAttribute('inert'), false);
  ui.recovery.style.display = 'flex'; ui.recovery.hidden = false; ui.sync(ui.recovery);
  assert.equal(ui.login.hasAttribute('inert'), true);
  assert.equal(ui.recovery.hasAttribute('inert'), false);
  ui.recovery.style.display = 'none'; ui.sync(ui.recovery);
  assert.equal(ui.login.hasAttribute('inert'), false);
  assert.equal(ui.layout.hasAttribute('inert'), true);
  ui.login.style.display = 'none'; ui.sync(ui.login);
  assert.equal(ui.layout.hasAttribute('inert'), false);
});

test('modal completion restores pre-existing closed-drawer inert state', () => {
  const ui = modalUI();
  ui.drawer.setAttribute('inert', '');
  ui.confirm.style.display = 'flex'; ui.confirm.hidden = false; ui.sync(ui.confirm);
  ui.confirm.style.display = 'none'; ui.sync(ui.confirm);
  assert.equal(ui.drawer.hasAttribute('inert'), true);
  assert.equal(ui.layout.hasAttribute('inert'), false);
});

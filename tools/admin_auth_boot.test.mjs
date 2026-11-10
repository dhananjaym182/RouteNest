import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../internal/admin/ui/js/16_init.js', import.meta.url), 'utf8');

async function boot() {
  const calls = [];
  const handlers = new Map();
  const context = vm.createContext({
    _adminPrincipal: { userID: 'break-glass' },
    applyLang() {},
    initializeAdminAuth: async () => true,
    getActiveTab: () => 'security',
    applyTab: tab => calls.push(tab),
    loadModels: async () => {},
    loadConfig() {},
    loadStats() {},
    checkModelDelistings() {},
    setInterval() {},
    document: { hidden: false, addEventListener() {} },
    window: { addEventListener: (name, handler) => handlers.set(name, handler) }
  });
  vm.runInContext(source, context);
  await Promise.resolve();
  await Promise.resolve();
  return { calls, handlers, context };
}

test('authenticated history still dispatches the canonical console destination', async () => {
  const ui = await boot();
  ui.handlers.get('hashchange')();
  assert.deepEqual(ui.calls, ['security', 'security']);
});

test('expired-session history never invokes dashboard navigation from login or recovery', async () => {
  const ui = await boot();
  ui.context._adminPrincipal = null;
  ui.handlers.get('hashchange')();
  assert.deepEqual(ui.calls, ['security']);
});

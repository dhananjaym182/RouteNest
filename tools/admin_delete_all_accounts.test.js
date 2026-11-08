const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const source = fs.readFileSync(new URL('../internal/admin/ui/js/04_accounts.js', `file://${__dirname}/`), 'utf8');

function harness(confirmResult = true) {
  const calls = [];
  const toasts = [];
  const button = { disabled: false, innerHTML: 'Delete all accounts' };
  const context = {
    showConfirm: async message => { calls.push(['confirm', message]); return confirmResult; },
    api: async (...args) => { calls.push(['api', ...args]); return { success: true }; },
    toast: (...args) => { toasts.push(args); },
    loadAccounts: async () => { calls.push(['loadAccounts']); },
    loadStats: async () => { calls.push(['loadStats']); },
    loadGlobal: async value => { calls.push(['loadGlobal', value]); },
    t: value => value,
    console,
  };
  vm.createContext(context);
  vm.runInContext(source, context);
  context.loadAccounts = async () => { calls.push(['loadAccounts']); };
  context.loadStats = async () => { calls.push(['loadStats']); };
  context.loadGlobal = async value => { calls.push(['loadGlobal', value]); };
  return { context, calls, toasts, button };
}

test('deleteAllAccounts cancels without a request', async () => {
  const { context, calls, button } = harness(false);
  await context.deleteAllAccounts(button);
  assert.equal(calls.filter(call => call[0] === 'api').length, 0);
  assert.equal(button.disabled, false);
  assert.equal(button.innerHTML, 'Delete all accounts');
});

test('deleteAllAccounts sends one request and restores feedback state', async () => {
  const { context, calls, toasts, button } = harness(true);
  await Promise.all([context.deleteAllAccounts(button), context.deleteAllAccounts(button)]);
  const apiCalls = calls.filter(call => call[0] === 'api');
  assert.equal(apiCalls.length, 1);
  assert.deepEqual(apiCalls[0].slice(0, 3), ['api', 'POST', '/accounts/delete-all']);
  assert.equal(JSON.stringify(apiCalls[0][3]), '{}');
  assert.equal(button.disabled, false);
  assert.equal(button.innerHTML, 'Delete all accounts');
  assert.deepEqual(toasts, [['全部 Cline 账号已删除；客户端 API 密钥和独立配置已保留', 'success']]);
});

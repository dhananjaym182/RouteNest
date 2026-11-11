// Run with: node --test tools/admin_credentials_page.test.mjs
// Accounts & Credentials (#credentials): permission gating, load / failure states, the summary,
// account-health refresh, the generated-key banner timer, the Cline pool tab and handler escaping.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const ui = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');
const assets = ['04_accounts.js', '05_settings.js', '11_connections.js', '15_dashboard.js'];
// 01_core.js needs the whole i18n dictionary at load; take only its real escaping helpers.
const coreLine = prefix => ui('js/01_core.js').split('\n').find(line => line.startsWith(prefix));
const escapeHTML = value => String(value == null ? '' : value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

function createUI({ permissions = ['credential.write'], api: apiImpl } = {}) {
  const elements = new Map();
  const field = (extra = {}) => ({ innerHTML: '', textContent: '', hidden: false, style: {}, dataset: {}, children: [], appendChild() {}, querySelector: () => null, querySelectorAll: () => [], ...extra });
  const subpages = ['conns', 'cline', 'inventory', 'client-keys'].map(name => field({ dataset: { subpage: name } }));
  const tabs = field();
  const panel = field({
    querySelector: selector => selector === '.credential-view-tabs' ? tabs : null,
    querySelectorAll: selector => selector === ':scope > .page-subpage' ? subpages : [],
  });
  for (const id of ['credentialSummary', 'credentialAccessNote', 'connectionsList', 'dashAttention', 'keysList', 'keyGenResult', 'accountTableBody', 'accountCards', 'accountPager', 'accSearchCount', 'selCount', 'delCount', 'resetCount', 'accSelAll']) elements.set(id, field());
  elements.set('tab-credentials', panel);
  elements.get('credentialAccessNote').hidden = true;

  const calls = [];
  const timers = [];
  const toasts = [];
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    esc: escapeHTML,
    document: {
      createElement: () => { const el = field(); el.addEventListener = () => {}; el.setAttribute = () => {}; el.appendChild = child => el.children.push(child); return el; },
      querySelector: () => null, querySelectorAll: () => [], addEventListener() {},
    },
    window: {},
    t: value => value, tx: value => value,
    uiIcon: name => '<i class="ic-' + name + '"></i>',
    ICON_LOGINS: '', ICON_ACCOUNTS: '',
    formatNumber: value => String(value), formatTokenCount: value => String(value), formatDateTime: () => '', formatCooldown: () => '',
    countLabel: undefined,
    toast: (message, kind) => toasts.push({ message, kind }),
    showConfirm: async () => true,
    copyText() {}, fallbackCopy() {},
    adminHasPermission: permission => permissions.includes(permission),
    loadStats() {}, loadGlobal() {}, renderModelUsage() {}, _cachedModels: [{}],
    refreshProvDetailIfOpen() {}, renderConnPick() {}, connImportBuildSelect() {}, flowLabel: value => value,
    _globalData: null,
    setTimeout: (fn, ms) => { timers.push({ fn, ms, cleared: false }); return timers.length - 1; },
    clearTimeout: id => { if (timers[id]) timers[id].cleared = true; },
    Date, JSON, Math, Set, Object, Array, String, Number,
    api: async (method, path) => { calls.push(method + ' ' + path); return apiImpl ? apiImpl(method, path) : { data: {} }; },
  });
  context.window = context;
  vm.runInContext([coreLine('const escAttr'), coreLine('const jsArg')].join('\n'), context, { filename: '01_core.js (escaping helpers)' });
  for (const asset of assets) vm.runInContext(ui('js/' + asset), context, { filename: asset });
  return {
    context, calls, timers, toasts, elements, subpages, tabs,
    run: source => vm.runInContext(source, context),
    html: id => elements.get(id).innerHTML,
  };
}

const okApi = (accounts = [], connections = [], health = {}) => async (method, path) => {
  if (path === '/accounts') return { data: { accounts } };
  if (path === '/connections') return { data: { connections, flows: [], flowLabels: {} } };
  if (path === '/account-health') return { data: { accounts: health } };
  if (path === '/keys') return { data: { keys: [] } };
  return { data: {} };
};

test('without credential.write nothing is requested, and the page explains why instead of showing empty states', async () => {
  const page = createUI({ permissions: ['admin.view'], api: okApi() });
  await page.run('loadAccounts()');
  await page.run('loadConnections()');
  assert.deepEqual(page.calls, [], 'no 403-bound call is made');
  assert.equal(page.toasts.length, 0, 'no error toast');
  assert.equal(page.elements.get('credentialAccessNote').hidden, false, 'the explanation is shown');
  assert.equal(page.tabs.hidden, true, 'the sub-tabs are hidden');
  assert.ok(page.subpages.every(p => p.hidden), 'every sub-page is hidden');
  assert.equal(page.html('credentialSummary'), '', 'no summary tiles that read "none connected"');
  assert.doesNotMatch(page.html('connectionsList'), /暂无登录连接/);
});

test('with credential.write the explanation is hidden and the sub-pages are available', async () => {
  const page = createUI({ api: okApi([{ accountId: 'acc_1', email: 'a@x.test', status: 'active' }]) });
  await page.run('loadAccounts()');
  assert.equal(page.elements.get('credentialAccessNote').hidden, true);
  assert.equal(page.tabs.hidden, false);
  assert.ok(page.subpages.every(p => p.hidden === false));
});

test('the dashboard attention panel does not claim the pool is healthy when the role cannot read it', async () => {
  const denied = createUI({ permissions: ['admin.view'], api: okApi() });
  await denied.run('renderAttention()');
  assert.match(denied.html('dashAttention'), /账号与连接状态需要凭据管理权限/);
  assert.doesNotMatch(denied.html('dashAttention'), /账号池健康/);
  assert.deepEqual(denied.calls, []);

  const allowed = createUI({ api: okApi() });
  await allowed.run('renderAttention()');
  assert.match(allowed.html('dashAttention'), /账号池健康/);
});

test('a failed connections load is shown as a failure with a retry, not as "no connections"', async () => {
  const page = createUI({ api: async (method, path) => { if (path === '/connections') throw new Error('boom <b>x</b>'); return { data: {} }; } });
  await page.run('loadConnections()');
  const html = page.html('connectionsList');
  assert.match(html, /加载登录连接失败/);
  assert.match(html, /boom &lt;b&gt;x&lt;\/b&gt;/, 'the message is escaped');
  assert.match(html, /loadConnections\(true\)/, 'retry reloads fresh');
  assert.doesNotMatch(html, /暂无登录连接/);
});

test('summary tiles say loading until their own source answers, and failed when it fails', async () => {
  const page = createUI({ api: async (method, path) => { if (path === '/connections') throw new Error('down'); return { data: {} }; } });
  page.run('renderCredentialSummary()');
  let html = page.html('credentialSummary');
  assert.equal((html.match(/加载中…/g) || []).length, 3, 'all three tiles are loading, none claims 0');
  assert.doesNotMatch(html, /尚未登录|尚未配置/);

  await page.run('loadConnections()');
  html = page.html('credentialSummary');
  assert.match(html, /is-error[\s\S]*加载失败/);
  assert.equal((html.match(/加载失败/g) || []).length, 1, 'only the failed tile reports failure');
  assert.equal((html.match(/加载中…/g) || []).length, 2);
});

test('summary shows real counts once loaded, including a genuine zero', async () => {
  const page = createUI({ api: okApi([{ accountId: 'a1', email: 'x', status: 'active' }, { accountId: 'a2', email: 'y', status: 'active' }], []) });
  await page.run('loadConnections()');
  await page.run('loadAccounts()');
  page.run("_globalData = { credentials: [{ id: 'c1', providerIds: ['p1'], usageCount: 4 }] }; renderCredentialSummary()");
  const html = page.html('credentialSummary');
  assert.match(html, /summary-value">0<[\s\S]*尚未登录/, 'a loaded, empty OAuth list is a real zero');
  assert.match(html, /summary-value">1<[\s\S]*个 Provider/);
  assert.match(html, /summary-value">2<[\s\S]*内置账号池/);
});

test('a failed /accounts load marks the pool tile failed', async () => {
  const page = createUI({ api: async (method, path) => { if (path === '/accounts') throw new Error('nope'); return { data: { accounts: {} } }; } });
  await page.run('loadAccounts()');
  assert.match(page.html('credentialSummary'), /内置账号池|加载失败/);
  assert.match(page.html('credentialSummary'), /is-error/);
  assert.equal(page.toasts.length, 1);
});

test('account health is refetched on every account load and survives a failed refresh', async () => {
  let healthCalls = 0, fail = false;
  const page = createUI({
    api: async (method, path) => {
      if (path === '/account-health') { healthCalls++; if (fail) throw new Error('x'); return { data: { accounts: { acc_1: [1, 1, 0] } } }; }
      if (path === '/accounts') return { data: { accounts: [{ accountId: 'acc_1', email: 'a@x.test', status: 'active' }] } };
      return { data: {} };
    },
  });
  await page.run('loadAccounts()');
  await page.run('loadAccounts()');
  assert.equal(healthCalls, 2, 'refetched, not cached for the page lifetime');
  assert.match(page.html('accountTableBody'), /heat-blocks/);
  fail = true;
  await page.run('loadAccounts()');
  assert.equal(healthCalls, 3);
  assert.match(page.html('accountTableBody'), /heat-blocks/, 'previous strips are kept when the refresh fails');
});

test('a second generated key is not wiped by the first key\'s timer', async () => {
  const page = createUI({ api: async (method, path) => path === '/keys/generate' ? { data: { key: 'sk-' + Math.random() } } : { data: { keys: [] } } });
  await page.run('generateKey(null)');
  await page.run('generateKey(null)');
  const live = page.timers.filter(t => !t.cleared);
  assert.equal(page.timers.length, 2);
  assert.equal(live.length, 1, 'the first timer was cancelled; only the latest one can clear the banner');
  assert.equal(page.timers[0].cleared, true);
});

test('a failed key-list load shows the reason and a retry', async () => {
  const page = createUI({ api: async () => { throw new Error('store <i>down</i>'); } });
  await page.run('loadKeys()');
  const html = page.html('keysList');
  assert.match(html, /加载失败: store &lt;i&gt;down&lt;\/i&gt;/);
  assert.match(html, /onclick="loadKeys\(\)"/);
});

test('header refresh also reloads the client keys, and keys are never requested without permission', async () => {
  const allowed = createUI({ api: okApi() });
  allowed.context.loadConnections = async () => {};
  allowed.run('refreshCredentials()');
  await Promise.resolve();
  assert.ok(allowed.calls.includes('GET /keys'));
  assert.ok(allowed.calls.includes('GET /accounts'));

  const denied = createUI({ permissions: ['admin.view'], api: okApi() });
  denied.context.loadConnections = async () => {};
  denied.run('refreshCredentials()');
  await Promise.resolve();
  assert.deepEqual(denied.calls.filter(call => call !== 'GET /stats'), [], 'only /stats (admin.view) is allowed');
});

// Decode an HTML attribute value the way the parser does, then run it as the event handler would.
const decodeAttr = value => value.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
function runHandlers(html, id) {
  const seen = [];
  const sandbox = vm.createContext({
    alert: () => seen.push('INJECTED'),
    ...Object.fromEntries(['onSelAccount', 'testAccount', 'resetAccount', 'deleteAccount', 'testConnection', 'deleteConnection', 'toggleConnStats'].map(name => [name, (...args) => seen.push([name, args[0]])])),
    toggleModelUsage() {}, // handlers that take no id are irrelevant here
  });
  const handlers = [...html.matchAll(/ on(?:click|change)="([^"]*)"/g)].map(m => decodeAttr(m[1]));
  for (const code of handlers) vm.runInContext(`(function(){ ${code} }).call({ checked: true })`, sandbox);
  return { seen, count: handlers.length };
}

test('hostile account and connection ids stay one string argument and never execute', async () => {
  const evil = `x');alert(1);//"><img src=x onerror=alert(2)>`;
  const page = createUI({ api: okApi([{ accountId: evil, email: 'a@x.test', status: 'active' }], [{ id: evil, provider: 'kilocode', status: 'active', authType: 'device_code', email: 'e@x.test' }]) });
  await page.run('loadAccounts()');
  await page.run('loadConnections()');
  for (const [name, html] of [['table', page.html('accountTableBody')], ['cards', page.html('accountCards')], ['connections', page.html('connectionsList')]]) {
    assert.doesNotMatch(html, /<img/, name + ': no injected element');
    const { seen, count } = runHandlers(html, evil);
    assert.ok(count >= 3, name + ': handlers were found (' + count + ')');
    assert.ok(!seen.includes('INJECTED'), name + ': hostile id executed code');
    assert.ok(seen.every(entry => entry[1] === evil), name + ': every handler received the id intact');
  }
  assert.match(page.html('accountTableBody'), /data-account-id="x&#39;\);alert\(1\)/);
});

test('"select all" fallback reads account ids from data-account-id, not from the handler text', () => {
  const page = createUI();
  const ids = [];
  const box = { checked: true };
  const checkboxes = ["a'1", 'b"2'].map(id => ({ dataset: { accountId: id }, checked: false, getAttribute: () => { throw new Error('must not parse onchange'); } }));
  page.context.document.querySelectorAll = () => checkboxes;
  page.run('_accSearch = ""; _lastAccountList = []');
  page.run('toggleAllAccountSel({ checked: true })');
  assert.deepEqual(JSON.parse(page.run('JSON.stringify([..._checkedAccounts])')), ["a'1", 'b"2']);
  assert.ok(checkboxes.every(cb => cb.checked));
  assert.ok(box && ids);
});

test('the Cline account pool has its own tab; OAuth connections no longer carry it', () => {
  const html = ui('html/pages/accounts.html');
  const subpage = name => {
    const start = html.indexOf(`data-subpage="${name}"`);
    const rest = html.slice(start + 1);
    const next = rest.search(/data-subpage="/);
    return html.slice(start, next < 0 ? html.length : start + 1 + next);
  };
  assert.match(html, /<button[^>]*data-sub="cline"[^>]*>Cline 账号池<\/button>/);
  const cline = subpage('cline'), conns = subpage('conns');
  for (const id of ['accountTableBody', 'accountCards', 'accountPager', 'modelUsageSection', 'poolCapBar', 'statTotal']) {
    assert.ok(cline.includes(`id="${id}"`), id + ' belongs to the Cline tab');
    assert.ok(!conns.includes(`id="${id}"`), id + ' is not on the OAuth tab');
  }
  assert.ok(conns.includes('id="connectionsList"') && conns.includes('data-conn-provider-groups'));
  assert.equal((html.match(/data-sub="/g) || []).length, (html.match(/data-subpage="/g) || []).length, 'every tab has a sub-page');
  assert.match(ui('js/02_nav.js'), /sub === 'pool'\) sub = 'cline'/, 'dashboard "pool" links land on the Cline tab');
  assert.match(html, /onclick="refreshCredentials\(\)">刷新/);
  assert.match(html, /id="credentialAccessNote"[^>]*hidden/);
});

test('the client-keys note and tooltip describe what Copy actually does', () => {
  const html = ui('html/pages/accounts.html'), settings = ui('js/05_settings.js');
  assert.doesNotMatch(html + settings, /新密钥只在生成后短暂完整显示|完整密钥已隐藏|新密钥仅在此处完整显示/);
  assert.match(html, /点击「复制」会复制完整密钥/);
});

test('the page says which credentials RouteNest uses upstream and which keys clients use inbound', () => {
  const html = ui('html/pages/accounts.html');
  const dictionarySource = ui('js/01_i18n.js');
  const dictionary = new Function(dictionarySource.slice(dictionarySource.indexOf('const I18N = {'), dictionarySource.indexOf('\n};', dictionarySource.indexOf('const I18N = {')) + 3).replace('const I18N =', 'return'))();

  const subtitle = html.match(/class="large-subtitle">([^<]+)</)[1];
  assert.match(subtitle, /RouteNest 调用上游服务/, 'subtitle names the upstream direction');
  assert.match(subtitle, /客户端调用 RouteNest/, 'subtitle names the inbound direction');

  const tabs = [...html.matchAll(/<button[^>]*title="([^"]+)"[^>]*class="subtab[^"]*"[^>]*data-sub="([^"]+)"/g)].map(m => [m[2], m[1]]);
  assert.deepEqual(tabs.map(([sub]) => sub), ['conns', 'cline', 'inventory', 'client-keys']);
  for (const [sub, title] of tabs) {
    assert.match(title, sub === 'client-keys' ? /^入站：/ : /^上游：/, sub + ' tooltip states its direction');
    assert.ok(title in dictionary, sub + ' tooltip has an English entry');
  }
  assert.ok(subtitle in dictionary, 'subtitle has an English entry');
  assert.match(html, /data-sub="client-keys"[^>]*>客户端 API 密钥</, 'the established "client API key" term is kept');
});

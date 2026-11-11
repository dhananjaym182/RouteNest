// Run with: node --test tools/admin_provider_table.test.mjs
// The Providers page has one table: identity from /provider-instances, models from
// /model-offerings, and health / sync / quick actions from the managed-provider data.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const assets = ['06_providers_custom.js', '07_providers_detail.js', '08_providers_detect.js', '18_global.js', '18_provider_table.js'];
const page = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');

function createUI() {
  const elements = new Map();
  const field = () => ({ innerHTML: '', value: '', dataset: {}, classList: { add() {}, remove() {}, toggle() {}, contains: () => false } });
  for (const id of ['globalInstances', 'providerCooldowns', 'providerSyncBanner']) elements.set(id, field());
  const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    document: { addEventListener() {}, querySelector: () => null, querySelectorAll: () => [] },
    localStorage: { getItem: () => null },
    t: value => value, tx: value => value, esc: escapeHTML, escAttr: escapeHTML,
    uiIcon: name => '<i class="ic-' + name + '"></i>',
    formatNumber: value => String(value),
    formatTokenCount: value => String(value),
    LC: () => 'en',
    toast() {}, showConfirm: async () => true,
    api: async () => ({ data: {} }),
    Date,
  });
  for (const asset of assets) {
    vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/' + asset, import.meta.url), 'utf8'), context, { filename: asset });
  }
  const run = source => vm.runInContext(source, context);
  const json = source => JSON.parse(vm.runInContext('JSON.stringify(' + source + ')', context));
  const set = (name, value) => run(name + ' = ' + JSON.stringify(value));
  return { run, json, set, context, html: () => elements.get('globalInstances').innerHTML, elements };
}

const custom = {
  id: 'prov_a', name: 'Alpha', baseURL: 'http://localhost:8379/v1', enabled: true, deletable: true, runtimeBuiltin: false,
  freeTier: true, kind: 'other', priority: 10, weight: 1,
};
const builtin = { id: 'cline', name: 'Cline', baseURL: 'https://api.cline.bot/api/v1', enabled: true, deletable: false, runtimeBuiltin: true };
const offering = (providerId, modelId, extra = {}) => ({ providerId, modelId, qualifiedId: providerId + '/' + modelId, upstreamModelId: modelId, ...extra });

function load(ui, { instances = [custom, builtin], offerings = [], managed = [], health = {}, sync = {}, credentials = [] } = {}) {
  ui.set('_globalData', { instances, offerings, credentials, state: [], profiles: [] });
  ui.run('Object.keys(_provCache).forEach(k => delete _provCache[k])');
  managed.forEach(p => ui.run('_provCache[' + JSON.stringify(p.id) + '] = ' + JSON.stringify(p)));
  ui.set('_provHealth', health);
  ui.set('_provSync', sync);
  ui.run('renderGlobalInstances(_globalData)');
  return ui.html();
}
const group = (html, id) => html.split('<tbody ').slice(1).map(g => '<tbody ' + g.slice(0, g.indexOf('</tbody>'))).find(g => g.includes('data-prov="' + id + '"'));

test('one table, one tbody per provider, with the columns that used to be split across the card list', () => {
  const ui = createUI();
  const html = load(ui, { managed: [{ ...custom, connectionIds: ['c1', 'c2'] }] });
  for (const label of ['Provider', '状态', '类型', '凭据', '模型授权', '优先级', '最近检测', 'Base URL', '操作']) {
    assert.ok(html.includes(label), 'header ' + label);
  }
  assert.equal((html.match(/<tbody /g) || []).length, 2);
  assert.equal((html.match(/<table /g) || []).length, 1);
  assert.doesNotMatch(html, /prov-card/);
  const alpha = group(html, 'prov_a');
  assert.match(alpha, />free</);
  assert.match(alpha, /token×2/);
  assert.match(alpha, />10</, 'priority is a column');
});

test('health shows in the status cell for managed providers only; built-ins keep a plain badge', () => {
  const ui = createUI();
  const html = load(ui, { managed: [custom], health: { prov_a: { samples: 4, rate: 1, recent: [1, 1, 1, 1], limits: { rpm: 15 }, rpm: 14 } } });
  assert.match(group(html, 'prov_a'), /prov-health[\s\S]*接近上限/);
  assert.match(group(html, 'prov_a'), /RPM 14\/15/);
  assert.doesNotMatch(group(html, 'cline'), /prov-health/);
});

test('an idle provider is "no samples", not "near limit" (server reports rate 0 without traffic)', () => {
  const ui = createUI();
  const idle = group(load(ui, { managed: [custom], health: { prov_a: { samples: 0, rate: 0, recent: [], limits: {} } } }), 'prov_a');
  assert.match(idle, /tone-idle/);
  assert.match(idle, /暂无样本/);
  assert.doesNotMatch(idle, /接近上限/);
  const failing = group(load(ui, { managed: [custom], health: { prov_a: { samples: 8, rate: 0.25, recent: [0, 0, 1], limits: {} } } }), 'prov_a');
  assert.match(failing, /成功率偏低/);
  const healthy = group(load(ui, { managed: [custom], health: { prov_a: { samples: 8, rate: 1, recent: [1], limits: {} } } }), 'prov_a');
  assert.match(healthy, /tone-ok/);
  const cooling = group(load(ui, { managed: [custom], health: { prov_a: { samples: 2, rate: 1, cooldowns: [{ model: 'm', until: '2999-01-01T00:00:00Z' }], limits: {} } } }), 'prov_a');
  assert.match(cooling, /冷却中/);
});

test('last-check cell carries the sync time, the error icon and the pending-review pill', () => {
  const ui = createUI();
  const now = Date.now();
  const html = load(ui, { managed: [custom], sync: { prov_a: { lastCheckedMs: now - 8 * 60000, newQueued: 40, batchId: 'b1', lastError: 'boom' } } });
  const alpha = group(html, 'prov_a');
  assert.match(alpha, /8 分钟前/);
  assert.match(alpha, /openAutoBatch\('b1'\)/);
  assert.match(alpha, /title="boom"/);
  assert.match(group(html, 'cline'), /prov-row">[\s\S]*<span class="hint-xs">—<\/span>/);
});

test('models count expands to the full list; mapped names show their upstream ID; removed models are marked', () => {
  const ui = createUI();
  const offerings = [
    offering('prov_a', 'plain'),
    offering('prov_a', 'friendly', { upstreamModelId: 'vendor/real' }),
    offering('prov_a', 'gone', { delisted: true }),
    offering('cline', 'x'),
  ];
  let html = load(ui, { offerings });
  assert.match(group(html, 'prov_a'), /data-prov-toggle="prov_a" aria-expanded="false"/);
  assert.doesNotMatch(group(html, 'prov_a'), /prov-expand-row/, 'collapsed by default');
  ui.run('toggleGlobalProviderModels("prov_a")');
  html = ui.html();
  const alpha = group(html, 'prov_a');
  assert.match(alpha, /aria-expanded="true"/);
  assert.match(alpha, /prov-expand-row/);
  assert.match(alpha, /friendly<span class="prov-chip-up"> → vendor\/real<\/span>/);
  assert.match(alpha, /prov-chip-off" title="prov_a\/gone · 已下架"/);
  assert.match(alpha, /http:\/\/localhost:8379\/v1/, 'the full address is in the expanded row');
  assert.doesNotMatch(group(html, 'cline'), /prov-expand-row/, 'other providers stay collapsed');
  ui.run('toggleGlobalProviderModels("prov_a")');
  assert.doesNotMatch(group(ui.html(), 'prov_a'), /prov-expand-row/);
});

test('a provider with no models shows a plain 0 and cannot be expanded', () => {
  const ui = createUI();
  ui.run('_globalExpandedProviders.prov_a = true');
  const alpha = group(load(ui, { offerings: [] }), 'prov_a');
  assert.doesNotMatch(alpha, /prov-models-toggle/);
  assert.doesNotMatch(alpha, /prov-expand-row/);
});

test('quick actions: custom providers get edit, test, re-check, toggle and delete; built-ins get connection settings only', () => {
  const ui = createUI();
  const html = load(ui, { managed: [custom] });
  const alpha = group(html, 'prov_a');
  assert.match(alpha, /openGlobalProviderDetail\(&quot;prov_a&quot;\)/);
  assert.match(alpha, /editProvider\(&quot;prov_a&quot;\)/);
  assert.match(alpha, /testProviderById\(&quot;prov_a&quot;,this\)/);
  assert.match(alpha, /recheckProvider\(&quot;prov_a&quot;,this\)/);
  assert.match(alpha, /toggleProvider\(&quot;prov_a&quot;,false,this\)/, 'an enabled provider offers disable');
  assert.match(alpha, /deleteProvider\(&quot;prov_a&quot;\)/);
  assert.match(alpha, /aria-label="停用"/);
  const cline = group(html, 'cline');
  assert.match(cline, /setupProviderInstance\(&quot;cline&quot;\)/);
  assert.doesNotMatch(cline, /deleteProvider|testProviderById|recheckProvider|toggleProvider/);
  const disabled = group(load(ui, { instances: [{ ...custom, enabled: false }], managed: [{ ...custom, enabled: false }] }), 'prov_a');
  assert.match(disabled, /toggleProvider\(&quot;prov_a&quot;,true,this\)/);
  assert.match(disabled, /aria-label="启用"/);
  assert.doesNotMatch(disabled, /prov-health/, 'a disabled provider shows no health');
});

test('inline test and re-check results live in the provider row group and survive a re-render', () => {
  const ui = createUI();
  ui.run(`_provRowResults['test:prov_a'] = { ok: true, text: '120ms' }; _provRowResults['recheck:prov_a'] = { ok: false, text: 'upstream down' }`);
  const alpha = group(load(ui, { managed: [custom] }), 'prov_a');
  assert.match(alpha, /prov-row-test-out"><span class="save-result ok">[\s\S]*120ms/);
  assert.match(alpha, /prov-row-recheck-out"><span class="save-result err">[\s\S]*upstream down/);
  assert.match(group(ui.html(), 'cline'), /prov-row-test-out"><\/span>/, 'empty slots exist for providers with no result');
});

test('test / re-check handlers write into the row group of the button that was clicked', async () => {
  const ui = createUI();
  const make = () => {
    const out = { innerHTML: '' };
    const host = { querySelector: selector => selector === '.prov-row-test-out' ? out : (selector === '.prov-row-recheck-out' ? out : null) };
    const asked = [];
    return { out, asked, button: { disabled: false, innerHTML: 'orig', closest: selector => { asked.push(selector); return host; } } };
  };
  ui.context.api = async () => ({ data: { ok: true, durationMs: 77 } });

  const test = make();
  ui.context.__button = test.button;
  await ui.run('testProviderById("prov_a", __button)');
  assert.match(test.asked[0], /\[data-prov\]/, 'looks up the row group, not a card or a bare td');
  assert.match(test.out.innerHTML, /77ms/);
  assert.equal(test.button.innerHTML, 'orig', 'button label is restored');
  assert.equal(test.button.disabled, false);

  ui.context.api = async () => ({ data: { lastCheckedMs: 1, newQueued: 3 } });
  ui.context.loadProviders = async () => {};
  ui.context.loadModels = async () => {};
  const recheck = make();
  ui.context.__button = recheck.button;
  await ui.run('recheckProvider("prov_a", __button)');
  assert.match(recheck.asked[0], /\[data-prov\]/);
  assert.match(recheck.out.innerHTML, /\+3/);
  assert.equal(ui.json("_provRowResults['recheck:prov_a'].ok"), true);
});

test('sorting by priority puts unprioritised built-ins last; sorting by models is numeric', () => {
  const ui = createUI();
  const beta = { ...custom, id: 'prov_b', name: 'Beta', priority: 1 };
  load(ui, { instances: [custom, builtin, beta], offerings: [offering('prov_a', 'm1'), offering('prov_a', 'm2'), offering('cline', 'x')] });
  ui.run('setGlobalProviderSort("priority")');
  const order = html => [...html.matchAll(/<tbody class="prov-group" data-prov="([^"]+)"/g)].map(m => m[1]);
  assert.deepEqual(order(ui.html()), ['prov_b', 'prov_a', 'cline']);
  ui.run('setGlobalProviderSort("offerings")');
  assert.deepEqual(order(ui.html()), ['prov_b', 'cline', 'prov_a']);
});

test('hostile provider and model names cannot break out of attributes or inject markup', () => {
  const ui = createUI();
  const evil = { ...custom, name: '<img src=x onerror=alert(1)>', id: 'p"onmouseover="x' };
  const html = load(ui, { instances: [evil], offerings: [offering(evil.id, '"><script>alert(2)</script>', { upstreamModelId: '<b>up</b>' })] });
  ui.run('_globalExpandedProviders[' + JSON.stringify(evil.id) + '] = true; renderGlobalInstances(_globalData)');
  const all = ui.html();
  assert.doesNotMatch(all, /<img/);
  assert.doesNotMatch(all, /<script>/);
  assert.doesNotMatch(all, /<b>up/);
  assert.doesNotMatch(all, / onmouseover="x/);
  assert.ok(html.length > 0);
});

test('the compact (narrow) list carries the same data, actions and result slots', () => {
  const ui = createUI();
  const html = load(ui, { managed: [custom], offerings: [offering('prov_a', 'm1')], health: { prov_a: { samples: 2, rate: 1, recent: [1, 1], limits: {} } } });
  const compact = html.slice(html.indexOf('provider-compact-list'));
  assert.match(compact, /<article class="provider-compact-item" data-prov="prov_a"/);
  assert.match(compact, /testProviderById/);
  assert.match(compact, /pc-result/);
  assert.match(compact, /data-prov-toggle="prov_a"/);
  assert.match(compact, /pmini/);
});

test('search, kind and status filters still apply to the unified rows', () => {
  const ui = createUI();
  load(ui, { managed: [custom] });
  ui.run('setGlobalProviderSearch("cline")');
  assert.deepEqual([...ui.html().matchAll(/<tbody class="prov-group" data-prov="([^"]+)"/g)].map(m => m[1]), ['cline']);
  ui.run('setGlobalProviderSearch(""); setGlobalProviderStatus("disabled")');
  assert.match(ui.html(), /暂无匹配 Provider/);
});

test('the legacy "custom provider management" block is gone; its banner and cooldowns sit above the one table', () => {
  const html = page('html/pages/providers.html');
  assert.doesNotMatch(html, /provider-advanced-details|providersList|自定义 Provider 高级管理|loadProviderPresets/);
  const order = ['id="providerSyncBanner"', 'id="providerCooldowns"', 'id="globalProviderSearch"', 'id="globalInstances"'].map(needle => html.indexOf(needle));
  assert.ok(order.every(i => i > 0), 'all four anchors exist');
  assert.deepEqual([...order].sort((a, b) => a - b), order, 'banner and cooldowns come before the toolbar and the table');
  assert.equal((html.match(/id="globalInstances"/g) || []).length, 1);
});

// ---- API Credentials table (same renderer module, Providers & Credentials inventories) ----
function renderCredentials(ui, credentials, instances = [custom, builtin]) {
  ui.elements.set('globalCredentials', { innerHTML: '' });
  ui.set('_globalFilter', '');
  ui.set('_globalData', { instances, credentials, offerings: [], state: [], profiles: [] }); // provider names resolve from the loaded data
  ui.run('renderGlobalCredentials(_globalData)');
  return ui.elements.get('globalCredentials').innerHTML;
}
const apiCredential = (extra = {}) => ({ id: 'api-key:prov_a', subject: 'api-key:prov_a', kind: 'api_key', providerIds: ['prov_a'], status: 'active', usageCount: 3, totalTokens: 10, ...extra });

test('API credentials table leads with Provider, then Credential, and every header sits above its own cell', () => {
  const ui = createUI();
  const html = renderCredentials(ui, [apiCredential({ id: 'cred-1', subject: 'work key', providerIds: ['prov_a', 'cline'] })]);
  const table = html.slice(0, html.indexOf('credential-compact-list'));
  const headers = [...table.matchAll(/<th[^>]*>([^<]*)<\/th>/g)].map(m => m[1]);
  assert.deepEqual(headers.slice(0, 2), ['Provider', '凭据'], 'Provider first, Credentials second');
  assert.equal(headers.length, 8);

  const cells = [...table.slice(table.indexOf('<tbody>')).matchAll(/<td[^>]*>([\s\S]*?)<\/td>/g)].map(m => m[1]);
  assert.equal(cells.length, headers.length, 'one cell per header');
  assert.match(cells[0], /Alpha · Cline/, 'first cell is the provider(s)');
  assert.doesNotMatch(cells[0], /work key|cred-1/);
  assert.match(cells[1], /work key[\s\S]*cred-1/, 'second cell is the credential name and id');
  assert.doesNotMatch(cells[1], /Alpha/);
});

test('mobile credential list also leads with the provider, and hostile names stay escaped', () => {
  const ui = createUI();
  const html = renderCredentials(ui, [apiCredential({ id: 'c"1', subject: '<img src=x onerror=alert(1)>', providerIds: ['prov_a'] })]);
  const compact = html.slice(html.indexOf('credential-compact-list'));
  assert.match(compact, /pc-line1"><strong>Alpha<\/strong>/, 'provider is the card title');
  assert.match(compact, /pc-line2"><span>&lt;img src=x onerror=alert\(1\)&gt;<\/span>/, 'credential is the second line, escaped');
  assert.doesNotMatch(html, /<img/);
});

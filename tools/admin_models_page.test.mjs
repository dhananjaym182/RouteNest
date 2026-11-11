// Run with: node --test tools/admin_models_page.test.mjs
// Models page (#models): source-membership grouping, lazy and paged rows, in-place toggles,
// persisted filters, test-result expiry, keyboard/screen-reader markup, "sync all connected"
// and the offerings table paging. Loads the real shipped scripts into a VM; no browser.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const asset = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');
const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const instances = [
  { id: 'cline', name: 'Cline', enabled: true, runtimeBuiltin: true },
  { id: 'opencode', name: 'OpenCode Zen', enabled: true, runtimeBuiltin: true },
  { id: 'acme', name: 'Acme', enabled: true },
  { id: 'beta', name: 'Beta', enabled: true },
  { id: 'off', name: 'Switched off', enabled: false },
];
const baseModels = () => [
  { id: 'shared', source: 'remote', cost: 'free' },
  { id: 'shared', source: 'provider', provider: 'Acme', cost: 'free' },
  { id: 'cline-only', source: 'remote', cost: 'free' },
  { id: 'zen-only', source: 'zen', provider: 'opencode', cost: 'free' },
  { id: 'acme-only', source: 'provider', provider: 'Acme', cost: 'pass' },
  { id: 'beta-1', source: 'provider', provider: 'Beta', cost: 'free' },
];

function createUI({
  models = baseModels(), providers = instances, store = {}, canWrite = true, toggleError = '', recheck = {},
  syncErrors = {}, offerings = [],
} = {}) {
  const elements = new Map();
  const field = (extra = {}) => ({
    innerHTML: '', textContent: '', value: '', hidden: false, disabled: false, style: { display: 'none' }, dataset: {},
    classList: { toggle() {}, contains: () => false }, focus() { this.focused = true; },
    setAttribute(name, value) { (this.attrs ||= {})[name] = value; },
    querySelector: () => null, querySelectorAll: () => [], ...extra,
  });
  for (const id of ['modelsList', 'modelStatsBar', 'modelFilterProvider', 'modelFilterStatus', 'modelFilterCost', 'modelSearch',
    'modelClearFilters', 'syncAllModelsBtn', 'modelSyncSummary', 'modelCatalogView', 'modelSyncOverlay', 'modelSyncModal',
    'globalOfferings', 'globalOfferingsToggle', 'globalOfferingSearch', 'globalOfferingAccessFilter']) elements.set(id, field());
  const calls = [], toasts = [], sticky = [], timers = [], configLoads = [], subtabs = [], listeners = {};
  const gates = {};
  const storage = { ...store };
  let offeringRows = structuredClone(offerings);
  const context = vm.createContext({
    TextEncoder, Date, structuredClone,
    _: id => elements.get(id) || null,
    t: value => value, tx: value => value, esc: escapeHTML, escAttr: escapeHTML, uiIcon: () => '',
    listEmptyState: (_icon, message, action) => message + '|' + (action || ''), ICON_MODELS: '',
    formatTokenCount: value => String(value), formatNumber: value => String(value), formatDateTime: value => String(value || ''),
    fmtAgo: ms => 'ago:' + ms, _tierSignals: {},
    renderTestResultHTML: (ok, text) => '<span class="save-result ' + (ok ? 'ok' : 'err') + '">' + escapeHTML(text) + '</span>',
    document: { addEventListener: (type, fn) => { (listeners[type] ||= []).push(fn); }, querySelectorAll: () => [], activeElement: null },
    localStorage: { getItem: key => (key in storage ? storage[key] : null), setItem: (key, value) => { storage[key] = String(value); } },
    setTimeout: fn => { timers.push(fn); return timers.length; }, clearTimeout: id => { if (id) timers[id - 1] = null; },
    toast: (message, tone) => toasts.push({ message, tone }), toastError: message => sticky.push(message),
    adminHasPermission: () => canWrite, openAutoBatch: () => {}, openProviderSetup: () => {},
    switchPageSubTab: (...args) => subtabs.push(args),
    showConfirm: async () => true,
    api: async (method, path, body) => {
      calls.push({ method, path, body: body ? JSON.parse(JSON.stringify(body)) : undefined });
      if (gates[path]) await gates[path]();
      if (path === '/models/toggle') { if (toggleError) throw new Error(toggleError); return { data: {} }; }
      if (path === '/provider-instances') return { data: { instances: providers } };
      if (path === '/models/sync') { if (syncErrors.cline) throw new Error(syncErrors.cline); return { data: { changed: false, added: [], removed: [] } }; }
      if (path === '/opencode/models/sync') { if (syncErrors.zen) throw new Error(syncErrors.zen); return { data: { changed: false, added: [], removed: [] } }; }
      if (path === '/providers/recheck') { if (syncErrors[body.id]) throw new Error(syncErrors[body.id]); return { data: recheck[body.id] || { lastCheckedMs: 1 } }; }
      throw new Error('Unexpected request: ' + method + ' ' + path);
    },
  });
  for (const name of ['13_models.js', '13_model_sync.js', '13_model_provider_actions.js', '18_global.js']) {
    vm.runInContext(asset('js/' + name), context, { filename: name });
  }
  Object.assign(context, {
    loadConfig: async () => configLoads.push(true),
    loadGlobal: async () => { calls.push({ method: 'LOAD', path: 'loadGlobal' }); },
    loadModels: async () => { calls.push({ method: 'LOAD', path: 'loadModels' }); },
  });
  const run = source => vm.runInContext(source, context);
  // 18_global.js declares `let _globalData = null`, so seed the data after the scripts have loaded.
  run('_cachedModels = ' + JSON.stringify(models) + '; _globalData = { instances: ' + JSON.stringify(providers) + ', offerings: ' + JSON.stringify(offeringRows) + ' };');
  const html = () => elements.get('modelsList').innerHTML;
  const groups = () => html().split('<div class="model-group model-group-src">').slice(1).map(chunk => ({
    key: (chunk.match(/data-key="([^"]*)"/) || [])[1],
    count: (chunk.match(/class="model-group-count"[^>]*>([^<]*)</) || [])[1],
    rows: [...chunk.matchAll(/class="model-row[^"]*" data-model-id="([^"]*)"/g)].map(m => m[1]),
    rendered: (chunk.match(/data-rendered="(\d)"/) || [])[1],
  }));
  const openAll = () => run("['src:cline','src:zen','src:prov:Acme','src:prov:Beta'].forEach(k => { _modelGroupOpen[k] = true; })");
  return { run, context, elements, calls, toasts, sticky, timers, configLoads, subtabs, gates, storage, html, groups, openAll, listeners,
    posts: () => calls.filter(c => c.method === 'POST').map(c => c.path + (c.body && c.body.id ? ':' + c.body.id : '')) };
}

// ---------- grouping by source membership ----------
test('a model offered by several sources is listed under every one of them', () => {
  const ui = createUI();
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(modelSourceKeys(mergeModelsById(_cachedModels).find(m => m.id === "shared")))')).sort(), ['src:cline', 'src:prov:Acme']);
  ui.openAll();
  ui.run('renderModelList()');
  const byKey = Object.fromEntries(ui.groups().map(g => [g.key, g]));
  assert.deepEqual(byKey['src:cline'].rows.sort(), ['cline-only', 'shared']);
  assert.deepEqual(byKey['src:prov:Acme'].rows.sort(), ['acme-only', 'shared']);
  assert.equal(byKey['src:cline'].count, '2/2');
  assert.equal(byKey['src:prov:Acme'].count, '2/2');
  assert.match(ui.elements.get('modelStatsBar').innerHTML, /共<\/span>|共 <b>5<\/b>/, 'the total still counts each model once');
});

test('the source filter matches every source of a model and shows the matching group only', () => {
  const ui = createUI();
  ui.run('onModelFilterChange("provider", "src:cline")');
  const groups = ui.groups();
  assert.deepEqual(groups.map(g => g.key), ['src:cline']);
  assert.deepEqual(groups[0].rows.sort(), ['cline-only', 'shared'], 'a model that is also at a provider still appears under Cline');
  assert.equal(groups[0].rendered, '1', 'the selected group opens by itself');
  assert.match(ui.elements.get('modelFilterProvider').innerHTML, /Cline \(2\)/);
  assert.match(ui.elements.get('modelFilterProvider').innerHTML, /Acme \(2\)/);
});

test('group bulk actions cover every model the group lists', () => {
  const ui = createUI();
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(mergedRowsForGroupKey("src:cline").map(m => m.id).sort())')), ['cline-only', 'shared']);
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(mergedRowsForGroupKey("src:prov:Acme").map(m => m.id).sort())')), ['acme-only', 'shared']);
});

test('a saved source filter whose provider is gone is cleared instead of hiding every model', () => {
  const ui = createUI({ store: { cline_admin_model_filters: JSON.stringify({ provider: 'src:prov:Gone' }) } });
  ui.run('renderModelList()');
  assert.equal(ui.run('_modelFilters.provider'), '');
  assert.ok(ui.groups().length > 0);
});

// ---------- lazy and paged rows ----------
test('collapsed groups build no rows; expanding builds them once and keeps aria-expanded in sync', () => {
  const ui = createUI();
  ui.run('renderModelList()');
  assert.ok(ui.groups().every(g => g.rendered === '0' && g.rows.length === 0), 'no row markup for collapsed groups');
  assert.ok(!ui.html().includes('data-model-id'));
  const body = { dataset: { rendered: '0' }, classList: { contains: c => c === 'model-group-body' }, style: {}, innerHTML: '' };
  const toggle = { attrs: {}, setAttribute(n, v) { this.attrs[n] = v; } };
  const head = { dataset: { key: 'src:cline' }, classList: { toggle() {}, contains: () => false }, nextElementSibling: body, querySelector: () => toggle };
  ui.run('toggleModelGroupEl').call(null, head);
  assert.equal(body.dataset.rendered, '1');
  assert.match(body.innerHTML, /data-model-id="cline-only"/);
  assert.equal(toggle.attrs['aria-expanded'], 'true');
  assert.equal(body.style.display, 'block');
  const built = body.innerHTML;
  head.classList.contains = () => true;
  ui.run('toggleModelGroupEl').call(null, head);
  assert.equal(toggle.attrs['aria-expanded'], 'false');
  assert.equal(body.style.display, 'none');
  assert.equal(body.innerHTML, built, 'collapse keeps the built rows');
  assert.equal(JSON.parse(ui.storage.cline_admin_model_groups_open)['src:cline'], false, 'the open state is saved when no filter is active');
});

test('a very large group is built a page at a time', () => {
  const many = Array.from({ length: 130 }, (_, i) => ({ id: 'bulk-' + String(i).padStart(3, '0'), source: 'provider', provider: 'Acme', cost: 'free' }));
  const ui = createUI({ models: many });
  ui.run('_modelGroupOpen["src:prov:Acme"] = true; renderModelList()');
  const first = ui.groups()[0];
  assert.equal(first.rows.length, 120);
  assert.match(ui.html(), /onclick="showMoreModelRows\(this\)">显示更多 \(10\)/);
  const body = { dataset: { key: 'src:prov:Acme' }, innerHTML: '', querySelectorAll: () => [] };
  ui.run('showMoreModelRows').call(null, { closest: () => body });
  assert.equal([...body.innerHTML.matchAll(/data-model-id=/g)].length, 130);
  assert.ok(!body.innerHTML.includes('showMoreModelRows'));
});

test('typing in the search box redraws once, after the pause, not per keystroke', () => {
  const ui = createUI();
  let renders = 0;
  ui.context.renderModelList = () => { renders++; };
  ui.run('onModelFilterChange("search", "s")');
  ui.run('onModelFilterChange("search", "sh")');
  ui.run('onModelFilterChange("search", "sha")');
  assert.equal(renders, 0);
  ui.timers.filter(Boolean).forEach(fn => fn());
  assert.equal(renders, 1);
  assert.equal(ui.run('_modelFilters.search'), 'sha');
});

// ---------- in-place toggles ----------
test('toggling a model updates the list in place with one request and no refetch', async () => {
  const ui = createUI();
  ui.openAll();
  await ui.run('toggleModel("cline-only", true)');
  assert.deepEqual(ui.calls.map(c => c.path), ['/models/toggle']);
  assert.deepEqual(ui.calls[0].body, { ids: ['cline-only'], disabled: true });
  assert.equal(ui.run('_cachedModels.find(m => m.id === "cline-only").disabled'), true);
  assert.match(ui.html(), /class="model-row disabled-row" data-model-id="cline-only"/);
  assert.equal(ui.groups().find(g => g.key === 'src:cline').count, '1/2', 'the group count follows immediately');
  assert.equal(ui.toasts.at(-1).tone, 'success');
});

test('a failed toggle is rolled back and reported', async () => {
  const ui = createUI({ toggleError: 'save failed' });
  ui.openAll();
  await ui.run('toggleModel("cline-only", true)');
  assert.equal(ui.run('_cachedModels.find(m => m.id === "cline-only").disabled'), false);
  assert.doesNotMatch(ui.html(), /disabled-row" data-model-id="cline-only"/);
  assert.equal(ui.toasts.at(-1).tone, 'error');
  assert.deepEqual(ui.calls.map(c => c.path), ['/models/toggle']);
});

test('rapid toggles are written in click order, one at a time', async () => {
  const ui = createUI();
  let release;
  ui.gates['/models/toggle'] = () => new Promise(resolve => { release = resolve; });
  const first = ui.run('toggleModel("cline-only", true)');
  const second = ui.run('toggleModel("cline-only", false)');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ui.calls.length, 1, 'the second write waits for the first');
  ui.gates['/models/toggle'] = null;
  release();
  await first; await second;
  assert.deepEqual(ui.calls.map(c => c.body.disabled), [true, false]);
});

test('the default-model setting is re-read after a successful toggle only', async () => {
  const ok = createUI();
  ok.elements.set('settingDefModel', {});
  await ok.run('toggleModel("cline-only", true)');
  assert.equal(ok.configLoads.length, 1);
  const bad = createUI({ toggleError: 'nope' });
  bad.elements.set('settingDefModel', {});
  await bad.run('toggleModel("cline-only", true)');
  assert.equal(bad.configLoads.length, 0);
});

test('keyboard focus returns to the same control after the list is redrawn', async () => {
  const ui = createUI();
  ui.openAll();
  ui.run('renderModelList()');
  const target = { focus() { this.focused = true; } };
  const row = { dataset: { modelId: 'cline-only' } };
  const body = { dataset: { key: 'src:cline' } };
  ui.context.document.activeElement = {
    dataset: { focus: 'switch' },
    closest: selector => ({ '#modelsList': {}, '.model-row': row, '.model-group-body': body }[selector] || null),
  };
  ui.context.document.querySelectorAll = () => [{
    dataset: { key: 'src:cline' },
    querySelectorAll: () => [{ dataset: { modelId: 'cline-only' }, querySelector: sel => (sel === '[data-focus="switch"]' ? target : null) }],
  }];
  await ui.run('toggleModel("cline-only", true)');
  assert.equal(target.focused, true);
});

// ---------- filters ----------
test('source, cost, status and sort are saved and restored; the search text is not', () => {
  const first = createUI();
  first.run('_modelFilters.search = "typed"; onModelFilterChange("cost", "free"); onModelFilterChange("status", "disabled"); setModelSort("ctx")');
  const saved = JSON.parse(first.storage.cline_admin_model_filters);
  assert.deepEqual(saved, { provider: '', cost: 'free', status: 'disabled', sort: { key: 'ctx', dir: 1 } });
  const second = createUI({ store: { cline_admin_model_filters: first.storage.cline_admin_model_filters } });
  assert.equal(second.run('_modelFilters.cost'), 'free');
  assert.equal(second.run('_modelFilters.status'), 'disabled');
  assert.equal(second.run('_modelFilters.search'), '');
  assert.equal(second.run('_modelSort.key'), 'ctx');
});

test('corrupt saved filters fall back to defaults', () => {
  const ui = createUI({ store: { cline_admin_model_filters: JSON.stringify({ cost: 'bogus', status: 'x', sort: { key: 'nope', dir: 9 } }) } });
  assert.equal(ui.run('_modelFilters.cost'), '');
  assert.equal(ui.run('_modelFilters.status'), '');
  assert.equal(ui.run('_modelSort.key'), 'id');
});

test('the stats are quick filters, shown pressed, and a second click clears them', () => {
  const ui = createUI();
  ui.run('mergeModelsById(_cachedModels)');
  ui.run('renderModelStats(mergeModelsById(_cachedModels))');
  assert.match(ui.elements.get('modelStatsBar').innerHTML, /onclick="setModelStatusFilter\('disabled'\)"/);
  ui.run('setModelStatusFilter("disabled")');
  assert.equal(ui.run('_modelFilters.status'), 'disabled');
  assert.match(ui.elements.get('modelStatsBar').innerHTML, /model-stat active" aria-pressed="true"[^>]*>[\s\S]*?已停用/);
  ui.run('setModelStatusFilter("disabled")');
  assert.equal(ui.run('_modelFilters.status'), '');
});

test('clear-filters shows only while a filter is active and resets everything', () => {
  const ui = createUI();
  ui.run('renderModelList()');
  assert.equal(ui.elements.get('modelClearFilters').hidden, true);
  ui.run('onModelFilterChange("cost", "pass")');
  assert.equal(ui.elements.get('modelClearFilters').hidden, false);
  ui.run('clearModelFilters()');
  assert.equal(ui.run('modelFilterActive()'), false);
  assert.equal(ui.elements.get('modelClearFilters').hidden, true);
  assert.equal(JSON.parse(ui.storage.cline_admin_model_filters).cost, '');
});

test('with a filter active matching groups open; collapsing one sticks for that filter only', () => {
  const ui = createUI();
  ui.run('onModelFilterChange("cost", "free")');
  assert.ok(ui.groups().every(g => g.rendered === '1'));
  ui.run('_modelFilterOpen["src:zen"] = false; renderModelList()');
  assert.equal(ui.groups().find(g => g.key === 'src:zen').rendered, '0');
  ui.run('onModelFilterChange("cost", "")');
  assert.ok(ui.groups().every(g => g.rendered === '0'), 'back to the saved (collapsed) state');
});

test('empty states point to the next step', () => {
  const none = createUI({ models: [] });
  none.run('renderModelList()');
  assert.match(none.html(), /暂无模型\|.*openProviderSetup\(\).*连接 Provider.*syncAllModels\(\)/);
  assert.ok(!none.html().includes('从 Cline'), 'Cline is no longer the only suggested action');
  const filtered = createUI();
  filtered.run('onModelFilterChange("status", "failed")');
  assert.match(filtered.html(), /无匹配模型\|.*clearModelFilters\(\)/);
});

// ---------- test results ----------
test('saved test results expire after 7 days; results without a timestamp start their clock now', () => {
  const day = 24 * 3600 * 1000;
  const store = { cline_admin_model_tests: JSON.stringify({
    old: { ok: false, text: 'x', at: Date.now() - 8 * day },
    fresh: { ok: false, text: 'y', at: Date.now() - day },
    legacy: { ok: false, text: 'z' },
    junk: 5,
  }) };
  const ui = createUI({ store });
  assert.deepEqual(Object.keys(JSON.parse(ui.run('JSON.stringify(_modelTestResults)'))).sort(), ['fresh', 'legacy']);
  assert.ok(ui.run('_modelTestResults.legacy.at') > Date.now() - 5000);
  assert.deepEqual(Object.keys(JSON.parse(ui.storage.cline_admin_model_tests)).sort(), ['fresh', 'legacy'], 'the cleaned list is saved back');
});

test('a failed test is a button that names the model and the reason', () => {
  const ui = createUI();
  const html = ui.run('modelTestCellHTML("m<1>", { ok: false, text: "HTTP 401", at: 5 })');
  assert.match(html, /<button type="button" class="model-test-fail"/);
  assert.match(html, /aria-label="测试失败: m&lt;1&gt; · HTTP 401"/);
  assert.match(html, /title="HTTP 401 · 测试于 ago:5"/);
  assert.match(html, /showModelTestDetail\(&quot;m&lt;1&gt;&quot;\)/);
  assert.doesNotMatch(ui.run('modelTestCellHTML("m", { ok: true, text: "fine", at: 5 })'), /<button/);
  ui.run('_modelTestResults.m = { ok: false, text: "boom", at: 1 }; showModelTestDetail("m")');
  assert.deepEqual(ui.sticky, ['m: boom']);
});

// ---------- accessible markup ----------
test('group headers are real buttons and every row control names its model', () => {
  const ui = createUI();
  ui.openAll();
  ui.run('renderModelList()');
  const html = ui.html();
  const toggle = html.match(/<button type="button" class="model-group-toggle"[\s\S]*?<\/button>/)[0];
  assert.match(toggle, /aria-expanded="true"/);
  assert.match(toggle, /aria-label="Cline, 2\/2 已启用"/);
  assert.doesNotMatch(toggle, /model-group-actions|<button[\s\S]*<button/, 'actions sit beside the toggle, not inside it');
  assert.match(html, /<input type="checkbox" data-focus="switch" aria-label="启用模型: cline-only"/);
  assert.match(html, /data-focus="test"[^>]*aria-label="测试: cline-only"/);
  assert.match(html, /data-focus="edit"[^>]*aria-label="编辑上下文与输出: cline-only"/);
  const row = html.match(/<div class="model-row[^"]*" data-model-id="shared">[\s\S]*?<\/div><\/div>/)[0];
  assert.doesNotMatch(row, /aria-label="测试"/);
});

test('a model with provider offerings links to the offerings tab', () => {
  const ui = createUI({ offerings: [{ modelId: 'shared', providerId: 'acme', providerName: 'Acme', qualifiedId: 'ac/shared' }] });
  ui.openAll();
  ui.run('renderModelList()');
  assert.match(ui.html(), /class="model-link-btn" data-focus="offerings" onclick="showModelOfferings\(&quot;shared&quot;\)"/);
  assert.equal([...ui.html().matchAll(/model-link-btn/g)].length, 2, 'only the model that has offerings, once per group listing it');
  ui.run('showModelOfferings("shared")');
  assert.deepEqual(ui.subtabs.at(-1), ['modelviews', 'offerings']);
  assert.equal(ui.run('_globalOfferingSearch'), 'shared');
  assert.equal(ui.elements.get('globalOfferingSearch').value, 'shared');
});

// ---------- sync ----------
test('sync all runs Cline, an enabled OpenCode Zen and every enabled provider, skipping disabled ones', async () => {
  const ui = createUI();
  await ui.run('syncAllModels()');
  assert.deepEqual(ui.posts(), ['/models/sync', '/opencode/models/sync', '/providers/recheck:acme', '/providers/recheck:beta']);
  assert.deepEqual(ui.calls.slice(-2).map(c => c.path), ['loadGlobal', 'loadModels'], 'offerings and catalogue are refreshed once at the end');
  assert.match(ui.elements.get('modelSyncSummary').innerHTML, /Cline[\s\S]*OpenCode Zen[\s\S]*Acme[\s\S]*Beta/);
  assert.equal(ui.elements.get('modelSyncSummary').hidden, false);
  assert.equal(ui.toasts.at(-1).tone, 'success');
});

test('Zen is left out when it is not enabled', async () => {
  const ui = createUI({ providers: instances.map(p => (p.id === 'opencode' ? { ...p, enabled: false } : p)) });
  await ui.run('syncAllModels()');
  assert.ok(!ui.posts().includes('/opencode/models/sync'));
});

test('one failing source does not stop the others and is listed with its reason', async () => {
  const ui = createUI({ syncErrors: { acme: 'upstream 500' } });
  await ui.run('syncAllModels()');
  assert.deepEqual(ui.posts(), ['/models/sync', '/opencode/models/sync', '/providers/recheck:acme', '/providers/recheck:beta']);
  const summary = ui.elements.get('modelSyncSummary').innerHTML;
  assert.match(summary, /sync-chip err[^>]*>[\s\S]*Acme: upstream 500/);
  assert.match(summary, /sync-chip ok[^>]*>[\s\S]*Beta/);
  assert.equal(ui.toasts.at(-1).tone, 'error');
  assert.match(ui.toasts.at(-1).message, /1\/4/);
});

test('a provider check that never completed is reported as a failure', async () => {
  const ui = createUI({ recheck: { acme: { lastError: 'dns failure' } } });
  await ui.run('syncAllModels()');
  assert.match(ui.elements.get('modelSyncSummary').innerHTML, /Acme: dns failure/);
});

test('new models waiting for review are announced with a link to the review batch', async () => {
  const ui = createUI({ recheck: { acme: { lastCheckedMs: 1, newQueued: 3, batchId: 'b-1' } } });
  await ui.run('syncAllModels()');
  assert.match(ui.elements.get('modelSyncSummary').innerHTML, /sync-chip-link" onclick="openAutoBatch\(&quot;b-1&quot;\)"[\s\S]*3 个新模型待审/);
});

test('a second click while a sync runs does nothing', async () => {
  const ui = createUI();
  let release;
  ui.gates['/models/sync'] = () => new Promise(resolve => { release = resolve; });
  const first = ui.run('syncAllModels()');
  await new Promise(resolve => setImmediate(resolve));
  await ui.run('syncAllModels()');
  ui.gates['/models/sync'] = null;
  release();
  await first;
  assert.equal(ui.posts().filter(p => p === '/models/sync').length, 1);
});

test('buttons are disabled with progress during a sync and restored afterwards', async () => {
  const ui = createUI();
  const button = ui.elements.get('syncAllModelsBtn');
  button.innerHTML = '同步全部模型';
  const seen = [];
  ui.context.document.querySelectorAll = selector => (selector.includes('data-model-sync') ? [button] : []);
  ui.gates['/providers/recheck'] = async () => { seen.push({ disabled: button.disabled, html: button.innerHTML }); };
  await ui.run('syncAllModels()');
  assert.ok(seen.length === 2 && seen.every(s => s.disabled && /同步中\.\.\. \d\/4/.test(s.html)), JSON.stringify(seen));
  assert.equal(button.disabled, false);
  assert.equal(button.innerHTML, '同步全部模型');
});

test('a group button syncs only that group', async () => {
  const head = key => ({ closest: () => ({ dataset: { key } }) });
  const cline = createUI();
  await cline.run('syncModelGroup').call(null, head('src:cline'));
  assert.deepEqual(cline.posts(), ['/models/sync']);
  const acme = createUI();
  await acme.run('syncModelGroup').call(null, head('src:prov:Acme'));
  assert.deepEqual(acme.posts(), ['/providers/recheck:acme']);
  const zen = createUI();
  await zen.run('syncModelGroup').call(null, head('src:zen'));
  assert.deepEqual(zen.posts(), ['/opencode/models/sync']);
});

test('sync controls are hidden from users who cannot write providers', async () => {
  const ui = createUI({ canWrite: false });
  ui.run('renderModelList()');
  assert.equal(ui.elements.get('syncAllModelsBtn').hidden, true);
  assert.doesNotMatch(ui.html(), /data-model-sync/);
  await ui.run('syncAllModels()');
  assert.deepEqual(ui.calls, []);
  const writer = createUI();
  writer.run('renderModelList()');
  assert.equal(writer.elements.get('syncAllModelsBtn').hidden, false);
  assert.match(writer.html(), /data-model-sync="src:cline"/);
  assert.match(writer.html(), /data-model-sync="src:prov:Acme"/);
  assert.doesNotMatch(writer.html(), /data-model-sync="src:custom"/);
});

// ---------- offerings table ----------
const manyOfferings = count => Array.from({ length: count }, (_, i) => ({
  modelId: 'model-' + String(i).padStart(4, '0'), providerId: 'acme', providerName: 'Acme', access: 'free', status: 'active',
}));
const offeringRows = ui => [...ui.elements.get('globalOfferings').innerHTML.matchAll(/<tr><td/g)].length;

test('offerings: a short preview, then pages of 200 with a counter', () => {
  const ui = createUI({ offerings: manyOfferings(450) });
  ui.run('renderGlobalOfferings(_globalData)');
  assert.equal(offeringRows(ui), 12);
  ui.run('toggleGlobalOfferings()');
  assert.equal(offeringRows(ui), 200);
  assert.match(ui.elements.get('globalOfferings').innerHTML, /已显示 200 \/ 450/);
  ui.run('showMoreGlobalOfferings()');
  assert.equal(offeringRows(ui), 400);
  ui.run('showMoreGlobalOfferings()');
  assert.equal(offeringRows(ui), 450);
  assert.doesNotMatch(ui.elements.get('globalOfferings').innerHTML, /showMoreGlobalOfferings/);
  ui.run('toggleGlobalOfferings()');
  assert.equal(offeringRows(ui), 12, 'collapsing returns to the preview');
  ui.run('toggleGlobalOfferings()');
  assert.equal(offeringRows(ui), 200, 'and re-expanding starts from the first page again');
});

test('offerings: the table region is keyboard-scrollable and the search is debounced', () => {
  const ui = createUI({ offerings: manyOfferings(30) });
  ui.run('renderGlobalOfferings(_globalData)');
  assert.match(ui.elements.get('globalOfferings').innerHTML, /class="offerings-table-wrap" tabindex="0" role="region" aria-label="Provider 模型授权"/);
  let renders = 0;
  ui.context.renderGlobalOfferings = () => { renders++; };
  ui.run('setGlobalOfferingSearch("a"); setGlobalOfferingSearch("ab")');
  assert.equal(renders, 0);
  ui.timers.filter(Boolean).forEach(fn => fn());
  assert.equal(renders, 1);
});

// ---------- static markup ----------
test('the page has one sync-all button and a result strip, and no leftover per-source buttons or duplicate note', () => {
  const html = asset('html/pages/models.html');
  assert.match(html, /<button type="button" class="btn btn-sm btn-primary" id="syncAllModelsBtn" onclick="syncAllModels\(\)"/);
  assert.match(html, /<div id="modelSyncSummary" class="model-sync-summary" role="status" aria-live="polite" hidden>/);
  assert.match(html, /<button type="button" class="btn btn-sm" id="modelClearFilters" onclick="clearModelFilters\(\)" hidden>/);
  assert.doesNotMatch(html, /syncModelsBtn|syncOcModelsBtn|model-sync-actions|model-name-note/);
  assert.equal([...html.matchAll(/点击模型名即可复制/g)].length, 2, 'the copy hint is part of each description, not a separate note');
  assert.ok(html.indexOf('syncAllModelsBtn') < html.indexOf('id="modelsList"'));
});

test('the offerings header stays visible while the table scrolls', () => {
  const css = asset('css/40_pages_models.css');
  assert.match(css, /#tab-models \.offerings-table-wrap\{max-height:/);
  assert.match(css, /#tab-models \.offerings-table thead th\{position:sticky;top:0;/);
});

// ---------- aliases are specific to the provider group they are shown in ----------
const nvidiaID = 'nvidia/nemotron-3.5-lightning-free';
const aliasFixture = () => createUI({
  models: [
    { id: nvidiaID, source: 'remote', cost: 'free' },
    { id: nvidiaID, source: 'provider', provider: 'Acme', cost: 'free' },
    { id: nvidiaID, source: 'provider', provider: 'Beta', cost: 'free' },
  ],
  offerings: [
    { modelId: nvidiaID, providerId: 'acme', providerName: 'Acme', qualifiedId: 'as/' + nvidiaID },
    { modelId: nvidiaID, providerId: 'cline', providerName: 'Cline', qualifiedId: 'cl/' + nvidiaID },
    { modelId: nvidiaID, providerId: 'beta', providerName: 'Beta', qualifiedId: 'nv/' + nvidiaID },
  ],
});
const aliasesIn = (ui, groupKey) => {
  const chunk = ui.html().split('<div class="model-group model-group-src">').slice(1).find(c => c.includes('data-key="' + groupKey + '"'));
  return [...chunk.matchAll(/class="model-provider-prefix">([^<]*)</g)].map(m => m[1]);
};

test('each provider group lists only that provider\'s own aliases of a shared model', () => {
  const ui = aliasFixture();
  ui.openAll();
  ui.run('renderModelList()');
  assert.deepEqual(aliasesIn(ui, 'src:prov:Acme'), ['as/']);
  assert.deepEqual(aliasesIn(ui, 'src:cline'), ['cl/']);
  assert.deepEqual(aliasesIn(ui, 'src:prov:Beta'), ['nv/']);
  const acme = ui.html().split('<div class="model-group model-group-src">').find(c => c.includes('data-key="src:prov:Acme"'));
  assert.doesNotMatch(acme, /cl\/nvidia|nv\/nvidia/, 'no alias from another provider leaks into this group');
  assert.doesNotMatch(acme, /has-many-provider-names/, 'one alias, so no multi-name layout');
});

test('the merged view (no group) still lists every provider\'s alias', () => {
  const ui = aliasFixture();
  const html = ui.run('renderModelRow(mergeModelsById(_cachedModels)[0])');
  assert.deepEqual([...html.matchAll(/class="model-provider-prefix">([^<]*)</g)].map(m => m[1]).sort(), ['as/', 'cl/', 'nv/']);
  assert.match(html, /has-many-provider-names/);
});

test('a group that cannot be resolved to providers falls back to all aliases instead of hiding them', () => {
  const ui = aliasFixture();
  ui.run('_globalData.instances = []');
  assert.equal(ui.run('modelGroupProviderIDs("src:prov:Acme")'), null);
  ui.openAll();
  ui.run('renderModelList()');
  assert.deepEqual(aliasesIn(ui, 'src:prov:Acme').sort(), ['as/', 'cl/', 'nv/']);
});

test('the offerings link stays on every row of a model that has offerings, and search still finds any alias', () => {
  const ui = aliasFixture();
  ui.openAll();
  ui.run('renderModelList()');
  assert.equal([...ui.html().matchAll(/model-link-btn/g)].length, 3, 'one per group the model is listed in');
  ui.run('_modelFilters.search = "cl/nvidia"');
  ui.run('renderModelList()');
  assert.ok(ui.groups().length === 3 && ui.groups().every(g => g.rows.includes(nvidiaID)), 'an alias from another provider still finds the model');
});

// ---------- the "更多操作" menu ----------
const moreMenu = ({ right }) => {
  const classes = new Set();
  const menu = { offsetWidth: 160 };
  return {
    classList: { toggle: (c, on) => (on ? classes.add(c) : classes.delete(c)), add: c => classes.add(c), remove: c => classes.delete(c), contains: c => classes.has(c) },
    querySelector: sel => (sel === '.toolbar-more-menu' ? menu : null),
    getBoundingClientRect: () => ({ right }),
    contains: () => false, classes,
  };
};

test('the menu opens toward the side that has room, so it stays attached to its button', () => {
  const wide = createUI();
  const rightEdge = moreMenu({ right: 900 });
  wide.elements.set('modelBulkMore', rightEdge);
  wide.elements.set('toolbarMoreBtn', { setAttribute() {}, focus() {} });
  wide.run('toggleToolbarMore({ stopPropagation() {} })');
  assert.ok(rightEdge.classes.has('open'));
  assert.ok(!rightEdge.classes.has('align-left'), 'button near the right edge: menu extends left as before');

  const narrow = createUI();
  const leftEdge = moreMenu({ right: 120 });
  narrow.elements.set('modelBulkMore', leftEdge);
  narrow.elements.set('toolbarMoreBtn', { setAttribute() {}, focus() {} });
  narrow.run('toggleToolbarMore({ stopPropagation() {} })');
  assert.ok(leftEdge.classes.has('align-left'), 'button near the left edge: menu extends right instead of off-screen');
  narrow.run('toggleToolbarMore({ stopPropagation() {} })');
  assert.ok(!leftEdge.classes.has('open'));
});

test('Escape closes the menu and returns focus to its button; it does nothing when the menu is closed', () => {
  const ui = createUI();
  const more = moreMenu({ right: 900 });
  const button = { attrs: {}, setAttribute(n, v) { this.attrs[n] = v; }, focus() { this.focused = true; } };
  ui.elements.set('modelBulkMore', more);
  ui.elements.set('toolbarMoreBtn', button);
  const escape = () => ui.listeners.keydown.forEach(fn => fn({ key: 'Escape' }));
  escape();
  assert.equal(button.focused, undefined, 'closed menu: focus is left alone');
  ui.run('toggleToolbarMore({ stopPropagation() {} })');
  escape();
  assert.ok(!more.classes.has('open'));
  assert.equal(button.attrs['aria-expanded'], 'false');
  assert.equal(button.focused, true);
});

// ---------- toolbar sizing ----------
test('the search box is smaller and "更多操作" stays on the row\'s right edge', () => {
  const css = asset('css/40_pages_models.css');
  assert.match(css, /\.model-toolbar input\[type="text"\]\{flex:1 1 160px;min-width:130px;/);
  assert.match(css, /\.model-toolbar select\{flex:0 1 auto;min-width:104px;max-width:170px;/);
  assert.match(css, /\.model-toolbar \.toolbar-more\{margin-left:auto\}/);
  assert.match(css, /@media \(min-width:901px\)\{\s*\.model-toolbar input\[type="text"\]\{max-width:260px\}/, 'the cap applies only where the box is not full-width');
  assert.match(css, /@media \(min-width:1101px\)\{\s*\.model-toolbar\{flex-wrap:nowrap\}/);
  assert.match(css, /\.toolbar-more\.align-left \.toolbar-more-menu\{left:0;right:auto\}/);
  const html = asset('html/pages/models.html');
  assert.match(html, /id="modelSearch" placeholder="搜索模型…" title="搜索模型 ID 或 Provider 模型名…"/, 'short placeholder, full hint on hover');
});

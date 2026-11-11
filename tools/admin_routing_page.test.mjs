// Run with: node --test tools/admin_routing_page.test.mjs
// Routing page (#routing): read-only accounts, de-duplicated default-model list, request volume,
// save/revert behaviour, the separate "路由检查" tab and its lazy loading, the model picker,
// the fallback-chain helper, accessible editor controls and the flow-diagram CSS.
// Runs the real shipped scripts in a VM against a stubbed API; no browser.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const asset = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');
const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const ROLES = {
  viewer: ['admin.view'],
  provider_admin: ['admin.view', 'provider.write', 'dangerous.execute'],
  credential_admin: ['admin.view', 'credential.write'],
};
const baseModels = () => [
  { id: 'shared-model', source: 'remote', cost: 'free' },
  { id: 'shared-model', source: 'provider', provider: 'Acme', cost: 'free' },
  { id: 'solo-paid', source: 'zen', cost: 'pass' },
  { id: 'zeta', source: 'provider', provider: 'Acme', cost: 'free' },
  { id: 'alpha', source: 'provider', provider: 'Acme', cost: 'free', disabled: true },
];

function createUI({ role = 'provider_admin', models = baseModels(), config, saveError = '', subtab = 'config' } = {}) {
  const elements = new Map();
  const field = (extra = {}) => ({
    id: '', value: '', dataset: {}, innerHTML: '', textContent: '', title: '', hidden: false, disabled: false, style: {},
    classList: { toggle() {}, add() {}, remove() {}, contains: () => false }, setAttribute() {}, appendChild() {}, focus() { this.focused = true; },
    querySelector: () => null, querySelectorAll: () => [], ...extra,
  });
  const el = id => { if (!elements.has(id)) elements.set(id, field({ id })); return elements.get(id); };
  const permissions = ROLES[role];
  let serverConfig = config || { defaultModel: 'solo-paid', strategy: 'round_robin', modelChain: ['solo-paid'], onlyFree: false };
  const calls = [], toasts = [];
  let activeSub = subtab;
  const subtabEl = () => ({ dataset: { sub: activeSub } });
  // collections the permission pass walks over
  const writeInputs = [field({ tag: 'input' }), field({ tag: 'select' }), field({ tag: 'button' }), field({ tag: 'input', disabled: true })];
  const writePage = { querySelectorAll: () => writeInputs };
  const context = vm.createContext({
    TextEncoder, Date,
    _: el,
    document: {
      addEventListener() {}, createElement: () => field(),
      querySelector: selector => (selector.includes('.subtab.active') ? subtabEl() : null),
      querySelectorAll: selector => (selector === '#tab-routing [data-route-write]' ? [writePage] : []),
    },
    localStorage: { getItem: () => null, setItem() {} },
    t: value => value, tx: value => value, esc: escapeHTML, escAttr: escapeHTML, uiIcon: () => '',
    formatNumber: value => String(value), formatDateTime: String, formatTokenCount: String, formatDuration: String,
    _globalStatuses: {}, _globalAccess: {}, _globalObservations: {}, _globalConfidence: {}, accessTone: () => '', globalStatusTone: () => '',
    _tierSignals: {}, setSaveBarCtx() {}, refreshSaveBar() {}, diffFlat: () => [], fmtDiffVal: String,
    listEmptyState: () => '', ICON_MODELS: '',
    adminHasPermission: permission => permissions.includes(permission),
    toast: (message, kind) => toasts.push({ message, kind }),
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      if (path === '/config/update') {
        if (saveError) throw new Error(saveError);
        serverConfig = { ...serverConfig, strategy: body.strategy, defaultModel: body.defaultModel || serverConfig.defaultModel, modelChain: [...new Set(body.modelChain)], onlyFree: body.onlyFree };
        return { data: {} };
      }
      if (path === '/config') {
        if (!permissions.includes('provider.write')) throw new Error('permission_denied');
        return { data: serverConfig };
      }
      if (path === '/credentials') {
        if (!permissions.includes('credential.write')) throw new Error('permission_denied');
        return { data: { credentials: [] } };
      }
      if (path === '/provider-instances') return { data: { instances: [{ id: 'acme', name: 'Acme', enabled: true }] } };
      if (path === '/model-offerings') return { data: { offerings: [{ modelId: 'shared-model', providerId: 'acme' }, { modelId: 'zeta', providerId: 'acme' }] } };
      if (path === '/provider-state') return { data: { observations: [] } };
      if (path === '/combos') return { data: { combos: [] } };
      if (path === '/request-logs?limit=100') return { data: { items: [] } };
      throw new Error('Unexpected request: ' + method + ' ' + path);
    },
  });
  for (const name of ['05_settings.js', '13_models.js', '20_routing_inspector.js']) {
    vm.runInContext(asset('js/' + name), context, { filename: name });
  }
  vm.runInContext('_cachedModels = ' + JSON.stringify(models), context);
  const run = source => vm.runInContext(source, context);
  return {
    run, el, elements, calls, toasts, writeInputs,
    setSub: value => { activeSub = value; },
    paths: () => calls.map(call => call.method + ' ' + call.path),
    reads: () => calls.filter(call => call.method === 'GET').map(call => call.path),
  };
}

// ---------- read-only accounts ----------
test('a viewer gets a working inspector: no /config request, no error panel, a clear note about the global chain', async () => {
  const ui = createUI({ role: 'viewer' });
  await ui.run('loadRouteInspector()');
  assert.ok(!ui.reads().includes('/config'), 'the forbidden request is never made');
  assert.equal(ui.el('routeInspectorError').hidden, true);
  assert.match(ui.el('routeInspectorCandidates').innerHTML, /Acme/);
  assert.match(ui.el('routeInspectorFallback').innerHTML, /需要 provider\.write 权限才能查看全局回退链/);
  assert.doesNotMatch(ui.el('routeInspectorFallback').innerHTML, /内置免费回退链/, 'it does not claim the chain is empty');
});

test('a permission_denied answer from /config is treated as "not visible", not as a failure', async () => {
  const ui = createUI({ role: 'provider_admin' });
  ui.run('adminHasPermission = () => true'); // the client believes it may read; the server says no
  ui.run("api = (orig => async (m, p, b) => (p === '/config' ? Promise.reject(new Error('permission_denied')) : orig(m, p, b)))(api)");
  await ui.run('loadRouteInspector()');
  assert.equal(ui.el('routeInspectorError').hidden, true);
  assert.match(ui.el('routeInspectorFallback').innerHTML, /provider\.write/);
});

test('without provider.write the settings are disabled, noted, and the flow diagram no longer says "loading"', () => {
  const ui = createUI({ role: 'viewer' });
  ui.run('applyRoutingAccess()');
  assert.equal(ui.el('routeReadOnlyNotice').hidden, false);
  assert.ok(ui.writeInputs.every(input => input.disabled), 'every control in the settings tabs is disabled');
  for (const id of ['routeFlowModel', 'routeFlowStrategy', 'routeFlowFallback']) assert.equal(ui.el(id).textContent, '无权限查看');
});

test('write access is restored exactly, including controls that were disabled to begin with', () => {
  const ui = createUI({ role: 'viewer' });
  ui.run('applyRoutingAccess()');
  ui.run('adminHasPermission = () => true');
  ui.run('applyRoutingAccess()');
  assert.deepEqual(ui.writeInputs.map(input => input.disabled), [false, false, false, true]);
  assert.equal(ui.el('routeReadOnlyNotice').hidden, true);
});

test('a provider admin sees no read-only note and nothing disabled', () => {
  const ui = createUI();
  ui.run('applyRoutingAccess()');
  assert.equal(ui.el('routeReadOnlyNotice').hidden, true);
  assert.ok(ui.writeInputs.slice(0, 3).every(input => !input.disabled));
});

test('loadConfig makes no request for a read-only account and re-applies the read-only state', async () => {
  const ui = createUI({ role: 'credential_admin' });
  await ui.run('loadConfig()');
  assert.deepEqual(ui.calls, []);
  assert.equal(ui.el('routeReadOnlyNotice').hidden, false);
});

// ---------- default model dropdown ----------
test('a model offered by several sources is listed once in the default-model dropdown', async () => {
  const ui = createUI();
  await ui.run('loadConfig()');
  const html = ui.el('settingDefModel').innerHTML;
  assert.equal([...html.matchAll(/value="shared-model"/g)].length, 1);
  assert.match(html, /免费模型 \(2\)/, 'shared-model and zeta');
  assert.match(html, /付费模型 \(1\)/);
  assert.doesNotMatch(html, /value="alpha"/, 'a disabled model is not offered');
  assert.match(html, /value="solo-paid" selected/);
});

// ---------- request volume ----------
test('opening the page reads /config once, and the inspector is not loaded while its tab is hidden', async () => {
  const ui = createUI();
  await Promise.all([ui.run('loadConfig()'), ui.run('maybeLoadRouteInspector()')]);
  assert.deepEqual(ui.reads(), ['/config'], 'one config read, no inspector requests');
});

test('opening the page on the inspector tab shares a single /config read between the form and the inspector', async () => {
  const ui = createUI({ subtab: 'inspect' });
  await Promise.all([ui.run('loadConfig()'), ui.run('maybeLoadRouteInspector()')]);
  assert.equal(ui.reads().filter(path => path === '/config').length, 1);
  assert.equal(ui.reads().length, 6, 'config once, plus instances, offerings, state, combos and logs (credentials are not requested without credential.write)');
});

test('concurrent config reads are one request; later reads fetch fresh data', async () => {
  const ui = createUI();
  await Promise.all([ui.run('fetchAdminConfig()'), ui.run('fetchAdminConfig()'), ui.run('fetchAdminConfig()')]);
  assert.equal(ui.reads().length, 1);
  await ui.run('fetchAdminConfig()');
  assert.equal(ui.reads().length, 2, 'no stale caching');
});

test('switching back to the inspector within 30 seconds does not reload it; after that it does', async () => {
  const ui = createUI();
  await ui.run('routeInspectorShown()');
  const first = ui.calls.length;
  await ui.run('routeInspectorShown()');
  assert.equal(ui.calls.length, first, 'fresh data is reused');
  ui.run('_routeInspectorLoadedAt = Date.now() - 31000');
  await ui.run('routeInspectorShown()');
  assert.ok(ui.calls.length > first);
});

test('saving a setting costs one write and one config read, and the inspector is updated in place', async () => {
  const ui = createUI();
  await ui.run('loadConfig()');
  await ui.run('loadRouteInspector()');
  ui.calls.length = 0;
  ui.el('settingStrategy').value = 'random';
  ui.el('settingDefModel').value = 'zeta';
  ui.el('settingModelChain').value = 'zeta, alpha';
  ui.el('settingOnlyFree').value = 'true';
  await ui.run('updateConfig()');
  assert.deepEqual(ui.paths(), ['POST /config/update', 'GET /config']);
  assert.equal(ui.el('routeFlowStrategy').textContent, 'random');
  assert.match(ui.el('routeInspectorFallback').innerHTML, /zeta[\s\S]*alpha/, 'the inspector shows the new chain');
  assert.match(ui.el('routeInspectorModel').innerHTML, /<optgroup label="默认与回退链"><option value="zeta"/, 'and the new default leads the picker');
  assert.equal(ui.run('_routeInspectorModel'), 'solo-paid', 'the model being inspected is not changed under the user');
  assert.doesNotMatch(ui.el('routeInspectorSummary').innerHTML, /skeleton/, 'no loading skeleton on a refresh');
});

test('a refresh before the inspector was ever opened does nothing', () => {
  const ui = createUI();
  ui.run('refreshRouteInspectorConfig({ defaultModel: "x", modelChain: [] })');
  assert.deepEqual(ui.calls, []);
  assert.equal(ui.run('_routeInspectorData'), null);
});

// ---------- failed save ----------
test('a failed save puts the dropdowns back to the server values and keeps the typed chain', async () => {
  const ui = createUI({ saveError: 'save_failed' });
  await ui.run('loadConfig()');
  ui.calls.length = 0;
  ui.el('settingStrategy').value = 'random';
  ui.el('settingDefModel').value = 'zeta';
  ui.el('settingOnlyFree').value = 'true';
  ui.el('settingModelChain').value = 'my, draft';
  ui.el('settingModelChain').dataset.dirty = '1';
  await ui.run('updateConfig()');
  assert.deepEqual(ui.paths(), ['POST /config/update'], 'a failed save makes no further request');
  assert.equal(ui.el('settingStrategy').value, 'round_robin');
  assert.equal(ui.el('settingDefModel').value, 'solo-paid');
  assert.equal(ui.el('settingOnlyFree').value, 'false');
  assert.equal(ui.el('settingModelChain').value, 'my, draft');
  assert.equal(ui.el('settingModelChain').dataset.dirty, '1');
  assert.equal(ui.toasts.at(-1).kind, 'error');
});

// ---------- inspector tab ----------
test('the page has the inspector as its own tab, no link-tab, and distinct titles', () => {
  const html = asset('html/pages/routing.html');
  assert.deepEqual([...html.matchAll(/<button type="button" class="subtab[^"]*" data-sub="(\w+)">([^<]+)</g)].map(m => m[1] + ':' + m[2]),
    ['config:路由策略', 'inspect:路由检查', 'tuning:运行调优']);
  assert.doesNotMatch(html, /模型目录 ↗|data-sub="models"/);
  assert.match(html, /<div class="page-subpage" data-subpage="inspect" style="display:none">[\s\S]*id="routeInspectorSection"/);
  assert.ok(html.indexOf('data-subpage="config"') < html.indexOf('data-subpage="inspect"') && html.indexOf('page-subtabs') < html.indexOf('routeInspectorSection'),
    'the inspector no longer sits above the tabs');
  assert.equal([...html.matchAll(/>路由策略</g)].length, 2, 'page title and tab only; the section is titled differently');
  assert.match(html, /默认模型与回退/);
  assert.match(html, /<button type="button" class="btn btn-sm" onclick="switchTab\('models'\)">查看模型<\/button>/, 'the header button still opens the Models page');
});

test('the write-only tabs are marked so the permission pass can disable them', () => {
  const html = asset('html/pages/routing.html');
  assert.match(html, /data-subpage="config" data-route-write>/);
  assert.match(html, /data-subpage="tuning" style="display:none" data-route-write>/);
  assert.doesNotMatch(html.slice(html.indexOf('data-subpage="inspect"')), /data-route-write/, 'the inspector stays usable read-only');
});

test('the egress link text matches the page it opens', () => {
  const html = asset('html/pages/routing.html');
  assert.match(html, /showPageSubTab\('upstreams', 'cline'\);return false" class="lnk-a">全局出口代理：出口代理池内多个地址的选择方式 →/);
  assert.doesNotMatch(html, /网络 → 出口代理池|OpenCode Zen \/ Cline 的出口代理地址/);
});

// ---------- model picker ----------
test('the picker lists the default model and the fallback chain first, then everything else A to Z', async () => {
  const ui = createUI({ models: [{ id: 'zeta' }, { id: 'alpha' }, { id: 'mid' }, { id: 'solo-paid' }] });
  await ui.run('loadRouteInspector()');
  const html = ui.el('routeInspectorModel').innerHTML;
  assert.match(html, /<optgroup label="默认与回退链"><option value="solo-paid" selected>/);
  const rest = html.slice(html.indexOf('<optgroup label="全部模型">'));
  assert.deepEqual([...rest.matchAll(/value="([^"]+)"/g)].map(m => m[1]), ['alpha', 'mid', 'shared-model', 'zeta']);
  assert.equal(ui.el('routeInspectorModelCount').textContent, '5');
});

test('typing in the filter narrows the list (keeping the selection) and Enter picks the first match', async () => {
  const ui = createUI({ models: [{ id: 'zeta' }, { id: 'alpha' }, { id: 'mid' }, { id: 'solo-paid' }] });
  await ui.run('loadRouteInspector()');
  ui.run('filterRouteInspectorModels("ZE")');
  const html = ui.el('routeInspectorModel').innerHTML;
  assert.deepEqual([...html.matchAll(/value="([^"]+)"/g)].map(m => m[1]), ['solo-paid', 'zeta'], 'the current model stays so the select is never empty');
  assert.equal(ui.el('routeInspectorModelCount').textContent, '2 / 5');
  ui.run('pickFirstRouteInspectorModel()');
  assert.equal(ui.run('_routeInspectorModel'), 'zeta', 'Enter selects the first model that matches the filter');
  ui.run('filterRouteInspectorModels("zzz-nothing"); pickFirstRouteInspectorModel()');
  assert.equal(ui.run('_routeInspectorModel'), 'zeta', 'no match: the selection is left alone');
  assert.match(ui.el('routeInspectorSummary').innerHTML, /zeta/);
  ui.run('filterRouteInspectorModels("")');
  assert.equal(ui.el('routeInspectorModelCount').textContent, '5');
});

// ---------- fallback chain helper ----------
test('the chain helper appends a catalog model, saves, and clears its box', async () => {
  const ui = createUI();
  await ui.run('loadConfig()');
  ui.calls.length = 0;
  ui.el('settingChainAdd').value = 'zeta';
  await ui.run('addModelToChain()');
  assert.equal(ui.calls[0].body.modelChain.join(','), 'solo-paid,zeta');
  assert.equal(ui.el('settingChainAdd').value, '');
  assert.equal(ui.el('settingModelChain').value, 'solo-paid, zeta');
});

test('the chain helper refuses unknown ids and duplicates without saving, and accepts "free"', async () => {
  const ui = createUI();
  await ui.run('loadConfig()');
  ui.calls.length = 0;
  ui.el('settingChainAdd').value = 'not-in-catalog';
  await ui.run('addModelToChain()');
  ui.el('settingChainAdd').value = 'solo-paid';
  await ui.run('addModelToChain()');
  assert.deepEqual(ui.calls, []);
  assert.deepEqual(ui.toasts.map(n => n.kind), ['info', 'info']);
  ui.el('settingChainAdd').value = 'free';
  await ui.run('addModelToChain()');
  assert.equal(ui.calls[0].body.modelChain.join(','), 'solo-paid,free');
});

// ---------- accessible names in the structured editors ----------
test('editor inputs and buttons have accessible names', () => {
  const made = [];
  const ui = createUI();
  ui.run('document.createElement = () => { const e = { className: "", style: {}, children: [], attrs: {}, classList: { add() {}, remove() {} }, setAttribute(n, v) { this.attrs[n] = v; }, appendChild(c) { this.children.push(c); }, addEventListener() {}, querySelector: () => null }; globalThis.__made.push(e); return e; }; document.createTextNode = text => ({ text });');
  ui.run('globalThis.__made = []; _("tunAliasesEditor").appendChild = () => {}; _("tunErrorRulesEditor").appendChild = () => {};');
  ui.run('addAliasRow("a", "b", "c"); addErrorRuleRow([429], "x", "continue", false)');
  const all = JSON.parse(ui.run('JSON.stringify(globalThis.__made.map(e => ({ cls: e.className, attrs: e.attrs })))'));
  const withLabel = cls => all.filter(e => e.cls.includes(cls) && e.attrs['aria-label']);
  for (const cls of ['se-alias', 'se-target', 'se-provider', 'se-codes', 'se-match', 'se-action', 'se-remove', 'se-move']) {
    assert.ok(withLabel(cls).length >= 1, cls + ' has no aria-label');
  }
  assert.equal(all.filter(e => e.cls.includes('se-move') && e.attrs['aria-label']).length, 2, 'both move buttons');
  void made;
});

// ---------- flow diagram CSS ----------
test('the flow diagram has one consistent narrow-screen layout', () => {
  const css = asset('css/100_ui_optimization.css');
  const block = css.slice(css.indexOf('.route-flow{display:grid;grid-template-columns:repeat(2'));
  assert.ok(block.startsWith('.route-flow{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;overflow:visible;'), 'display:grid is explicit, so the columns actually apply');
  assert.match(css, /\.route-flow-node:last-child\{grid-column:1\/-1\}/);
  assert.match(css, /@media \(max-width:420px\)\{\s*\.route-flow\{grid-template-columns:minmax\(0,1fr\)\}/);
  const base = asset('css/95_control_plane.css');
  assert.match(base, /@media \(max-width:1180px\)\{[\s\S]*?\.route-flow\{display:flex;overflow-x:auto/, 'the mid-width scroll strip remains for 901-1180px');
});

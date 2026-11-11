// Run with: node --test tools/admin_provider_model_rows.test.mjs
// Provider editor model rows (client name -> upstream ID, test-before-add) and the
// Routing alias preview. Static VM harness: no browser involved.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const assets = [
  '05_alias_preview.js', '06_providers_custom.js', '08_providers_detect.js',
  '09_providers_pickers.js', '10_providers_import.js', '13_models.js',
];

function createUI({ testResult = () => ({ data: { ok: true, durationMs: 12 } }) } = {}) {
  const elements = new Map();
  const cells = new Map(); // selector -> [{dataset, innerHTML}]
  const field = (value = '') => ({
    value, textContent: '', style: { display: 'none' }, dataset: {}, className: '',
    classList: { contains: () => false, toggle: () => {}, add: () => {}, remove: () => {} },
    focus: () => {},
    get innerHTML() { return this.html || ''; },
    set innerHTML(v) { this.html = v; },
  });
  for (const id of ['provModels', 'provAddName', 'provAddUpstream', 'provAddResult', 'provModelsChips', 'provModelsCount',
    'provBaseURL', 'provKey', 'provHeaders', 'provName', 'provPriority', 'provTimeout', 'provRpmLimit', 'provDailyLimit',
    'provTokenLimit', 'provTokenWindow', 'provProxyUrl', 'provWeight', 'provEnabled', 'provFree',
    'provPickerList', 'provPickerSearch', 'provPickerCount']) elements.set(id, field());
  elements.get('provTokenWindow').value = 'monthly';
  const calls = [];
  const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    document: {
      addEventListener: () => {},
      querySelector: () => null,
      querySelectorAll: selector => cells.get(selector) || [],
    },
    localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    t: value => value, tx: value => value,
    esc: escapeHTML, escAttr: escapeHTML,
    uiIcon: () => '', toast: () => {},
    refreshSaveBar: () => {},
    setTimeout, clearTimeout, Date,
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      if (path === '/providers/test') return testResult(body);
      if (path === '/model-offerings') return { data: { offerings: [] } };
      throw new Error('Unexpected API request: ' + method + ' ' + path);
    },
  });
  for (const asset of assets) {
    vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/' + asset, import.meta.url), 'utf8'), context, { filename: asset });
  }
  vm.runInContext('var _provConnSel = []; initConnPick = () => {}; syncProvTokenWindowUI = () => {};', context);
  return {
    calls, elements,
    set: (id, value) => { elements.get(id).value = value; },
    cell: (selector, model) => {
      const el = { dataset: { model }, innerHTML: '' };
      cells.set(selector, [...(cells.get(selector) || []), el]);
      return el;
    },
    run: source => vm.runInContext(source, context),
    // Values created inside the VM have that realm's prototypes; compare plain data.
    plain: value => value === undefined ? undefined : JSON.parse(JSON.stringify(value)),
    json: source => {
      const text = vm.runInContext('JSON.stringify(' + source + ')', context);
      return text === undefined ? undefined : JSON.parse(text);
    },
    lastBody: () => JSON.parse(JSON.stringify(calls.at(-1).body)),
  };
}

test('only declared models whose upstream ID differs from the name are saved', () => {
  const ui = createUI();
  ui.set('provModels', 'friendly, plain, other');
  ui.run(`_provModelMap = { friendly: ' vendor/real ', plain: 'plain', other: '', gone: 'zzz' }`);
  assert.deepEqual(ui.json('provModelMapForSave()'), { friendly: 'vendor/real' });
});

test('editing a row upstream ID updates the draft, marks the form dirty and drops its stale test result', () => {
  const ui = createUI();
  ui.set('provModels', 'friendly');
  ui.run(`_provBaseJson = JSON.stringify(serializeProvForm())`);
  ui.run(`_provRowTests.friendly = { ok: true, text: '12ms' }`);
  ui.run(`onProvModelUpstreamInput({ dataset: { model: 'friendly' }, value: ' vendor/real ' })`);
  assert.deepEqual(ui.json('provModelMapForSave()'), { friendly: 'vendor/real' });
  assert.equal(ui.json('_provRowTests.friendly'), undefined);
  assert.equal(ui.run('JSON.stringify(serializeProvForm()) !== _provBaseJson'), true, 'mapping change must count as an unsaved edit');
  // typing the name itself (or clearing) means "same as name" and removes the entry
  ui.run(`onProvModelUpstreamInput({ dataset: { model: 'friendly' }, value: 'friendly' })`);
  assert.deepEqual(ui.json('provModelMapForSave()'), {});
  assert.equal(ui.run('JSON.stringify(serializeProvForm()) === _provBaseJson'), true);
});

test('removing a model drops its mapping and test result', () => {
  const ui = createUI();
  ui.set('provModels', 'a, b');
  ui.run(`_provModelMap = { a: 'up/a', b: 'up/b' }; _provRowTests = { a: { ok: true, text: 'x' }, b: { ok: true, text: 'y' } }`);
  ui.run(`removeProvModel('a')`);
  assert.equal(ui.elements.get('provModels').value, 'b');
  assert.deepEqual(ui.json('_provModelMap'), { b: 'up/b' });
  assert.deepEqual(Object.keys(ui.json('_provRowTests')), ['b']);
});

test('applyProviderForm loads the saved mapping and clears row tests', () => {
  const ui = createUI();
  ui.run(`_provRowTests = { old: { ok: true, text: 'x' } }`);
  ui.run(`applyProviderForm({ name: 'P', baseURL: 'http://p/v1', modelIds: ['friendly'], modelMap: { friendly: 'vendor/real' }, enabled: true })`);
  assert.deepEqual(ui.json('_provModelMap'), { friendly: 'vendor/real' });
  assert.deepEqual(ui.json('_provRowTests'), {});
  assert.match(ui.elements.get('provModelsChips').innerHTML, /value="vendor\/real"/);
});

test('a hostile model name or upstream ID cannot break out of row attributes', () => {
  const ui = createUI();
  const name = `x"><img src=x onerror=alert(1)>`;
  ui.run(`_provModelMap = {}`);
  ui.set('provModels', name);
  ui.run(`_provModelMap[${JSON.stringify(name)}] = ${JSON.stringify(`" onfocus="alert(2)`)}`);
  ui.run('renderProvModelChips()');
  const html = ui.elements.get('provModelsChips').innerHTML;
  assert.doesNotMatch(html, /<img/);
  assert.doesNotMatch(html, /onfocus="alert/);
});

test('the test request always carries the row upstream draft, for saved and unsaved providers', async () => {
  const ui = createUI();
  const saved = await ui.run(`provTestModel({ id: 'p1' }, 'friendly', 'vendor/real')`);
  assert.equal(saved.ok, true);
  assert.deepEqual(ui.lastBody(), { id: 'p1', model: 'friendly', upstreamModel: 'vendor/real' });
  // a cleared draft is sent as the name so it overrides a mapping that is already saved
  await ui.run(`provTestModel({ id: 'p1' }, 'friendly', '')`);
  assert.equal(ui.lastBody().upstreamModel, 'friendly');
  await ui.run(`provTestModel({ baseURL: 'http://x/v1', apiKey: 'k', headers: {} }, 'm', 'up')`);
  assert.deepEqual(ui.lastBody(), { baseURL: 'http://x/v1', apiKey: 'k', headers: {}, model: 'm', upstreamModel: 'up' });
});

test('test results report the upstream ID that was sent, and failures surface the upstream error', async () => {
  let reply = { data: { ok: true, durationMs: 40, upstreamModel: 'vendor/real' } };
  const ui = createUI({ testResult: () => reply });
  const ok = ui.plain(await ui.run(`provTestModel({ id: 'p1' }, 'friendly', 'vendor/real')`));
  assert.deepEqual(ok, { ok: true, text: '40ms · 实际发送: vendor/real' });
  reply = { data: { ok: false, error: 'model not found' } };
  assert.deepEqual(ui.plain(await ui.run(`provTestModel({ id: 'p1' }, 'friendly', '')`)), { ok: false, text: 'model not found' });
  assert.equal((await ui.run(`provTestModel({ baseURL: '' }, 'm', '')`)).ok, false, 'no provider target must not call the API');
  const before = ui.calls.length;
  await ui.run(`provTestModel(null, 'm', '')`);
  assert.equal(ui.calls.length, before);
});

test('a row test result is discarded when the draft changed while it was running', async () => {
  let release;
  const ui = createUI({ testResult: () => new Promise(resolve => { release = () => resolve({ data: { ok: true, durationMs: 5 } }); }) });
  const resultCell = ui.cell('#provModelsChips .prov-model-result', 'friendly');
  ui.set('provModels', 'friendly');
  ui.run(`_editingProviderId = 'p1'; _provModelMap = { friendly: 'vendor/old' }`);
  const pending = ui.run(`testProvModelRow({ dataset: { model: 'friendly' }, disabled: false })`);
  assert.match(resultCell.innerHTML, /测试中/);
  ui.run(`onProvModelUpstreamInput({ dataset: { model: 'friendly' }, value: 'vendor/new' })`);
  release();
  await pending;
  assert.equal(ui.json('_provRowTests.friendly'), undefined, 'stale result must not be stored');
  assert.equal(resultCell.innerHTML, '', 'stale result must not be shown');
});

test('a row test result is discarded when another provider editor was opened', async () => {
  let release;
  const ui = createUI({ testResult: () => new Promise(resolve => { release = () => resolve({ data: { ok: true, durationMs: 5 } }); }) });
  ui.set('provModels', 'friendly');
  ui.run(`_editingProviderId = 'p1'`);
  const pending = ui.run(`testProvModelRow({ dataset: { model: 'friendly' }, disabled: false })`);
  ui.run(`_provEditorRevision++`);
  release();
  await pending;
  assert.equal(ui.json('_provRowTests.friendly').running, true, 'only the stale completion is dropped');
});

test('add-with-test: the tested name and upstream ID travel with the model, edits invalidate the test', async () => {
  const ui = createUI({ testResult: () => ({ data: { ok: true, durationMs: 9 } }) });
  ui.run(`_editingProviderId = 'p1'`);
  ui.set('provAddName', 'friendly');
  ui.set('provAddUpstream', 'vendor/real');
  await ui.run('testNewProvModel(null)');
  assert.deepEqual(ui.lastBody(), { id: 'p1', model: 'friendly', upstreamModel: 'vendor/real' });
  ui.run('addProvModel()');
  assert.equal(ui.elements.get('provModels').value, 'friendly');
  assert.deepEqual(ui.json('_provModelMap'), { friendly: 'vendor/real' });
  assert.equal(ui.json('_provRowTests.friendly').ok, true, 'a tested model keeps its result in the list');
  assert.equal(ui.elements.get('provAddName').value, '');

  // changed after the test -> no carried result
  ui.set('provAddName', 'second');
  ui.set('provAddUpstream', 'vendor/two');
  await ui.run('testNewProvModel(null)');
  ui.set('provAddUpstream', 'vendor/three');
  ui.run('addProvModel()');
  assert.equal(ui.elements.get('provModels').value, 'friendly, second');
  assert.equal(ui.json('_provRowTests.second'), undefined);
  assert.equal(ui.json('_provModelMap').second, 'vendor/three');
});

test('add validates the name: required, no comma, no duplicate; an upstream equal to the name is not stored', () => {
  const ui = createUI();
  ui.set('provModels', 'existing');
  ui.run('addProvModel()');
  assert.match(ui.elements.get('provAddResult').innerHTML, /请先填写模型名称/);
  ui.set('provAddName', 'a,b');
  ui.run('addProvModel()');
  assert.match(ui.elements.get('provAddResult').innerHTML, /逗号/);
  ui.set('provAddName', 'existing');
  ui.run('addProvModel()');
  assert.match(ui.elements.get('provAddResult').innerHTML, /已在列表/);
  assert.equal(ui.elements.get('provModels').value, 'existing');
  ui.set('provAddName', 'same');
  ui.set('provAddUpstream', 'same');
  ui.run('addProvModel()');
  assert.equal(ui.elements.get('provModels').value, 'existing, same');
  assert.deepEqual(ui.json('_provModelMap'), {});
});

test('picker and import rows test by name; import tests use the batch provider ID', async () => {
  const ui = createUI();
  ui.run(`_editingProviderId = 'p1'`);
  const pickCell = ui.cell('#provPickerList .prov-pick-result', 'm1');
  await ui.run(`testProvPickerModel({ dataset: { model: 'm1' }, disabled: false })`);
  assert.deepEqual(ui.lastBody(), { id: 'p1', model: 'm1', upstreamModel: 'm1' });
  assert.match(pickCell.innerHTML, /12ms/);

  const importCell = ui.cell('#importPreviewList .import-test-result', 'm2');
  ui.run(`_importBatch = { providerId: 'p9', items: [] }`);
  await ui.run(`testImportModel({ dataset: { model: 'm2' }, disabled: false })`);
  assert.deepEqual(ui.lastBody(), { id: 'p9', model: 'm2', upstreamModel: 'm2' });
  assert.match(importCell.innerHTML, /12ms/);

  // a batch swapped during the request must not receive the old result
  ui.run(`_importBatch = { providerId: 'p9', items: [] }`);
  const stale = ui.run(`testImportModel({ dataset: { model: 'm3' }, disabled: false })`);
  ui.run(`_importBatch = { providerId: 'p10', items: [] }`);
  await stale;
  assert.equal(ui.json('_importTests.m3').running, true);
});

test('alias preview resolves the provider and upstream ID for a target', () => {
  const ui = createUI();
  const offerings = [
    { providerId: 'prov_a', providerName: 'Alpha', modelId: 'friendly', qualifiedId: 'al/friendly', upstreamModelId: 'vendor/real' },
    { providerId: 'prov_b', providerName: 'Beta', modelId: 'friendly', qualifiedId: 'be/friendly', upstreamModelId: 'friendly' },
    { providerId: 'prov_b', providerName: 'Beta', modelId: 'other', qualifiedId: 'be/other', upstreamModelId: 'other' },
  ];
  const preview = (target, provider) => ui.json(`resolveAliasPreview(${JSON.stringify(target)}, ${JSON.stringify(provider)}, ${JSON.stringify(offerings)})`);
  assert.equal(preview('friendly', '').matches.length, 2, 'unpinned alias can reach both providers');
  assert.deepEqual(preview('friendly', 'prov_a').matches.map(m => m.upstreamModelId), ['vendor/real']);
  assert.deepEqual(preview('friendly', 'beta').matches.map(m => m.providerId), ['prov_b'], 'display name, case-insensitive');
  assert.deepEqual(preview('friendly', 'al').matches.map(m => m.providerId), ['prov_a'], 'namespace prefix');
  assert.deepEqual(preview('al/friendly', '').matches.map(m => m.providerId), ['prov_a'], 'qualified target pins the provider');
  assert.equal(preview('missing', '').kind, 'none');
  assert.equal(preview('', '').kind, 'empty');
  assert.equal(preview('friendly', 'nobody').kind, 'none');
  const text = value => ui.run(`aliasPreviewText(${JSON.stringify(value)})`);
  assert.equal(text(preview('friendly', 'prov_a')), '→ Alpha · vendor/real');
  assert.equal(text(preview('missing', '')), '未找到匹配的模型');
  assert.equal(text(preview('', '')), '');
  assert.match(text(preview('friendly', '')), /^→ Alpha · vendor\/real ; Beta · friendly$/);
});

test('alias preview lists at most three providers and counts the rest', () => {
  const ui = createUI();
  const many = Array.from({ length: 5 }, (_, i) => ({ providerId: 'p' + i, providerName: 'P' + i, modelId: 'm', qualifiedId: 'x' + i + '/m', upstreamModelId: 'm' }));
  const text = ui.run(`aliasPreviewText(resolveAliasPreview('m', '', ${JSON.stringify(many)}))`);
  assert.match(text, /\+2$/);
  assert.equal(text.split(' ; ').length, 3);
});

test('the Models page shows the upstream ID only when it differs from the client name', () => {
  const ui = createUI();
  const mapped = ui.run(`modelProviderNameButton({ qualifiedId: 'al/friendly', modelId: 'friendly', upstreamModelId: 'vendor/real', providerName: 'Alpha' })`);
  assert.match(mapped, /model-provider-upstream/);
  assert.match(mapped, /→ vendor\/real/);
  const plain = ui.run(`modelProviderNameButton({ qualifiedId: 'al/plain', modelId: 'plain', upstreamModelId: 'plain', providerName: 'Alpha' })`);
  assert.doesNotMatch(plain, /model-provider-upstream/);
});

test('picker rows: Test only for models not yet in the provider; full ID in a tooltip; group header has aligned parts', () => {
  const ui = createUI();
  ui.run(`_provPickerModels = ['added-one', 'free-new', 'paid-new'];
    _provPickerExisting = { 'added-one': true };
    _provPickerTiers = { 'added-one': 'free', 'free-new': 'free', 'paid-new': 'pass' };
    _provPickerOrder = [];
    _provPickerTests = { 'free-new': { ok: false, text: 'model not found' } };
    _provPickerStats = { free: 3, paid: 292 };`);
  ui.run('renderProvPickerList()');
  const html = ui.elements.get('provPickerList').innerHTML;
  const rows = html.split('<label ').slice(1);
  assert.equal(rows.length, 3);
  const byId = id => rows.find(r => r.includes('>' + id + '</span>'));
  assert.doesNotMatch(byId('added-one'), /prov-pick-test/, 'added rows get no Test button');
  assert.match(byId('added-one'), /已添加/);
  assert.match(byId('free-new'), /prov-pick-test/);
  assert.match(byId('free-new'), /model not found/, 'result text is rendered in full for the row');
  assert.match(byId('paid-new'), /prov-pick-test/);
  assert.match(html, /title="paid-new"/, 'long IDs stay readable through the tooltip');
  assert.match(html, /grp-name">免费<\/span><span class="grp-count">2 \/ 3<\/span>/);
  assert.match(html, /grp-name">付费<\/span><span class="grp-count">1 \/ 292<\/span>/);
  assert.equal((html.match(/grp-select/g) || []).length, 2);
  assert.doesNotMatch(html, /float-note/);
});

// Run with: node --test tools/admin_combo_isolation.test.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const catalog = [
  { id: 'deepseek-4-1', source: 'zen', provider: 'opencode', cost: 'free' },
  { id: 'deepseek-4-1', source: 'provider', provider: 'AntSeed', cost: 'free' },
];
const stableCatalog = [
  { id: 'deepseek-4-1', source: 'zen', provider: 'opencode', providerId: 'opencode', providerName: 'OpenCode Zen', qualifiedId: 'op/deepseek-4-1', cost: 'free' },
  { id: 'deepseek-4-1', source: 'provider', provider: 'AntSeed', providerId: 'antseed_primary', providerName: 'AntSeed', qualifiedId: 'as/deepseek-4-1', cost: 'free' },
];

const escapeHTML = value => String(value).replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[character]));
const decodeHTML = value => value.replace(/&(?:amp|lt|gt|quot|apos|#\d+|#x[\da-f]+);/gi, entity => {
  const named = { '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&apos;': "'" };
  if (named[entity]) return named[entity];
  return String.fromCodePoint(entity.startsWith('&#x') ? parseInt(entity.slice(3, -1), 16) : Number(entity.slice(2, -1)));
});

function attributes(tag) {
  const result = {};
  const pattern = /([\w:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;
  for (const match of tag.matchAll(pattern)) result[match[1]] = decodeHTML(match[2] ?? match[3]);
  return result;
}

function createUI({ models = catalog, cleanupState } = {}) {
  const elements = new Map();
  for (const id of [
    'comboPickerSearch', 'comboPickerList', 'comboPickerCount', 'comboModelPicker',
    'comboEntries', 'comboPreview', 'comboId', 'comboName', 'comboSaveResult',
    'comboDesc', 'comboCategory', 'comboEnabled', 'comboEmergency',
    'comboTestModal', 'comboTestList', 'comboTestId', 'comboTestSummary', 'comboTestResult', 'comboTestSaveBtn',
  ]) elements.set(id, {
    value: '', innerHTML: '', textContent: '', checked: false,
    scrollTop: 0, style: {}, dataset: {}, focus: () => {},
  });
  elements.get('comboId').value = 'antseed-combo';
  elements.get('comboName').value = 'AntSeed combo';
  elements.get('comboEnabled').value = 'true';
  const writes = [];
  const toasts = [];
  let savedCombo;
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    t: value => value, tx: value => value,
    esc: escapeHTML,
    escAttr: escapeHTML,
    uiIcon: () => '',
    toast: value => toasts.push(value),
    friendlyTestError: error => error.message,
    pickerMergeIds: (order, values) => values.forEach(value => { if (!order.includes(value)) order.push(value); }),
    openDrawer: () => {},
    closeDrawer: () => {},
    document: { addEventListener: () => {}, querySelector: () => null, querySelectorAll: () => [] },
    api: async (method, path, body) => {
      if (method === 'POST' && path === '/combos/save') {
        writes.push(JSON.parse(JSON.stringify(body)));
        savedCombo = JSON.parse(JSON.stringify(body));
        return { data: { combo: body } };
      }
      if (method === 'GET' && path === '/combos') return { data: { combos: savedCombo ? [savedCombo] : [], modelCatalog: models, reservedAliases: [] } };
      if (method === 'POST' && path === '/combos/test') return { data: { results: (savedCombo.entries || []).filter(entry => !entry.disabled).map(entry => ({
        model: entry.model, source: entry.source, provider: entry.provider, ok: true,
        upstream: entry.source === 'zen' ? 'opencode' : entry.source === 'provider' ? 'provider' : 'cline',
      })) } };
      throw new Error('Unexpected API request: ' + method + ' ' + path);
    },
  });
  const manifest = readFileSync(new URL('../internal/admin/ui_manifest.go', import.meta.url), 'utf8');
  const assets = Array.from(manifest.matchAll(/"ui\/js\/(12_[^"]+\.js)"/g), match => match[1]);
  for (const asset of assets) {
    vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/' + asset, import.meta.url), 'utf8'), context, { filename: asset });
  }
  if (cleanupState) {
    Object.assign(context, cleanupState);
    vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/13_model_cleanup.js', import.meta.url), 'utf8'), context);
  }
  vm.runInContext('_comboCatalog = ' + JSON.stringify(models) + ';', context);
  const evaluate = source => vm.runInContext(source, context);
  const runHandler = (handler, value, checked) => {
    context.testControl = { value, checked };
    context.event = { target: context.testControl };
    return evaluate('(function () {' + handler + '}).call(testControl)');
  };
  return {
    elements, writes, toasts, evaluate, runHandler,
    selectOffering: (provider, reference) => {
      evaluate('renderComboPickerList()');
      const html = elements.get('comboPickerList').innerHTML;
      const headers = Array.from(html.matchAll(/<div\b[^>]*class="[^"]*combo-pick-group[^"]*"[^>]*>([\s\S]*?)<\/div>/g));
      const groups = headers.flatMap((header, index) => {
        if (!decodeHTML(header[1]).includes(provider)) return [];
        return [html.slice(header.index + header[0].length, headers[index + 1]?.index ?? html.length)];
      });
      assert.ok(groups.length, provider + ' group is visible');
      const rows = groups.flatMap(group => Array.from(group.matchAll(/<label\b[^>]*class="[^"]*combo-pick-row[^"]*"[^>]*>([\s\S]*?)<\/label>/g), match => match[1]));
      const row = rows.find(candidate => !reference || decodeHTML(candidate).includes(reference));
      assert.ok(row, provider + (reference ? ': ' + reference : '') + ' offering is visible');
      const input = row.match(/<input\b[^>]*type="checkbox"[^>]*>/)?.[0];
      assert.ok(input, provider + ' offering has a checkbox');
      const attrs = attributes(input);
      assert.ok(attrs.onchange, provider + ' checkbox has a generated handler');
      runHandler(attrs.onchange, attrs.value || '', true);
    },
    entries: () => JSON.parse(evaluate('JSON.stringify(_comboEntries)')),
  };
}

test('choosing the AntSeed row preserves its provider pin in the actual combo save payload', async () => {
  const ui = createUI();
  ui.selectOffering('AntSeed');
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  assert.equal(ui.writes.length, 1, 'the combo is saved through the existing endpoint');
  assert.deepEqual(ui.writes[0].entries, [
    { model: 'deepseek-4-1', disabled: false, source: 'provider', provider: 'AntSeed' },
  ]);
});

test('stable instance IDs are saved while qualified model names remain presentation references', async () => {
  const ui = createUI({ models: stableCatalog });
  ui.selectOffering('AntSeed', 'as/deepseek-4-1');
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  assert.deepEqual(ui.writes[0].entries, [{ model: 'deepseek-4-1', disabled: false, source: 'provider', provider: 'antseed_primary' }]);
});

for (const scenario of [
  { name: 'provider missing this model', pin: 'antseed_primary', known: { id: 'other-model', providerName: 'Original AntSeed' } },
  { name: 'disabled provider missing this model', pin: 'antseed_disabled', known: { id: 'other-model', providerName: 'Disabled AntSeed', disabled: true } },
  { name: 'retired namespace-bound provider', pin: 'antseed_retired', prefixes: { 'old-as': 'antseed_retired' } },
  { name: 'deleted generated provider ID', pin: 'prov_retired123' },
  { name: 'deleted uppercase generated provider ID', pin: 'PROV_retired123' },
  { name: 'reserved OpenCode ID', pin: 'opencode' },
  { name: 'reserved Cline ID', pin: 'cline' },
]) {
  test(scenario.name + ' never resolves a sibling with an ID-shaped display name', async () => {
    const raw = 'deepseek-4-1', pin = scenario.pin;
    const sibling = { id: raw, source: 'provider', providerId: 'sibling_current', provider: pin, providerName: pin, qualifiedId: 'sibling/' + raw, cost: 'free' };
    const models = [sibling];
    if (scenario.known) models.push({ ...scenario.known, source: 'provider', providerId: pin });
    const ui = createUI({ models, cleanupState: {
      _cachedModels: [sibling],
      _provCache: { sibling_current: { id: 'sibling_current', name: pin, modelIds: [raw], enabled: true } },
      _globalData: { offerings: [{ modelId: raw, providerId: 'sibling_current', delisted: false, providerEnabled: true }] },
    } });
    ui.evaluate('var _tunBaseCfg = ' + JSON.stringify({ providerModelPrefixes: scenario.prefixes || {} }) + ';');
    ui.evaluate('_comboEntries = ' + JSON.stringify([{ model: raw, source: 'provider', provider: pin }]) + '; renderComboEntries();');
    const html = ui.elements.get('comboEntries').innerHTML;
    const badges = html.match(/<span class="combo-entry-badges">([\s\S]*?)<span class="combo-entry-actions">/)?.[1];
    assert.match(badges, /目录中未找到/);
    assert.doesNotMatch(badges, /model-tag free|model-tag pass/);
    const key = 'offering:' + JSON.stringify([raw, 'provider', pin]);
    assert.equal(ui.evaluate('comboEntryKey(_comboEntries[0])'), key);
    assert.equal(ui.evaluate('comboEntryLabel(_comboEntries[0])'), raw);
    assert.equal(ui.evaluate('comboCatalogEntry(_comboEntries[0])'), null);
    const selected = Array.from(html.matchAll(/<option\b([^>]*)>([\s\S]*?)<\/option>/g))
      .find(option => /\bselected\b/.test(option[1]));
    assert.equal(attributes('<option ' + selected[1] + '>').value, key);
    const decoded = JSON.parse(ui.evaluate('JSON.stringify(comboSelectionEntry(comboEntryKey(_comboEntries[0])))'));
    assert.deepEqual(decoded, { model: raw, disabled: false, source: 'provider', provider: pin });
    await ui.evaluate('saveCombo()');
    assert.deepEqual(ui.writes[0].entries, [{ model: raw, disabled: false, source: 'provider', provider: pin }]);

    if (pin !== 'cline' && pin !== 'opencode') {
      ui.evaluate('showComboEditor(' + JSON.stringify(ui.writes[0]) + ');');
      ui.evaluate("_pickerOrder = [comboEntryKey(_comboEntries[0]), comboOfferingKey(_comboCatalog.find(row => row.providerId === 'sibling_current'))];");
      const ref = { id: raw, source: 'provider', providerId: pin };
      ui.evaluate('reconcileRemovedModelDrafts(' + JSON.stringify([ref]) + ', ' + JSON.stringify([ref]) + ');');
      assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_pickerOrder)')), ['offering:' + JSON.stringify([raw, 'provider', 'sibling_current'])]);
    }
  });
}

test('an exact disabled provider offering retains its own metadata despite a colliding sibling name', () => {
  const pin = 'antseed_disabled';
  const ui = createUI({ models: [
    { id: 'deepseek-4-1', source: 'provider', providerId: 'sibling_current', providerName: pin, qualifiedId: 'sibling/deepseek-4-1', cost: 'free' },
    { id: 'deepseek-4-1', source: 'provider', providerId: pin, providerName: 'Original AntSeed', qualifiedId: 'as/deepseek-4-1', disabled: true, cost: 'paid' },
  ] });
  ui.evaluate("_comboEntries = [{model:'deepseek-4-1',source:'provider',provider:'antseed_disabled'}]; renderComboEntries()");
  const badges = ui.elements.get('comboEntries').innerHTML.match(/<span class="combo-entry-badges">([\s\S]*?)<span class="combo-entry-actions">/)?.[1];
  assert.match(badges, /Original AntSeed/);
  assert.match(badges, /模型已停用/);
  assert.doesNotMatch(badges, /model-tag free|目录中未找到/);
  assert.equal(ui.evaluate('comboEntryLabel(_comboEntries[0])'), 'as/deepseek-4-1');
  assert.equal(ui.evaluate('comboEntryKey(_comboEntries[0])'), 'offering:' + JSON.stringify(['deepseek-4-1', 'provider', pin]));
});

test('legacy unique names remain supported while duplicate legacy names remain ambiguous', () => {
  const first = { id: 'deepseek-4-1', source: 'provider', providerId: 'first_instance', providerName: 'prov_Legacy Name', cost: 'free' };
  const entry = { model: 'deepseek-4-1', source: 'provider', provider: 'prov_legacy name' };
  const unique = createUI({ models: [first] });
  unique.evaluate('_comboEntries = ' + JSON.stringify([entry]) + '; renderComboEntries();');
  assert.equal(unique.evaluate('comboCatalogEntry(_comboEntries[0]).providerId'), 'first_instance');
  assert.equal(unique.evaluate('comboEntryKey(_comboEntries[0])'), 'offering:' + JSON.stringify([entry.model, 'provider', 'first_instance']));
  const duplicate = createUI({ models: [first, { ...first, providerId: 'second_instance' }] });
  duplicate.evaluate('_comboEntries = ' + JSON.stringify([entry]) + '; renderComboEntries();');
  assert.equal(duplicate.evaluate('comboCatalogEntry(_comboEntries[0])'), null);
  assert.equal(duplicate.evaluate('comboEntryKey(_comboEntries[0])'), 'offering:' + JSON.stringify([entry.model, 'provider', entry.provider]));
});

test('changing the upstream dropdown repaints the selected provider metadata', () => {
  const ui = createUI({ models: stableCatalog });
  ui.evaluate("_comboEntries = [{model:'deepseek-4-1',disabled:false,source:'zen',provider:''}]; renderComboEntries();");
  const select = ui.elements.get('comboEntries').innerHTML.match(/<span class="combo-entry-source">(<select[\s\S]*?<\/select>)/)?.[1];
  assert.ok(select);
  const option = Array.from(select.matchAll(/<option\b([^>]*)>([\s\S]*?)<\/option>/g)).find(match => decodeHTML(match[2]).includes('AntSeed'));
  assert.ok(option);
  ui.runHandler(attributes(select.match(/<select\b[^>]*>/)[0]).onchange, attributes('<option ' + option[1] + '>').value, false);
  const badges = ui.elements.get('comboEntries').innerHTML.match(/<span class="combo-entry-badges">([\s\S]*?)<span class="combo-entry-actions">/)?.[1];
  assert.match(badges, /AntSeed/);
  assert.doesNotMatch(badges, /OpenCode Zen|opencode/);
  assert.equal(ui.entries()[0].provider, 'antseed_primary');
});

test('same-name provider instances remain independent choices and save distinct pins', async () => {
  const models = [
    { id: 'shared/model', source: 'provider', provider: 'AntSeed', providerName: 'AntSeed', providerId: 'antseed_a', qualifiedId: 'as-a/shared/model' },
    { id: 'shared/model', source: 'provider', provider: 'AntSeed', providerName: 'AntSeed', providerId: 'antseed_b', qualifiedId: 'as-b/shared/model' },
  ];
  const ui = createUI({ models });
  ui.selectOffering('AntSeed', 'as-a/shared/model');
  ui.selectOffering('AntSeed', 'as-b/shared/model');
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  assert.deepEqual(ui.writes[0].entries.map(entry => entry.provider), ['antseed_a', 'antseed_b']);
});

test('upstream choices distinguish provider instances with the same display name', () => {
  const models = [
    { id: 'shared/model', source: 'provider', provider: 'AntSeed', providerName: 'AntSeed', providerId: 'antseed_a', qualifiedId: 'as-a/shared/model' },
    { id: 'shared/model', source: 'provider', provider: 'AntSeed', providerName: 'AntSeed', providerId: 'antseed_b', qualifiedId: 'as-b/shared/model' },
  ];
  const ui = createUI({ models });
  ui.selectOffering('AntSeed', 'as-a/shared/model');
  ui.evaluate('comboPickerConfirm(false)');
  const select = ui.elements.get('comboEntries').innerHTML.match(/<span class="combo-entry-source">(<select[\s\S]*?<\/select>)/)?.[1];
  assert.match(decodeHTML(select), /AntSeed \(antseed_a\)/);
  assert.match(decodeHTML(select), /AntSeed \(antseed_b\)/);
});

test('legacy model-change handlers retain a valid provider-name pin against stable catalog IDs', () => {
  const ui = createUI({ models: stableCatalog });
  ui.evaluate("_comboEntries = [{model:'previous-model',source:'provider',provider:'AntSeed'}]; onComboEntryModelChange(0, 'deepseek-4-1')");
  assert.equal(ui.entries()[0].source, 'provider');
  assert.equal(ui.entries()[0].provider, 'AntSeed');
});

test('legacy automatic entries stay automatic when opened, rendered and saved', async () => {
  const ui = createUI({ models: stableCatalog });
  ui.evaluate("showComboEditor({id:'antseed-combo',name:'Existing auto combo',enabled:true,entries:[{model:'deepseek-4-1'}]})");
  const badges = ui.elements.get('comboEntries').innerHTML.match(/<span class="combo-entry-badges">([\s\S]*?)<span class="combo-entry-actions">/)?.[1];
  assert.match(badges, /自动/);
  assert.doesNotMatch(badges, /AntSeed|OpenCode Zen/);
  await ui.evaluate('saveCombo()');
  assert.deepEqual(ui.writes[0].entries, [{ model: 'deepseek-4-1', disabled: false }]);
});

test('raw model IDs containing @ remain unchanged after provider selection', async () => {
  const raw = 'vendor/model@variant';
  const models = stableCatalog.map(model => ({ ...model, id: raw, qualifiedId: (model.source === 'zen' ? 'op/' : 'as/') + raw }));
  const ui = createUI({ models });
  ui.selectOffering('AntSeed', 'as/' + raw);
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  assert.deepEqual(ui.writes[0].entries, [{ model: raw, disabled: false, source: 'provider', provider: 'antseed_primary' }]);
});

test('reordering and the test editor preserve independently pinned same-ID offerings', async () => {
  const ui = createUI({ models: stableCatalog });
  ui.selectOffering('AntSeed');
  ui.selectOffering('OpenCode Zen');
  ui.evaluate('comboPickerConfirm(false); moveComboEntry(1, -1)');
  await ui.evaluate('saveCombo()');
  await ui.evaluate("testCombo('antseed-combo')");
  ui.evaluate('comboTestToggleEntry(0, true)');
  await ui.evaluate('comboTestSave()');
  assert.deepEqual(ui.writes[1].entries, [
    { model: 'deepseek-4-1', disabled: true, source: 'zen' },
    { model: 'deepseek-4-1', disabled: false, source: 'provider', provider: 'antseed_primary' },
  ]);
  const row = ui.elements.get('comboTestList').innerHTML.match(/<div class="combo-test-row"><span[\s\S]*?<\/div>/)?.[0];
  assert.match(row, /as\/deepseek-4-1/);
  assert.match(row, /AntSeed/);
  assert.doesNotMatch(row, /OpenCode Zen/);
});

test('removing a failed OpenCode test row retains the AntSeed result and badges', async () => {
  const ui = createUI({ models: stableCatalog });
  ui.selectOffering('OpenCode Zen');
  ui.selectOffering('AntSeed');
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  await ui.evaluate("testCombo('antseed-combo')");
  ui.evaluate("_comboTestResults[0].ok = false; _comboTestResults[0].error = 'OpenCode failed'; renderComboTestModal(); comboTestRemoveEntry(0)");
  const html = ui.elements.get('comboTestList').innerHTML;
  assert.match(html, /AntSeed/);
  assert.doesNotMatch(html, /OpenCode Zen|OpenCode failed/);
  assert.match(html, /save-result ok/);
});

test('identical model and provider pins are rejected as true duplicates', async () => {
  const ui = createUI({ models: stableCatalog });
  ui.evaluate("_comboEntries = [{model:'deepseek-4-1',source:'provider',provider:'antseed_primary'}, {model:'deepseek-4-1',source:'provider',provider:'antseed_primary'}]");
  await ui.evaluate('saveCombo()');
  assert.equal(ui.writes.length, 0);
  assert.ok(ui.toasts.some(message => message.includes('重复')));
});

test('delisting cleanup removes only the selected provider offering key', () => {
  const ui = createUI({ models: stableCatalog, cleanupState: {
    _cachedModels: [{ id: 'deepseek-4-1', source: 'zen', provider: 'opencode', delisted: false }],
    _provCache: { antseed_primary: { id: 'antseed_primary', name: 'AntSeed', modelIds: [], enabled: true } },
    _globalData: { offerings: [{ modelId: 'deepseek-4-1', providerId: 'opencode', delisted: false }] },
  } });
  ui.selectOffering('AntSeed');
  ui.selectOffering('OpenCode Zen');
  ui.evaluate("reconcileRemovedModelDrafts([{id:'deepseek-4-1',source:'provider',providerId:'antseed_primary'}], [{id:'deepseek-4-1',source:'provider',providerId:'antseed_primary'}]); comboPickerConfirm(false)");
  assert.deepEqual(ui.entries(), [{ model: 'deepseek-4-1', disabled: false, source: 'zen', provider: '' }]);
});

test('cleanup after toggling same-ID test entries keeps the other provider result', async () => {
  const ui = createUI({ models: stableCatalog, cleanupState: {
    _cachedModels: [{ id: 'deepseek-4-1', source: 'zen', provider: 'opencode', delisted: false }],
    _provCache: { antseed_primary: { id: 'antseed_primary', name: 'AntSeed', modelIds: [], enabled: true } },
    _globalData: { offerings: [{ modelId: 'deepseek-4-1', providerId: 'opencode', delisted: false }] },
  } });
  ui.selectOffering('AntSeed');
  ui.selectOffering('OpenCode Zen');
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  await ui.evaluate("testCombo('antseed-combo')");
  ui.evaluate("comboTestToggleEntry(0, true); reconcileRemovedModelDrafts([{id:'deepseek-4-1',source:'provider',providerId:'antseed_primary'}], [{id:'deepseek-4-1',source:'provider',providerId:'antseed_primary'}])");
  const results = JSON.parse(ui.evaluate('JSON.stringify(_comboTestResults)'));
  assert.equal(results.length, 1);
  assert.equal(results[0].source, 'zen');
  assert.equal(results[0].upstream, 'opencode');
});

test('raw @ model IDs survive base-model cleanup across catalog, entries and dirty chains', () => {
  const raw = 'vendor/model@variant';
  const models = [{ id: raw, source: 'provider', providerId: 'antseed_primary', providerName: 'AntSeed' }];
  const ui = createUI({ models, cleanupState: {
    _cachedModels: [],
    _provCache: { antseed_primary: { id: 'antseed_primary', name: 'AntSeed', modelIds: [raw], enabled: true } },
    _globalData: { offerings: [] },
  } });
  ui.elements.set('settingModelChain', { value: 'vendor/model, ' + raw + ', unsaved@variant', dataset: { dirty: '1' } });
  ui.evaluate("_comboEntries = [{model:'vendor/model',source:'provider',provider:'antseed_primary'}, {model:'vendor/model@variant',source:'provider',provider:'antseed_primary'}, {model:'vendor/model@unlisted',source:'provider',provider:'antseed_primary'}]; reconcileRemovedModelDrafts([{id:'vendor/model',source:'provider',providerId:'antseed_primary'}, {id:'unsaved',source:'provider',providerId:'antseed_primary'}], [{id:'vendor/model',source:'provider',providerId:'antseed_primary'}, {id:'unsaved',source:'provider',providerId:'antseed_primary'}])");
  assert.deepEqual(ui.entries().map(entry => entry.model), [raw, 'vendor/model@unlisted']);
  assert.equal(ui.evaluate('_comboCatalog[0].id'), raw);
  assert.equal(ui.elements.get('settingModelChain').value, raw + ', unsaved@variant');
});

test('an exact removed model ID containing @ is pruned without damaging its base ID', () => {
  const raw = 'vendor/model@variant';
  const ui = createUI({ models: [], cleanupState: {
    _cachedModels: [],
    _provCache: { antseed_primary: { id: 'antseed_primary', name: 'AntSeed', modelIds: ['vendor/model'], enabled: true } },
    _globalData: { offerings: [] },
  } });
  ui.evaluate("_comboEntries = [{model:'vendor/model@variant',source:'provider',provider:'antseed_primary'}, {model:'vendor/model',source:'provider',provider:'antseed_primary'}]; reconcileRemovedModelDrafts([{id:'vendor/model@variant',source:'provider',providerId:'antseed_primary'}], [{id:'vendor/model@variant',source:'provider',providerId:'antseed_primary'}])");
  assert.deepEqual(ui.entries().map(entry => entry.model), ['vendor/model']);
});

test('qualified dirty-chain references are pruned only for the removed provider', () => {
  const ui = createUI({ models: stableCatalog, cleanupState: {
    _cachedModels: [{ id: 'deepseek-4-1', source: 'zen', provider: 'opencode', delisted: false }],
    _provCache: { antseed_primary: { id: 'antseed_primary', name: 'AntSeed', modelIds: [], enabled: true } },
    _globalData: { offerings: [{ modelId: 'deepseek-4-1', providerId: 'opencode', delisted: false }] },
    _tunBaseCfg: { providerModelPrefixes: { as: 'antseed_primary', op: 'opencode' } },
  } });
  ui.elements.set('settingModelChain', { value: 'as/deepseek-4-1, op/deepseek-4-1, deepseek-4-1, future/deepseek-4-1', dataset: { dirty: '1' } });
  ui.evaluate("reconcileRemovedModelDrafts([{id:'deepseek-4-1',source:'provider',providerId:'antseed_primary'}], [{id:'deepseek-4-1',source:'provider',providerId:'antseed_primary'}])");
  assert.equal(ui.elements.get('settingModelChain').value, 'op/deepseek-4-1, deepseek-4-1, future/deepseek-4-1');
});

test('generated picker handlers round-trip quoted model IDs and provider names', async () => {
  const raw = 'vendor/model"@variant<new>\\quoted';
  const provider = "Ant'Seed \"primary\"";
  const ui = createUI({ models: [{ id: raw, source: 'provider', providerId: 'quoted_provider', providerName: provider, provider, qualifiedId: 'as/' + raw }] });
  ui.selectOffering(provider, 'as/' + raw);
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  assert.deepEqual(ui.writes[0].entries, [{ model: raw, disabled: false, source: 'provider', provider: 'quoted_provider' }]);
});

test('a pinned AntSeed entry uses AntSeed metadata instead of the first OpenCode row', () => {
  const ui = createUI();
  ui.evaluate("_comboEntries = [{model:'deepseek-4-1',disabled:false,source:'provider',provider:'AntSeed'}]; renderComboEntries();");
  const html = ui.elements.get('comboEntries').innerHTML;
  const badges = html.match(/<span class="combo-entry-badges">([\s\S]*?)<span class="combo-entry-actions">/)?.[1];
  assert.ok(badges, 'rendered entry contains its provider badges');
  assert.match(badges, /AntSeed/);
  assert.doesNotMatch(badges, /OpenCode Zen|opencode/);
});

test('the same model selected at AntSeed and OpenCode remains two independently pinned entries', async () => {
  const ui = createUI();
  ui.selectOffering('AntSeed');
  ui.selectOffering('OpenCode Zen');
  ui.evaluate('comboPickerConfirm(false)');
  await ui.evaluate('saveCombo()');
  assert.equal(ui.writes.length, 1, 'different provider offerings are accepted by the save controller');
  assert.deepEqual(ui.writes[0].entries, [
    { model: 'deepseek-4-1', disabled: false, source: 'provider', provider: 'AntSeed' },
    { model: 'deepseek-4-1', disabled: false, source: 'zen' },
  ]);
});

test('the model dropdown retains the AntSeed offering and selecting it establishes its pin', () => {
  const ui = createUI();
  ui.evaluate('addComboEntry()');
  const html = ui.elements.get('comboEntries').innerHTML;
  const select = html.match(/<span class="combo-entry-select">(<select[\s\S]*?<\/select>)/)?.[1];
  assert.ok(select, 'the entry renders its real model dropdown');
  const option = Array.from(select.matchAll(/<option\b([^>]*)>([\s\S]*?)<\/option>/g)).find(match => decodeHTML(match[2]).includes('AntSeed'));
  assert.ok(option, 'the same-ID OpenCode row must not remove the AntSeed option');
  const handler = attributes(select.match(/<select\b[^>]*>/)[0]).onchange;
  ui.runHandler(handler, attributes('<option ' + option[1] + '>').value, false);
  assert.deepEqual(ui.entries(), [
    { model: 'deepseek-4-1', disabled: false, source: 'provider', provider: 'AntSeed' },
  ]);
});

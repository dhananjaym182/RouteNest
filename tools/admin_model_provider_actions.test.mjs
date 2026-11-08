// Run with: node --test tools/admin_model_provider_actions.test.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const rawID = 'deepseek-v4.1-flash-free-for-stakers';
const instances = [
  { id: 'cline', name: 'Cline', runtimeBuiltin: true },
  { id: 'opencode', name: 'OpenCode Zen', runtimeBuiltin: true },
  { id: 'antseed_primary', name: 'AntSeed', deletable: true },
];
const offerings = [
  { modelId: rawID, providerId: 'antseed_primary', providerName: 'AntSeed', qualifiedId: 'as/' + rawID },
  { modelId: rawID, providerId: 'opencode', providerName: 'OpenCode Zen', qualifiedId: 'op/' + rawID },
  { modelId: 'vendor/model@reasoning', providerId: 'antseed_primary', providerName: 'AntSeed', qualifiedId: 'as/vendor/model@reasoning' },
];
const models = [
  { id: rawID, source: 'provider', provider: 'AntSeed', cost: 'free', tierSignal: 'id-free' },
  { id: rawID, source: 'zen', provider: 'opencode', cost: 'free' },
  { id: 'vendor/model@reasoning', source: 'provider', provider: 'AntSeed', cost: 'free' },
];
const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]));

function createUI({ providerInstances = instances, providerOfferings = offerings, canWrite = true, confirm = true, remove, beforeRead } = {}) {
  const elements = new Map();
  const field = () => ({
    innerHTML: '', textContent: '', value: '', hidden: false, disabled: false,
    style: { display: 'none' }, dataset: {}, isConnected: true,
    classList: { toggle: () => {} },
    focus() { this.focused = true; },
    querySelector: () => null,
    querySelectorAll: () => [],
  });
  for (const id of [
    'modelsList', 'modelStatsBar', 'modelFilterProvider', 'modelFilterStatus',
    'modelProviderActionModal', 'modelProviderActionTitle', 'modelProviderActionNote',
    'modelProviderActionProviderField', 'modelProviderActionProvider', 'modelProviderActionSearchRow',
    'modelProviderActionSearch', 'modelProviderActionList', 'modelProviderActionCount',
    'modelProviderActionError', 'modelProviderActionSubmit', 'modelProviderActionCancel',
    'modelProviderActionSelectAll', 'modelProviderActionClear',
    'confirmModal', 'diffModal',
    'modelSyncModal', 'modelSyncOverlay',
  ]) elements.set(id, field());
  let currentOfferings = structuredClone(providerOfferings);
  const providers = providerInstances.filter(p => p.deletable).map(p => ({
    ...p, modelIds: currentOfferings.filter(o => o.providerId === p.id).map(o => o.modelId),
  }));
  const calls = [], editors = [], confirmations = [], refreshes = [], reconciliations = [];
  const context = vm.createContext({
    TextEncoder,
    _: id => elements.get(id) || null,
    t: value => value, esc: escapeHTML, escAttr: escapeHTML,
    uiIcon: () => '', listEmptyState: (_icon, value) => value, ICON_MODELS: '',
    _tierSignals: { 'id-free': 'id contains free segment' },
    formatTokenCount: value => String(value),
    document: { addEventListener: () => {}, querySelectorAll: () => [], activeElement: null },
    localStorage: { getItem: () => null, setItem: () => {} },
    toast: () => {}, refreshSaveBar: () => {},
    adminHasPermission: () => canWrite,
    _globalData: { instances: providerInstances, offerings: currentOfferings },
    _activeTabName: 'models', _tabNavigationGeneration: 0,
    setupProviderInstance: id => editors.push(id),
    api: async (method, path, body) => {
      calls.push({ method, path, body: body ? JSON.parse(JSON.stringify(body)) : undefined });
      if (method === 'GET' && beforeRead) await beforeRead(path);
      if (method === 'GET' && path === '/provider-instances') return { data: { instances: providerInstances } };
      if (method === 'GET' && path === '/model-offerings') return { data: { offerings: currentOfferings } };
      if (method === 'GET' && path === '/providers') return { data: { providers } };
      if (method === 'GET' && path === '/models') return { data: { models } };
      if (method === 'GET' && path === '/providers/sync-status') return { data: { providers: {} } };
      if (method === 'POST' && path === '/models/remove') {
        const data = remove ? await remove(body) : { removed: body.models, skipped: [], errors: [] };
        currentOfferings = currentOfferings.filter(o => !data.removed.some(ref => ref.id === o.modelId && ref.providerId === o.providerId));
        return { data };
      }
      throw new Error('Unexpected request: ' + method + ' ' + path);
    },
  });
  for (const asset of ['13_models.js', '13_model_sync.js', '13_model_provider_actions.js']) {
    const url = new URL('../internal/admin/ui/js/' + asset, import.meta.url);
    vm.runInContext(readFileSync(url, 'utf8'), context, { filename: asset });
  }
  Object.assign(context, {
    showConfirm: async message => { confirmations.push(message); return confirm; },
    loadModels: async () => refreshes.push('models'),
    loadProviders: async () => refreshes.push('providers'),
    loadGlobal: async () => { refreshes.push('offerings'); context._globalData.offerings = currentOfferings; },
    loadCombos: async () => refreshes.push('combos'),
    loadConfig: async options => refreshes.push(options),
    reconcileRemovedModelDrafts: (completed, removedRefs) => reconciliations.push(JSON.parse(JSON.stringify({ completed, removedRefs }))),
  });
  const run = source => vm.runInContext(source, context);
  run('_cachedModels = ' + JSON.stringify(models));
  const button = key => ({ ...field(), closest: () => ({ dataset: { key } }) });
  return { run, context, elements, calls, editors, confirmations, refreshes, reconciliations, button };
}

test('Catalog names appear in raw ID, matching aliases, inline badge order', () => {
  const ui = createUI();
  const html = ui.run('renderModelRow(mergeModelsById(_cachedModels)[0])');
  assert.ok(html.indexOf('model-row-id') < html.indexOf('model-provider-names'));
  assert.ok(html.indexOf('model-provider-names') < html.indexOf('model-row-tags'));
  assert.ok(!html.includes('class="hint-xs">Provider 模型名'));
  assert.ok(html.includes('as/' + rawID));
  assert.ok(!html.includes('as/vendor/model@reasoning'));
  assert.ok(html.includes('title="' + rawID + '"'), 'Full original ID remains available');
});

test('Provider header exposes Edit and Remove beside bulk controls', () => {
  const ui = createUI();
  ui.run('renderModelList()');
  const html = ui.elements.get('modelsList').innerHTML;
  assert.ok(html.includes('openModelProviderAction(this, &quot;edit&quot;)'));
  assert.ok(html.includes('openModelProviderAction(this, &quot;remove&quot;)'));
  assert.ok(html.indexOf('全部停用') < html.indexOf('编辑 Provider'));
});

test('Provider groups resolve exact stable builtin and custom instances', () => {
  const ui = createUI();
  for (const [key, ids] of [
    ['src:cline', ['cline']], ['src:zen', ['opencode']], ['src:prov:AntSeed', ['antseed_primary']],
    ['src:prov:Missing', []],
  ]) assert.deepEqual(JSON.parse(ui.run('JSON.stringify(modelGroupProviders(' + JSON.stringify(key) + ').map(p => p.id))')), ids);
});

test('A single-provider header opens the existing editor with its stable ID', async () => {
  const ui = createUI();
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "edit")');
  assert.deepEqual(ui.editors, ['antseed_primary']);
  assert.equal(ui.calls.filter(c => c.method === 'POST').length, 0);
});

test('Builtin provider headers open their own existing settings surface', async () => {
  for (const [key, id] of [['src:cline', 'cline'], ['src:zen', 'opencode']]) {
    const ui = createUI();
    ui.context.actionButton = ui.button(key);
    await ui.run('openModelProviderAction(actionButton, "edit")');
    assert.deepEqual(ui.editors, [id]);
  }
});

test('A displayed header retains its stable identity after provider rename or deletion', async () => {
  const providerInstances = instances.map(p => p.id === 'antseed_primary' ? { ...p, name: 'Renamed AntSeed' } : p)
    .concat({ id: 'unrelated_provider', name: 'AntSeed', deletable: true });
  const ui = createUI({ providerInstances });
  ui.context.actionButton = { ...ui.button('src:prov:AntSeed'), closest: () => ({ dataset: {
    key: 'src:prov:AntSeed', providerIds: JSON.stringify(['antseed_primary']),
  } }) };
  await ui.run('openModelProviderAction(actionButton, "edit")');
  assert.deepEqual(ui.editors, ['antseed_primary']);
  ui.context.actionButton.closest = () => ({ dataset: {
    key: 'src:prov:AntSeed', providerIds: JSON.stringify(['retired_provider']),
  } });
  await ui.run('openModelProviderAction(actionButton, "edit")');
  assert.deepEqual(ui.editors, ['antseed_primary'], 'A deleted provider must not fall through to the same-name replacement');
});

test('Duplicate display names require choosing a provider before editing', async () => {
  const ui = createUI({ providerInstances: instances.concat({ id: 'antseed_second', name: 'AntSeed', deletable: true }) });
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "edit")');
  assert.deepEqual(ui.editors, []);
  assert.equal(ui.elements.get('modelProviderActionProvider').value, '');
  assert.equal(ui.elements.get('modelProviderActionSubmit').disabled, true);
  ui.run('selectModelProviderAction("antseed_second")');
  await ui.run('submitModelProviderAction()');
  assert.deepEqual(ui.editors, ['antseed_second']);
});

test('Removal choices contain only target offerings including literal at IDs', async () => {
  const ui = createUI();
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  const refs = JSON.parse(ui.run('JSON.stringify(_modelProviderAction.models)'));
  assert.deepEqual(refs.map(ref => [ref.id, ref.source, ref.providerId]), [
    [rawID, 'provider', 'antseed_primary'], ['vendor/model@reasoning', 'provider', 'antseed_primary'],
  ]);
  assert.equal(refs.some(ref => ref.providerId === 'opencode'), false);
  const html = ui.elements.get('modelProviderActionList').innerHTML;
  assert.ok(html.includes('data-model-id="vendor/model@reasoning"'));
});

test('Search selects only shown models and clear removes the whole selection', async () => {
  const ui = createUI();
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.elements.get('modelProviderActionSearch').value = 'as/vendor/model@';
  ui.run('renderModelProviderActionList(); selectAllModelProviderRemovals()');
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(Array.from(_modelProviderAction.selected))')), ['vendor/model@reasoning']);
  ui.run('clearModelProviderRemovals()');
  assert.equal(ui.run('_modelProviderAction.selected.size'), 0);
});

test('Cancellation performs no removal writes and retains the selection', async () => {
  const ui = createUI({ confirm: false });
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.run('toggleModelProviderRemoval(' + JSON.stringify(rawID) + ', true)');
  await ui.run('submitModelProviderAction()');
  assert.equal(ui.calls.filter(c => c.method === 'POST').length, 0);
  assert.equal(ui.run('_modelProviderAction.selected.size'), 1);
  assert.equal(ui.run('_modelProviderAction.busy'), false);
});

test('Bulk removal submits stable provider-scoped refs and refreshes related views', async () => {
  const ui = createUI();
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.run('selectAllModelProviderRemovals()');
  await ui.run('submitModelProviderAction()');
  const writes = ui.calls.filter(c => c.method === 'POST');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].path, '/models/remove');
  assert.deepEqual(writes[0].body.models, [
    { id: rawID, source: 'provider', providerId: 'antseed_primary' },
    { id: 'vendor/model@reasoning', source: 'provider', providerId: 'antseed_primary' },
  ]);
  assert.ok(ui.context._globalData.offerings.some(o => o.providerId === 'opencode' && o.modelId === rawID));
  assert.ok(ui.refreshes.includes('models') && ui.refreshes.includes('providers') && ui.refreshes.includes('combos'));
  assert.ok(ui.refreshes.some(value => value.preserveEdits === true));
  assert.equal(ui.reconciliations.length, 1);
  assert.equal(ui.elements.get('modelProviderActionModal').style.display, 'none');
});

test('Partial removal errors keep failed selections available for retry', async () => {
  const ui = createUI({ remove: body => ({
    removed: body.models.slice(0, 1), skipped: [], errors: [{ model: body.models[1], error: 'fixture persistence error' }],
  }) });
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.run('selectAllModelProviderRemovals()');
  await ui.run('submitModelProviderAction()');
  assert.equal(ui.elements.get('modelProviderActionModal').style.display, 'flex');
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(Array.from(_modelProviderAction.selected))')), ['vendor/model@reasoning']);
  assert.ok(ui.elements.get('modelProviderActionError').textContent.includes('fixture persistence error'));
  assert.equal(ui.run('_modelProviderAction.busy'), false);
});

test('Read-only administrator cannot open editor/removal or submit mutations', async () => {
  const ui = createUI({ canWrite: false });
  ui.run('renderModelList()');
  assert.ok(!ui.elements.get('modelsList').innerHTML.includes('openModelProviderAction'));
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  await ui.run('submitModelProviderAction()');
  assert.equal(ui.calls.length, 0);
});

test('Provider declarations remain removable when an offering projection is absent', async () => {
  const ui = createUI();
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.run('_globalData.offerings = []');
  const refs = JSON.parse(ui.run('JSON.stringify(modelProviderRemovalChoices(_modelProviderAction.instance, _modelProviderAction.providers))'));
  assert.equal(refs.length, 2);
  assert.ok(refs.every(ref => ref.providerId === 'antseed_primary'));
});

test('Manual Cline models and Zen seed records are scoped to their builtin owner', async () => {
  const ui = createUI();
  ui.run('_cachedModels = [{id:"manual@revision", source:"custom", custom:true}, {id:"offline-seed", source:"seed", provider:"opencode"}]');
  const cline = JSON.parse(ui.run('JSON.stringify(modelProviderRemovalChoices({id:"cline"}, []))'));
  const zen = JSON.parse(ui.run('JSON.stringify(modelProviderRemovalChoices({id:"opencode"}, []))'));
  assert.ok(cline.some(ref => ref.id === 'manual@revision' && ref.source === 'remote' && ref.providerId === 'cline'));
  assert.ok(!cline.some(ref => ref.id === 'offline-seed'));
  assert.ok(zen.some(ref => ref.id === 'offline-seed' && ref.source === 'zen' && ref.providerId === 'opencode'));
  assert.ok(!zen.some(ref => ref.id === 'manual@revision'));
});

test('Overlapping dialog opens and removal submissions do not duplicate writes', async () => {
  const ui = createUI();
  ui.context.firstButton = ui.button('src:prov:AntSeed');
  ui.context.secondButton = ui.button('src:zen');
  await ui.run('Promise.all([openModelProviderAction(firstButton, "remove"), openModelProviderAction(secondButton, "remove")])');
  assert.equal(ui.run('_modelProviderAction.instance.id'), 'antseed_primary');
  assert.equal(ui.calls.filter(c => c.path === '/provider-instances').length, 1);
  ui.run('selectAllModelProviderRemovals()');
  await ui.run('Promise.all([submitModelProviderAction(), submitModelProviderAction()])');
  assert.equal(ui.calls.filter(c => c.method === 'POST').length, 1);
});

test('Large removal selections use bounded batches with the same stable owner', async () => {
  const providerOfferings = Array.from({ length: 205 }, (_, index) => ({
    modelId: 'model-' + String(index).padStart(3, '0'), providerId: 'antseed_primary', qualifiedId: 'as/model-' + index,
  }));
  const ui = createUI({ providerOfferings });
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.run('selectAllModelProviderRemovals()');
  await ui.run('submitModelProviderAction()');
  const writes = ui.calls.filter(c => c.method === 'POST');
  assert.deepEqual(writes.map(c => c.body.models.length), [100, 100, 5]);
  assert.ok(writes.flatMap(c => c.body.models).every(ref => ref.providerId === 'antseed_primary' && ref.source === 'provider'));
});

test('Manual removal clears only its provider notice and keeps sibling delisting notices', async () => {
  const ui = createUI();
  ui.context.actionButton = ui.button('src:prov:AntSeed');
  await ui.run('openModelProviderAction(actionButton, "remove")');
  ui.run('_modelSyncGroups.set("provider:antseed_primary", {label:"AntSeed", added:new Set(), removed:new Set([' + JSON.stringify(rawID) + '])});' +
    '_modelSyncGroups.set(JSON.stringify(["zen", ""]), {label:"OpenCode Zen", added:new Set(), removed:new Set([' + JSON.stringify(rawID) + '])})');
  await ui.run('removeModelProviderOfferings([' + JSON.stringify(rawID) + '])');
  assert.equal(ui.run('_modelSyncGroups.has("provider:antseed_primary")'), false);
  assert.equal(ui.run('_modelSyncGroups.get(JSON.stringify(["zen", ""])).removed.has(' + JSON.stringify(rawID) + ')'), true);
});

test('Navigation invalidates delayed edit and removal opens including an away/back round trip', async () => {
  for (const mode of ['edit', 'remove']) {
    for (const returnToModels of [false, true]) {
      let release;
      const gate = new Promise(resolve => { release = resolve; });
      const ui = createUI({ beforeRead: path => path === '/provider-instances' ? gate : undefined });
      ui.context.actionButton = ui.button('src:prov:AntSeed');
      const pending = ui.run('openModelProviderAction(actionButton, ' + JSON.stringify(mode) + ')');
      ui.context._activeTabName = returnToModels ? 'models' : 'security';
      ui.context._tabNavigationGeneration += returnToModels ? 2 : 1;
      release();
      await pending;
      assert.deepEqual(ui.editors, []);
      assert.equal(ui.elements.get('modelProviderActionModal').style.display, 'none');
      assert.equal(ui.run('_modelProviderActionLoading'), false);
    }
  }
});

test('Generated row actions preserve quotes/backslashes and cannot execute model text', () => {
  const ui = createUI();
  const args = [];
  for (const name of ['testModelRow', 'editModelMeta', 'deleteModel', 'toggleModel']) ui.context[name] = (...values) => args.push({ name, values });
  const decode = value => value.replace(/&(?:quot|amp|lt|gt);|&#39;/g, entity => ({
    '&quot;': '"', '&amp;': '&', '&lt;': '<', '&gt;': '>', '&#39;': "'",
  }[entity]));
  for (const id of ["vendor/o'model", 'vendor/back\\slash', "vendor/');globalThis.handlerSentinel=true;//"]) {
    const html = ui.run('renderModelRow(mergeModelsById(' + JSON.stringify([{ id, custom: true, cost: 'free' }]) + ')[0])');
    const handlers = Array.from(html.matchAll(/(?:onclick|onchange)="([^"]*)"/g), match => decode(match[1]))
      .filter(handler => /^(testModelRow|editModelMeta|deleteModel|toggleModel)\(/.test(handler));
    assert.equal(handlers.length, 4);
    ui.context.testControl = { checked: false };
    for (const handler of handlers) ui.run('(function(){' + handler + '}).call(testControl)');
    assert.ok(args.splice(0).every(call => call.values[0] === id));
    assert.equal(ui.context.handlerSentinel, undefined);
  }
});

test('Provider editor follow-up reads cannot open after leaving the requested page', async () => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const ui = createUI({ beforeRead: path => path === '/providers' ? gate : undefined });
  vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/08_providers_detect.js', import.meta.url), 'utf8'), ui.context);
  const editors = [];
  ui.context.showProviderEditor = provider => editors.push(provider.id);
  const pending = ui.run('editProvider("antseed_primary")');
  ui.context._activeTabName = 'security';
  ui.context._tabNavigationGeneration++;
  release();
  await pending;
  assert.deepEqual(editors, []);
});

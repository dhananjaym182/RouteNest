// Run with: node --test tools/admin_model_sync.test.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const assets = [
  '05_settings.js', '06_providers_custom.js', '07_providers_detail.js', '08_providers_detect.js',
  '09_providers_pickers.js', '10_providers_import.js', '11_upstreams.js', '12_combo_catalog.js', '12_combos.js',
  '13_models.js', '13_model_sync.js', '13_model_cleanup.js', '18_global.js',
];

function createUI({ models = [], providers = [], instances = providers, sync = {}, lastSync = {}, recheck = {}, zenSync = {}, offerings = [], combos = [], modelRead, storage = new Map(), remove, canRemove = true, canReadCredentials = true, preview, batchRead, config } = {}) {
  const elements = new Map();
  const field = (value = '') => ({
    value, textContent: '', style: { display: 'none' }, dataset: {}, rows: [],
    classList: { contains: () => false, toggle: () => {}, add: () => {}, remove: () => {} },
    focus: () => {}, querySelectorAll: () => [],
    get innerHTML() { return this.html || ''; },
    set innerHTML(value) { this.html = value; this.rows = []; },
  });
  for (const id of ['modelsList', 'providerCooldowns', 'modelSyncModal', 'modelSyncOverlay', 'provDetailRecheckOut']) {
    elements.set(id, field());
  }
  const queryAll = selector => {
    if (selector === '#tunAliasesEditor .alias-row') return elements.get('tunAliasesEditor')?.rows || [];
    return [];
  };
  const addAlias = (alias, target, provider) => {
    const values = { '.se-alias': field(alias), '.se-target': field(target), '.se-provider': field(provider) };
    const row = { querySelector: selector => values[selector], remove: () => {
      const rows = elements.get('tunAliasesEditor').rows;
      rows.splice(rows.indexOf(row), 1);
    } };
    elements.get('tunAliasesEditor').rows.push(row);
    return row;
  };
  const calls = [];
  const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
  const context = vm.createContext({
    TextEncoder,
    _: id => elements.get(id) || null,
    document: { addEventListener: () => {}, querySelector: () => null, querySelectorAll: queryAll },
    addTestAlias: addAlias,
    localStorage: {
      getItem: key => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, String(value)),
      removeItem: key => storage.delete(key),
    },
    t: value => value, tx: value => value,
    esc: escapeHTML,
    escAttr: escapeHTML,
    uiIcon: () => '',
    formatDateTime: value => value || '',
    friendlyTestError: error => error.message,
    renderTestResultHTML: (_ok, value) => value,
    toast: () => {},
    adminHasPermission: permission => permission === 'provider.write' ? canRemove : permission === 'credential.write' ? canReadCredentials : true,
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      if ((path === '/providers' || path === '/config') && !canRemove || (path === '/credentials' || path === '/keys') && !canReadCredentials) throw new Error('permission_denied');
      if (path === '/models') return modelRead ? modelRead() : { data: { models, lastSync } };
      if (path === '/providers') return { data: { providers } };
      if (path === '/providers/sync-status') return { data: { providers: sync } };
      if (path === '/providers/recheck') return { data: recheck };
      if (path === '/opencode/models/sync') return { data: zenSync };
      if (path === '/models/sync') return { data: lastSync };
      if (path === '/models/delisted/remove') return { data: await remove(body) };
      if (path === '/models/import/preview') return { data: await preview(body) };
      if (path.startsWith('/models/import/batch?')) return { data: await batchRead(path) };
      if (path === '/config') return { data: config };
      if (path === '/model-offerings') return { data: { offerings } };
      if (path === '/combos') return { data: { combos, modelCatalog: models, reservedAliases: [] } };
      if (path === '/provider-instances') return { data: { instances } };
      if (['/credentials', '/provider-state', '/egress-profiles'].includes(path)) return { data: {} };
      throw new Error('Unexpected API request: ' + method + ' ' + path);
    },
  });
  for (const asset of assets) {
    const url = new URL('../internal/admin/ui/js/' + asset, import.meta.url);
    vm.runInContext(readFileSync(url, 'utf8'), context, { filename: asset });
  }
  // Rendering outside the sync popup is unrelated to these controller tests.
  vm.runInContext(`
    renderModelList = () => {};
    renderGlobal = () => {};
    renderProvCooldownBar = () => '';
    refreshProvDetailIfOpen = () => {};
    renderProvPickerList = () => {};
    renderImportPreviewList = () => {};
    renderComboEntries = () => {};
    renderCombosList = () => {};
    renderComboTestRows = () => {};
    renderComboTestModal = () => {};
    setSaveBarCtx = () => {};
    refreshSaveBar = () => {};
    updateExcludedMatchCounts = () => {};
    readErrorRuleRows = () => [];
    addAliasRow = addTestAlias;
  `, context);
  if (!config) vm.runInContext('loadConfig = async () => {};', context);
  return {
    calls, elements,
    addField: (id, value = '') => { const element = field(value); elements.set(id, element); return element; },
    evaluate: source => vm.runInContext(source, context),
    run: async source => {
      await vm.runInContext(source, context);
      await new Promise(resolve => setImmediate(resolve));
    },
    popup: () => elements.get('modelSyncModal').innerHTML,
    popupOpen: () => elements.get('modelSyncOverlay').style.display === 'flex',
  };
}

const provider = { id: 'p1', name: 'Example Provider', modelIds: ['retired-model'], enabled: true };
const removedModel = { id: 'retired-model', source: 'provider', provider: provider.name, delisted: true };

test('acknowledged delistings stay dismissed after a full page refresh', async () => {
  const storage = new Map();
  const first = createUI({ models: [removedModel], storage });
  await first.run('loadModels()');
  assert.equal(first.popupOpen(), true, 'first detection opens the notice');
  await first.run('closeModelSyncModal()');
  const refreshed = createUI({ models: [removedModel], storage });
  await refreshed.run('loadModels()');
  assert.equal(refreshed.popupOpen(), false, 'full refresh must preserve acknowledgement');
});

test('refresh only lists newly delisted models and preserves provider acknowledgements', async () => {
  const storage = new Map();
  const providers = [provider];
  const sync = { p1: { delistedModels: ['retired-model'] } };
  const first = createUI({ providers, sync, storage });
  await first.run('loadProviders(); closeModelSyncModal()');
  const refreshed = createUI({ providers, sync, storage });
  await refreshed.run('loadProviders()');
  assert.equal(refreshed.popupOpen(), false);
  sync.p1.delistedModels.push('newly-retired-model');
  await refreshed.run('loadProviders()');
  assert.equal(refreshed.popupOpen(), true);
  assert.match(refreshed.popup(), /newly-retired-model/);
  assert.doesNotMatch(refreshed.popup(), />retired-model</);
});

test('startup catalogue loading cannot clear persisted provider acknowledgements', async () => {
  const storage = new Map();
  const providers = [provider];
  const sync = { p1: { delistedModels: ['retired-model'] } };
  const first = createUI({ providers, sync, storage });
  await first.run('loadProviders()');
  await first.run('closeModelSyncModal()');
  const refreshed = createUI({ providers, sync, storage, models: [] });
  await refreshed.run('loadModels()');
  await refreshed.run('loadProviders()');
  assert.equal(refreshed.popupOpen(), false, 'startup prewarm cannot forget provider notice');
});

test('a delisted provider catalogue row cannot replay a saved instance notice on reload', async () => {
  const storage = new Map();
  const providers = [provider];
  const sync = { p1: { delistedModels: ['retired-model'] } };
  const first = createUI({ providers, sync, models: [removedModel], storage });
  await first.run('loadProviders()');
  await first.run('loadModels()');
  await first.run('closeModelSyncModal()');
  const refreshed = createUI({ providers, sync, models: [removedModel], storage });
  await refreshed.run('loadModels()');
  await refreshed.run('loadProviders()');
  assert.equal(refreshed.popupOpen(), false, 'catalogue and sync status refer to the same saved provider notice');
});

test('renaming a provider does not replay its saved delisting during startup', async () => {
  const storage = new Map();
  const providers = [{ ...provider }];
  const sync = { p1: { delistedModels: ['retired-model'] } };
  const models = [{ ...removedModel }];
  const first = createUI({ providers, sync, models, storage });
  await first.run('loadProviders()');
  await first.run('loadModels()');
  await first.run('closeModelSyncModal()');
  providers[0].name = models[0].provider = 'Renamed Provider';
  const refreshed = createUI({ providers, sync, models, storage });
  await refreshed.run('loadModels()');
  assert.equal(refreshed.popupOpen(), false, 'the stable provider ID owns the acknowledgement');
});

test('provider recovery persists across reload and a later delisting warns again', async () => {
  const storage = new Map();
  const providers = [provider];
  const sync = { p1: { delistedModels: ['retired-model'] } };
  const first = createUI({ providers, sync, storage });
  await first.run('loadProviders(); closeModelSyncModal()');
  sync.p1.delistedModels = [];
  const recovered = createUI({ providers, sync, storage });
  await recovered.run('loadProviders()');
  assert.equal(recovered.popupOpen(), false);
  sync.p1.delistedModels.push('retired-model');
  const delistedAgain = createUI({ providers, sync, storage });
  await delistedAgain.run('loadProviders()');
  assert.equal(delistedAgain.popupOpen(), true);
});

test('a saved Cline sync result is not replayed on full reload', async () => {
  const storage = new Map();
  const lastSync = { changed: true, syncedAt: '2026-11-07T00:00:00Z', removed: ['retired-cline-model'] };
  const first = createUI({ lastSync, storage });
  await first.run('loadModels(); closeModelSyncModal()');
  const refreshed = createUI({ lastSync, storage });
  await refreshed.run('loadModels()');
  assert.equal(refreshed.popupOpen(), false);
});

test('the notice offers individual and bulk removal with provider-scoped targets', async () => {
  const ui = createUI({ providers: [provider], sync: { p1: { delistedModels: ['retired-model', 'second-retired-model'] } } });
  await ui.run('loadProviders()');
  assert.equal(ui.popup().match(/data-model-remove=/g).length, 2);
  assert.match(ui.popup(), /removeDelistedModels\(\)/);
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_modelSyncRemovalTargets)')), [
    { id: 'retired-model', source: 'provider', providerId: 'p1' },
    { id: 'second-retired-model', source: 'provider', providerId: 'p1' },
  ]);
});

test('single removal sends one scoped mutation and preserves the other notice', async () => {
  const sync = { p1: { delistedModels: ['retired-model', 'second-retired-model'] } };
  const ui = createUI({ providers: [provider], sync, remove: body => {
    sync.p1.delistedModels = sync.p1.delistedModels.filter(id => id !== body.models[0].id);
    return { removed: body.models, skipped: [], errors: [] };
  } });
  await ui.run('loadProviders()');
  await ui.run('removeDelistedModels([0])');
  const writes = ui.calls.filter(call => call.method === 'POST');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].path, '/models/delisted/remove');
  assert.deepEqual(JSON.parse(JSON.stringify(writes[0].body)), { models: [{ id: 'retired-model', source: 'provider', providerId: 'p1' }] });
  assert.equal(ui.popupOpen(), true);
  assert.doesNotMatch(ui.popup(), />retired-model</);
  assert.match(ui.popup(), /second-retired-model/);
});

test('Remove all batches Cline and Zen once and closes the completed notice', async () => {
  const models = [
    { id: 'same-model', source: 'remote', delisted: true },
    { id: 'same-model', source: 'zen', delisted: true },
  ];
  const ui = createUI({ models, remove: body => {
    models.splice(0);
    return { removed: body.models, skipped: [], errors: [] };
  } });
  await ui.run('loadModels()');
  await ui.run('removeDelistedModels()');
  const writes = ui.calls.filter(call => call.method === 'POST');
  assert.equal(writes.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(writes[0].body.models)), [
    { id: 'same-model', source: 'remote' }, { id: 'same-model', source: 'zen' },
  ]);
  assert.equal(ui.popupOpen(), false);
});

test('Remove all handles more than 500 notices within API count and byte limits', async () => {
  const models = Array.from({ length: 501 }, (_, i) => ({ id: 'retired-' + i, source: 'remote', delisted: true }));
  const ui = createUI({ models, remove: body => {
    assert.ok(body.models.length <= 500, 'API batch count limit');
    assert.ok(Buffer.byteLength(JSON.stringify(body)) <= 128 * 1024, 'API body size limit');
    const ids = new Set(body.models.map(model => model.id));
    models.splice(0, models.length, ...models.filter(model => !ids.has(model.id)));
    return { removed: body.models, skipped: [], errors: [] };
  } });
  await ui.run('loadModels()');
  await ui.run('removeDelistedModels()');
  assert.ok(ui.calls.filter(call => call.method === 'POST').length > 1);
  assert.equal(models.length, 0);
  assert.equal(ui.popupOpen(), false);
});

test('large escaped model IDs are packed within the removal body limit', async () => {
  const models = Array.from({ length: 80 }, (_, i) => ({ id: 'quoted"'.repeat(250) + i, source: 'remote', delisted: true }));
  const ui = createUI({ models, remove: body => {
    assert.ok(Buffer.byteLength(JSON.stringify(body)) <= 128 * 1024);
    const ids = new Set(body.models.map(model => model.id));
    models.splice(0, models.length, ...models.filter(model => !ids.has(model.id)));
    return { removed: body.models, skipped: [], errors: [] };
  } });
  await ui.run('loadModels()');
  await ui.run('removeDelistedModels()');
  assert.equal(models.length, 0);
  assert.equal(ui.popupOpen(), false);
});

test('successful earlier batches stay removed when a later batch fails', async () => {
  const models = Array.from({ length: 501 }, (_, i) => ({ id: 'retired-' + i, source: 'remote', delisted: true }));
  let attempts = 0;
  const ui = createUI({ models, remove: body => {
    if (++attempts === 2) throw new Error('later batch store failure');
    const ids = new Set(body.models.map(model => model.id));
    models.splice(0, models.length, ...models.filter(model => !ids.has(model.id)));
    return { removed: body.models, skipped: [], errors: [] };
  } });
  await ui.run('loadModels()');
  await ui.run('removeDelistedModels()');
  assert.equal(attempts, 2);
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /later batch store failure/);
  assert.doesNotMatch(ui.popup(), />retired-0</);
  assert.match(ui.popup(), /retired-500/);
});

test('a failed removal stays visible for retry and duplicate submits are ignored', async () => {
  let rejectRemoval;
  const ui = createUI({ models: [{ id: 'retired-cline-model', source: 'remote', delisted: true }],
    remove: () => new Promise((_resolve, reject) => { rejectRemoval = reject; }),
  });
  await ui.run('loadModels()');
  const pending = ui.evaluate('removeDelistedModels()');
  await new Promise(resolve => setImmediate(resolve));
  await ui.run('removeDelistedModels()');
  assert.equal(ui.calls.filter(call => call.method === 'POST').length, 1);
  rejectRemoval(new Error('strict store save failed'));
  await pending;
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /strict store save failed/);
  assert.match(ui.popup(), /retired-cline-model/);
});

test('viewers receive notices without model-removal controls', async () => {
  const ui = createUI({ models: [removedModel], canRemove: false });
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
  assert.doesNotMatch(ui.popup(), /data-model-remove|removeDelistedModels/);
  await ui.run('removeDelistedModels()');
  assert.equal(ui.calls.some(call => call.method === 'POST'), false);
});

test('viewer model reads use public provider identities and preserve scoped notice acknowledgements', async () => {
  const storage = new Map();
  const sync = { p1: { delistedModels: ['retired-model'] } };
  const instances = [{ id: 'cline', name: 'Cline' }, { id: 'p1', name: provider.name }];
  const ui = createUI({ models: [removedModel], instances, sync, storage, canRemove: false, canReadCredentials: false });
  await ui.run('loadModels()');
  assert.equal(ui.calls.some(call => call.path === '/providers'), false, 'viewer must not request protected provider declarations');
  assert.equal(ui.calls.some(call => call.path === '/provider-instances'), true);
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /Example Provider/);
  await ui.run('closeModelSyncModal(); checkModelDelistings()');
  assert.equal(ui.popupOpen(), false);

  const refreshed = createUI({
    models: [{ ...removedModel, provider: 'Renamed Provider' }],
    instances: [{ id: 'p1', name: 'Renamed Provider' }], sync, storage,
    canRemove: false, canReadCredentials: false,
  });
  await refreshed.run('loadModels()');
  assert.equal(refreshed.popupOpen(), false, 'renaming must retain the stable instance acknowledgement');
  assert.equal(refreshed.calls.some(call => call.path === '/providers'), false);
});

test('viewer global catalog retains qualified offerings without requesting credential inventory', async () => {
  const offerings = [{ modelId: 'shared-model', providerId: 'p1', qualifiedId: 'as/shared-model', providerName: provider.name }];
  const instances = [{ id: 'p1', name: provider.name }];
  const ui = createUI({ offerings, instances, canRemove: false, canReadCredentials: false });
  await ui.run('loadGlobal(false)');
  assert.equal(ui.calls.some(call => call.path === '/credentials'), false, 'viewer must not request protected credential inventory');
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_globalData.offerings)')), offerings);
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_globalData.instances)')), instances);
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_globalData.credentials)')), []);
});

test('viewer config loading leaves drafts intact without requesting protected routing settings', async () => {
  const ui = createUI({ config: { modelChain: ['stored-model'] }, canRemove: false, canReadCredentials: false });
  const chain = ui.addField('settingModelChain', 'unsaved-model');
  await ui.run('loadConfig()');
  assert.equal(ui.calls.some(call => call.path === '/config'), false, 'viewer must not request protected routing settings');
  assert.equal(chain.value, 'unsaved-model');
});

test('viewer startup key loading does not request protected API keys', async () => {
  const ui = createUI({ canRemove: false, canReadCredentials: false });
  ui.addField('keysList');
  await ui.run('loadKeys()');
  assert.equal(ui.calls.some(call => call.path === '/keys'), false, 'viewer must not request protected API keys');
});

test('repeated bulk errors are summarized once while failed model rows remain available for retry', async () => {
  const models = Array.from({ length: 100 }, (_, n) => ({ id: 'retired-' + n, source: 'remote', delisted: true }));
  const ui = createUI({ models, remove: body => ({ removed: [], skipped: [], errors: body.models.map(model => ({ model, error: 'Cannot persist model cleanup' })) }) });
  await ui.run('loadModels()');
  await ui.run('removeDelistedModels()');
  assert.equal(ui.popup().match(/Cannot persist model cleanup/g).length, 1);
  assert.equal(ui.popup().match(/data-model-remove=/g).length, 100);
  assert.match(ui.popup(), /onclick="removeDelistedModels\(\)"/);
});

test('completed cleanup refreshes the stored combo cache before another editor can reopen it', async () => {
  const models = [{ id: 'retired-model', source: 'remote', delisted: true }];
  const combos = [{ id: 'saved-combo', entries: [{ model: 'retired-model' }, { model: 'live-model' }] }];
  const ui = createUI({ models, combos, remove: body => {
    models.splice(0);
    combos[0].entries = [{ model: 'live-model' }];
    return { removed: body.models, skipped: [], errors: [] };
  } });
  await ui.run('loadCombos()');
  await ui.run('loadModels()');
  await ui.run('removeDelistedModels()');
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_comboList[0].entries)')), [{ model: 'live-model' }]);
});

for (const [label, action] of [
  ['provider list', "recheckProvider('p1')"],
  ['provider details', "_provDetailOpenId = 'p1'; provDetailRecheck()"],
]) {
  test(label + ' warns when rechecking detects a delisted model', async () => {
    const ui = createUI({
      models: [removedModel], providers: [provider],
      recheck: { lastCheckedMs: 1700000000000, delistedMarked: 1, delistedModels: ['retired-model'] },
      sync: { p1: { lastCheckedMs: 1700000000000, delistedMarked: 1, delistedModels: ['retired-model'] } },
    });
    await ui.run(action);
    assert.equal(ui.popupOpen(), true, 'Delisted models must open the shared sync popup');
    assert.match(ui.popup(), /retired-model/);
    assert.match(ui.popup(), /Example Provider/);
  });
}

test('manual Cline sync shows the delisted model', async () => {
  const ui = createUI({ lastSync: {
    syncedAt: '2026-11-07T00:00:00Z', changed: true, removed: ['retired-cline-model'],
  } });
  await ui.run('syncModels()');
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /retired-cline-model/);
});

test('manual OpenCode Zen sync shows the delisted model', async () => {
  const ui = createUI({ zenSync: {
    syncedAt: '2026-11-07T00:00:00Z', changed: true, removed: ['retired-zen-model'],
  } });
  await ui.run('syncOcModels()');
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /retired-zen-model/);
});

test('catalogue loading warns for every source, including matching model IDs', async () => {
  const ui = createUI({ models: [
    { id: 'retired-model', source: 'remote', provider: 'model-vendor', delisted: true },
    { id: 'retired-model', source: 'zen', provider: 'opencode', delisted: true },
    removedModel,
  ] });
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
  for (const source of ['Cline', 'OpenCode Zen', 'Example Provider']) assert.match(ui.popup(), new RegExp(source));
  assert.equal(ui.popup().match(/retired-model/g).length, 3);
});

test('refreshing unchanged delisted flags does not repeat the popup', async () => {
  const ui = createUI({ models: [removedModel] });
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
  await ui.run('closeModelSyncModal(); loadModels()');
  assert.equal(ui.popupOpen(), false);
});

test('later delistings and relisted models delisted again each warn', async () => {
  const models = [];
  const ui = createUI({ models });
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), false);
  models.push({ ...removedModel });
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
  await ui.run('closeModelSyncModal()');
  models[0].delisted = false;
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), false);
  models[0].delisted = true;
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
});

test('later Cline sync results are not suppressed by the first notice', async () => {
  const lastSync = { syncedAt: '2026-11-07T00:00:00Z', changed: true, removed: ['first-model'] };
  const ui = createUI({ lastSync });
  await ui.run('loadModels()');
  await ui.run('closeModelSyncModal()');
  Object.assign(lastSync, { syncedAt: '2026-11-07T01:00:00Z', removed: ['second-model'] });
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /second-model/);
  assert.doesNotMatch(ui.popup(), /first-model/);
});

test('loading provider sync status warns about background delistings', async () => {
  const ui = createUI({ models: [removedModel], providers: [provider], sync: { p1: { delistedMarked: 1, delistedModels: ['retired-model'] } } });
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /Example Provider/);
});

test('same-ID provider delisting stays provider-scoped and warns again after recovery', async () => {
  const providers = [
    { id: 'alpha', name: 'Alpha', modelIds: ['shared/model'], enabled: true },
    { id: 'beta', name: 'Beta', modelIds: ['shared/model'], enabled: true },
  ];
  const sync = {
    alpha: { delistedMarked: 1, delistedModels: ['shared/model'] },
    beta: { delistedMarked: 0, delistedModels: [] },
  };
  const ui = createUI({
    models: [{ id: 'shared/model', source: 'provider', provider: 'Beta', delisted: false }],
    providers, sync,
  });

  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /Alpha/);
  assert.doesNotMatch(ui.popup(), />Beta</);
  assert.equal(ui.popup().match(/shared\/model/g).length, 1);

  await ui.run('closeModelSyncModal(); loadProviders()');
  assert.equal(ui.popupOpen(), false, 'unchanged provider-scoped state must not repeat');

  sync.alpha.delistedMarked = 0;
  sync.alpha.delistedModels = [];
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), false, 'recovery is an authoritative empty provider state');

  sync.alpha.delistedMarked = 1;
  sync.alpha.delistedModels = ['shared/model'];
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), true, 'a later Alpha delisting must warn again');
  assert.match(ui.popup(), /Alpha/);
});

test('same-named provider instances keep independent popup lifecycle keys', async () => {
  const providers = [
    { id: 'alpha', name: 'Same Name', modelIds: ['shared/model'], enabled: true },
    { id: 'beta', name: 'Same Name', modelIds: ['shared/model'], enabled: true },
  ];
  const sync = {
    alpha: { delistedMarked: 1, delistedModels: ['shared/model'] },
    beta: { delistedMarked: 1, delistedModels: ['shared/model'] },
  };
  const ui = createUI({ providers, sync });
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), true);
  assert.equal(ui.popup().match(/shared\/model/g).length, 2, 'each provider instance needs its own popup group');
  assert.match(ui.popup(), /Same Name \(alpha\)/);
  assert.match(ui.popup(), /Same Name \(beta\)/);

  await ui.run('closeModelSyncModal()');
  sync.alpha.delistedMarked = 0;
  sync.alpha.delistedModels = [];
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), false, 'Beta was already acknowledged and Alpha recovered');

  sync.alpha.delistedMarked = 1;
  sync.alpha.delistedModels = ['shared/model'];
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), true, 'Alpha must warn again independently of same-named Beta');
  assert.equal(ui.popup().match(/shared\/model/g).length, 1);
  assert.match(ui.popup(), /Same Name \(alpha\)/);
});

test('loading global offerings uses provider sync state when the legacy row stays active', async () => {
  const ui = createUI({
    models: [{ id: 'shared/model', source: 'provider', provider: 'Beta', delisted: false }],
    providers: [
      { id: 'alpha', name: 'Alpha', modelIds: ['shared/model'], enabled: true },
      { id: 'beta', name: 'Beta', modelIds: ['shared/model'], enabled: true },
    ],
    sync: {
      alpha: { delistedMarked: 1, delistedModels: ['shared/model'] },
      beta: { delistedMarked: 0, delistedModels: [] },
    },
    offerings: [
      { providerId: 'alpha', modelId: 'shared/model', delisted: true },
      { providerId: 'beta', modelId: 'shared/model', delisted: false },
    ],
  });
  await ui.run('loadGlobal(false)');
  assert.equal(ui.popupOpen(), true);
  assert.match(ui.popup(), /Alpha/);
  assert.match(ui.popup(), /shared\/model/);
});

test('background checks warn without rendering the model table or writing config', async () => {
  const models = [];
  const ui = createUI({ models });
  await ui.run("renderModelList = () => { throw new Error('Background checks must not redraw the model table'); }; checkModelDelistings()");
  models.push(removedModel);
  await ui.run('checkModelDelistings()');
  assert.equal(ui.popupOpen(), true);
  assert.equal(ui.calls.every(call => call.method === 'GET'), true);
  assert.equal(ui.elements.get('modelsList').textContent, '');
});

test('notices arriving from another source preserve the open popup', async () => {
  const ui = createUI({ models: [removedModel], zenSync: { changed: true, removed: ['retired-zen-model'] } });
  await ui.run('loadModels()');
  await ui.run('syncOcModels()');
  assert.match(ui.popup(), /retired-model/);
  assert.match(ui.popup(), /Example Provider/);
  assert.match(ui.popup(), /retired-zen-model/);
  assert.match(ui.popup(), /OpenCode Zen/);
});

test('a Cline delisting acknowledged after a background check is not repeated on navigation', async () => {
  const ui = createUI({
    models: [{ id: 'retired-cline-model', source: 'remote', delisted: true }],
    lastSync: { changed: true, syncedAt: '2026-11-07T00:00:00Z', removed: ['retired-cline-model'] },
  });
  await ui.run('checkModelDelistings()');
  assert.equal(ui.popupOpen(), true);
  await ui.run('closeModelSyncModal(); loadModels()');
  assert.equal(ui.popupOpen(), false);
});

test('acknowledging manual sync while catalogue refresh is pending does not repeat the notice', async () => {
  let resolveRead;
  const ui = createUI({
    lastSync: { changed: true, syncedAt: '2026-11-07T00:00:00Z', removed: ['retired-cline-model'] },
    modelRead: () => new Promise(resolve => { resolveRead = resolve; }),
  });
  const pending = ui.evaluate('syncModels()');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ui.popupOpen(), true);
  await ui.run('closeModelSyncModal()');
  resolveRead({ data: { models: [{ id: 'retired-cline-model', source: 'remote', delisted: true }] } });
  await pending;
  assert.equal(ui.popupOpen(), false);
});

test('custom providers named like built-ins keep separate delisting notices', async () => {
  const ui = createUI({ models: [
    { id: 'shared-model', source: 'remote', delisted: true },
    { id: 'shared-model', source: 'zen', delisted: true },
    { id: 'shared-model', source: 'provider', provider: 'Cline', delisted: true },
    { id: 'shared-model', source: 'provider', provider: 'OpenCode Zen', delisted: true },
  ] });
  await ui.run('loadModels()');
  assert.equal(ui.popup().match(/shared-model/g).length, 4);
});

test('an older catalogue response cannot reset an acknowledged newer delisting', async () => {
  let resolveOld;
  let reads = 0;
  const ui = createUI({ modelRead: () => ++reads === 1
    ? new Promise(resolve => { resolveOld = resolve; })
    : { data: { models: [removedModel] } },
  });
  const old = ui.evaluate('loadModels()');
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), true);
  await ui.run('closeModelSyncModal()');
  resolveOld({ data: { models: [] } });
  await old;
  await ui.run('loadModels()');
  assert.equal(ui.popupOpen(), false);
});

test('background checks retry failed reads and skip hidden tabs', async () => {
  let reads = 0;
  const ui = createUI({ modelRead: () => {
    if (++reads === 1) throw new Error('temporary read failure');
    return { data: { models: [removedModel] } };
  } });
  await ui.run('document.hidden = true; checkModelDelistings()');
  assert.equal(reads, 0);
  await ui.run('document.hidden = false; checkModelDelistings()');
  assert.equal(ui.popupOpen(), false);
  await ui.run('checkModelDelistings()');
  assert.equal(ui.popupOpen(), true);
});

test('new provider models remain pending review without a delisting notice or import', async () => {
  const ui = createUI({ providers: [provider], sync: { p1: { newQueued: 2, batchId: 'pending-batch' } } });
  await ui.run('loadProviders()');
  assert.equal(ui.popupOpen(), false);
  assert.equal(ui.calls.some(call => call.method === 'POST'), false);
  assert.equal(ui.calls.some(call => call.path === '/models'), false);
});

test('a late provider picker response cannot populate another provider editor or its cache', async () => {
  let resolvePreview;
  const ui = createUI({ preview: () => new Promise(resolve => { resolvePreview = resolve; }) });
  ui.addField('provTestResult');
  ui.addField('provModels');
  ui.addField('provPickerSearch');
  ui.addField('provPickerModal');
  ui.addField('providerEditor').classList.contains = () => true;
  await ui.run("_editingProviderId = 'p1'");
  const pending = ui.evaluate('fetchProviderModels()');
  await new Promise(resolve => setImmediate(resolve));
  await ui.run("_editingProviderId = 'p2'");
  resolvePreview({ batch: { providerId: 'p1', items: [{ modelId: 'old-provider-model', tier: 'free', action: 'add' }] } });
  await pending;
  assert.equal(ui.elements.get('provPickerModal').style.display, 'none');
  assert.equal(ui.evaluate('Object.keys(_importPreviewCache).length'), 0);
  assert.equal(ui.evaluate('_provPickerModels.length'), 0);
});

test('a late import preview cannot open after switching provider editors', async () => {
  let resolvePreview;
  const ui = createUI({ preview: () => new Promise(resolve => { resolvePreview = resolve; }) });
  ui.addField('importPreviewModal');
  await ui.run("_editingProviderId = 'p1'");
  const pending = ui.evaluate('previewModelImport()');
  await new Promise(resolve => setImmediate(resolve));
  await ui.run("_editingProviderId = 'p2'");
  resolvePreview({ batch: { providerId: 'p1', items: [{ modelId: 'old-provider-model', tier: 'free', action: 'add' }] } });
  await pending;
  assert.equal(ui.elements.get('importPreviewModal').style.display, 'none');
  assert.equal(ui.evaluate('_importBatch'), null);
  assert.equal(ui.evaluate('Object.keys(_importPreviewCache).length'), 0);
});

function prepareConfigFields(ui) {
  for (const id of [
    'settingModelChain', 'settingStrategy', 'settingDefModel', 'localIPsRow', 'listenWarn',
    'tunRetryRounds', 'tunRetryCreds', 'tunRetryInterval', 'tunRetryCooldown', 'tunRetryFailover',
    'tunKeepalive', 'tunBootstrapRetries', 'tunFirstContentTimeout', 'tunSuppressKeepalive',
    'tunAffinityEnabled', 'tunAffinityTTL', 'tunMaxBodyMB', 'tunPassthrough',
    'tunNonstreamKeepalive', 'tunExcluded', 'tunAliasesEditor', 'tunErrorRulesEditor',
  ]) ui.addField(id);
}

test('cleanup config refresh preserves dirty tuning values and chain while refreshing server baselines', async () => {
  const config = { retry: { maxRounds: 2 }, modelChain: ['live-model'], modelAliases: [] };
  const ui = createUI({ config });
  prepareConfigFields(ui);
  await ui.run('fillTuning({ retry: { maxRounds: 2 } })');
  ui.elements.get('tunRetryRounds').value = '7';
  ui.elements.get('settingModelChain').value = 'live-model, unsaved-model';
  ui.elements.get('settingModelChain').dataset.dirty = '1';
  await ui.run('loadConfig({ preserveEdits: true })');
  assert.equal(ui.elements.get('tunRetryRounds').value, '7');
  assert.equal(ui.elements.get('settingModelChain').value, 'live-model, unsaved-model');
  assert.equal(ui.evaluate('JSON.parse(_tunBaseJson).retry.maxRounds'), 2);
  assert.equal(ui.evaluate('tuningDirty()'), true, 'unrelated edit is still a pending edit');
  await ui.run('discardTuning()');
  assert.equal(ui.elements.get('tunRetryRounds').value, '2');
});

test('ordinary config reload continues to discard edits explicitly', async () => {
  const ui = createUI({ config: { retry: { maxRounds: 2 }, modelChain: ['live-model'] } });
  prepareConfigFields(ui);
  await ui.run('fillTuning({ retry: { maxRounds: 2 } })');
  ui.elements.get('tunRetryRounds').value = '7';
  await ui.run('loadConfig()');
  assert.equal(ui.elements.get('tunRetryRounds').value, '2');
  assert.equal(ui.elements.get('settingModelChain').value, 'live-model');
  assert.equal(ui.evaluate('tuningDirty()'), false);
});

function prepareProviderFields(ui) {
  for (const id of [
    'provName', 'provBaseURL', 'provKey', 'provPriority', 'provTimeout', 'provRpmLimit',
    'provDailyLimit', 'provTokenLimit', 'provTokenWindow', 'provProxyUrl', 'provWeight',
    'provModels', 'provHeaders', 'provEnabled', 'provFree', 'provTestResult',
    'provPickerSearch', 'provPickerModal', 'importPreviewModal',
  ]) ui.addField(id);
  ui.addField('providerEditor').classList.contains = () => true;
  ui.evaluate('var _provConnSel = [];');
}

test('completed removal reconciles provider save payload, discard baseline, picker and import drafts', async () => {
  const ui = createUI({ providers: [{ ...provider, modelIds: ['kept-model'] }], offerings: [] });
  prepareProviderFields(ui);
  await ui.run('loadProviders(); closeModelSyncModal()');
  ui.elements.get('provModels').value = 'retired-model, kept-model';
  ui.elements.get('provName').value = 'Example Provider';
  await ui.run(`
    _editingProviderId = 'p1';
    _provBaseData = { id: 'p1', name: 'Example Provider', modelIds: ['retired-model', 'kept-model'] };
    rebaselineProvForm();
    _provPickerModels = ['retired-model', 'new-model'];
    _provPickerOrder = ['retired-model', 'new-model'];
    _provPickerExisting = { 'retired-model': true, 'kept-model': true };
    _provPickerTiers = { 'retired-model': 'free', 'new-model': 'pass' };
    _importBatch = { providerId: 'p1', items: [{ modelId: 'retired-model' }, { modelId: 'new-model' }] };
    importPreviewCacheSet('id:p1', _importBatch);
    importPreviewCacheSet('id:p2', { providerId: 'p2', items: [{ modelId: 'retired-model' }] });
  `);
  ui.elements.get('provName').value = 'Unsaved name';
  ui.elements.get('provModels').value += ', unsaved-model';
  await ui.run(`reconcileRemovedModelDrafts(
    [{ id: 'retired-model', source: 'provider', providerId: 'p1' }],
    [{ id: 'retired-model', source: 'provider', providerId: 'p1' }]
  )`);
  assert.equal(ui.evaluate('serializeProvForm().models'), 'kept-model, unsaved-model');
  assert.equal(ui.elements.get('provName').value, 'Unsaved name');
  assert.equal(ui.evaluate('provFormDirty()'), true);
  assert.equal(ui.evaluate('JSON.parse(_provBaseJson).models'), 'kept-model');
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_provBaseData.modelIds)')), ['kept-model']);
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_provPickerOrder)')), ['new-model']);
  assert.equal(ui.evaluate('_provPickerExisting["retired-model"]'), undefined);
  assert.equal(ui.evaluate('_provPickerTiers["retired-model"]'), undefined);
  assert.equal(ui.evaluate('_importBatch === null || !_importBatch.items.some(i => i.modelId === "retired-model")'), true);
  assert.equal(ui.evaluate('importPreviewCacheGet("id:p1") === null || !importPreviewCacheGet("id:p1").items.some(i => i.modelId === "retired-model")'), true);
  assert.equal(ui.evaluate('importPreviewCacheGet("id:p2").items[0].modelId'), 'retired-model', 'another provider preview is preserved');
});

test('combo cleanup preserves live shared IDs and same-name provider pins', async () => {
  const providers = [
    { id: 'p1', name: 'Shared Provider', modelIds: [] },
    { id: 'p2', name: 'Shared Provider', modelIds: ['shared-model'] },
  ];
  const ui = createUI({ providers, models: [{ id: 'shared-model', source: 'provider', provider: 'Shared Provider' }], offerings: [
    { modelId: 'shared-model', providerId: 'p2', available: true, delisted: false },
  ] });
  await ui.run('loadProviders(); loadModels(); loadGlobal(false); closeModelSyncModal()');
  await ui.run(`
    _comboEntries = [
      { model: 'shared-model', source: 'provider', provider: 'p1' },
      { model: 'shared-model', source: 'provider', provider: 'p2' },
      { model: 'shared-model', source: 'provider', provider: 'Shared Provider' },
      { model: 'shared-model' },
      { model: 'shared-model', source: 'cline' },
      { model: 'unsaved-model' }
    ];
    _comboTestCopy = { id: 'draft-combo', name: 'Unsaved combo name', entries: _comboEntries.map(e => ({ ...e })) };
    reconcileRemovedModelDrafts([
      { id: 'shared-model', source: 'provider', providerId: 'p1' },
      { id: 'shared-model', source: 'remote' }
    ], [
      { id: 'shared-model', source: 'provider', providerId: 'p1' },
      { id: 'shared-model', source: 'remote' }
    ]);
  `);
  const expected = [
    { model: 'shared-model', source: 'provider', provider: 'p2' },
    { model: 'shared-model', source: 'provider', provider: 'Shared Provider' },
    { model: 'shared-model' }, { model: 'unsaved-model' },
  ];
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_comboEntries)')), expected);
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(_comboTestCopy.entries)')), expected);
  assert.equal(ui.evaluate('_comboTestCopy.name'), 'Unsaved combo name');
});

test('alias and dirty chain cleanup remove stale pins without resetting unrelated tuning edits', async () => {
  const config = { retry: { maxRounds: 2 }, modelChain: ['shared-model'], modelAliases: [
    { alias: 'live-alias', target: 'shared-model', provider: 'p2' },
  ] };
  const ui = createUI({ config, providers: [{ id: 'p1', name: 'Example Provider', modelIds: [] }, { id: 'p2', name: 'Other Provider', modelIds: ['shared-model'] }],
    models: [{ id: 'shared-model', source: 'provider', provider: 'Other Provider' }],
    offerings: [{ modelId: 'shared-model', providerId: 'p2', delisted: false }],
  });
  prepareConfigFields(ui);
  await ui.run(`loadProviders(); loadModels(); loadGlobal(false); closeModelSyncModal();
    fillTuning({ retry: { maxRounds: 2 }, modelAliases: [
      { alias: 'retired-alias', target: 'retired-model', provider: 'p1' },
      { alias: 'live-alias', target: 'shared-model', provider: 'p2' }
    ] });
    addAliasRow('unsaved-alias', 'unsaved-model', '');
  `);
  ui.elements.get('tunRetryRounds').value = '7';
  ui.elements.get('settingModelChain').value = 'retired-model, shared-model@p1, shared-model, shared-model@p2, unsaved-model';
  ui.elements.get('settingModelChain').dataset.dirty = '1';
  await ui.run(`loadConfig({ preserveEdits: true });
    reconcileRemovedModelDrafts([
      { id: 'retired-model', source: 'provider', providerId: 'p1' },
      { id: 'shared-model', source: 'provider', providerId: 'p1' }
    ], [
      { id: 'retired-model', source: 'provider', providerId: 'p1' },
      { id: 'shared-model', source: 'provider', providerId: 'p1' }
    ]);
  `);
  assert.equal(ui.elements.get('settingModelChain').value, 'shared-model, shared-model@p2, unsaved-model');
  assert.equal(ui.elements.get('tunRetryRounds').value, '7');
  assert.deepEqual(JSON.parse(ui.evaluate('JSON.stringify(readAliasRows())')), [
    { alias: 'live-alias', target: 'shared-model', provider: 'p2' },
    { alias: 'unsaved-alias', target: 'unsaved-model', provider: '' },
  ]);
  assert.equal(ui.evaluate('JSON.parse(_tunBaseJson).retry.maxRounds'), 2);
  assert.equal(ui.evaluate('JSON.parse(_tunBaseJson).modelAliases.some(a => a.target === "retired-model")'), false);
  assert.equal(ui.evaluate('_tunBaseCfg.modelAliases.some(a => a.target === "retired-model")'), false);
  assert.equal(ui.evaluate('tuningDirty()'), true);
});

for (const operation of ['fetchProviderModels()', 'previewModelImport()']) {
  test(operation + ' continues to show current discovery results in an open editor', async () => {
    const ui = createUI({ preview: () => ({ batch: { providerId: 'p1', items: [{ modelId: 'live-model', tier: 'free', action: 'add' }] } }) });
    prepareProviderFields(ui);
    await ui.run("_editingProviderId = 'p1'");
    await ui.run(operation);
    assert.equal(ui.evaluate('importPreviewCacheGet("id:p1").items[0].modelId'), 'live-model');
    assert.equal(ui.elements.get(operation.startsWith('fetch') ? 'provPickerModal' : 'importPreviewModal').style.display, 'flex');
  });

  test(operation + ' discards discovery when its editor closes before the response', async () => {
    let resolvePreview;
    const ui = createUI({ preview: () => new Promise(resolve => { resolvePreview = resolve; }) });
    prepareProviderFields(ui);
    await ui.run("_editingProviderId = 'p1'");
    const pending = ui.evaluate(operation);
    await new Promise(resolve => setImmediate(resolve));
    ui.elements.get('providerEditor').classList.contains = () => false;
    resolvePreview({ batch: { providerId: 'p1', items: [{ modelId: 'old-model', tier: 'free', action: 'add' }] } });
    await pending;
    assert.equal(ui.evaluate('Object.keys(_importPreviewCache).length'), 0);
    assert.equal(ui.elements.get('provPickerModal').style.display, 'none');
    assert.equal(ui.elements.get('importPreviewModal').style.display, 'none');
  });

  test(operation + ' discards a stale response that arrives after completed removal', async () => {
    let resolvePreview;
    const ui = createUI({ providers: [{ ...provider, modelIds: [] }], preview: () => new Promise(resolve => { resolvePreview = resolve; }) });
    prepareProviderFields(ui);
    await ui.run("loadProviders(); _editingProviderId = 'p1'; closeModelSyncModal()");
    const pending = ui.evaluate(operation);
    await new Promise(resolve => setImmediate(resolve));
    await ui.run(`reconcileRemovedModelDrafts([{ id: 'retired-model', source: 'provider', providerId: 'p1' }], [{ id: 'retired-model', source: 'provider', providerId: 'p1' }])`);
    resolvePreview({ batch: { providerId: 'p1', items: [{ modelId: 'retired-model', tier: 'free', action: 'add' }] } });
    await pending;
    assert.equal(ui.elements.get('provPickerModal').style.display, 'none');
    assert.equal(ui.elements.get('importPreviewModal').style.display, 'none');
    assert.equal(ui.evaluate('Object.keys(_importPreviewCache).length'), 0);
    assert.equal(ui.evaluate('_importBatch'), null);
  });
}

test('recovered skipped models keep their drafts, while absent retry targets are reconciled', async () => {
  const ui = createUI({ providers: [{ ...provider, modelIds: ['live-model'] }], models: [
    { id: 'live-model', source: 'provider', provider: provider.name },
  ], offerings: [{ modelId: 'live-model', providerId: 'p1', delisted: false }] });
  prepareProviderFields(ui);
  await ui.run("loadProviders(); loadModels(); loadGlobal(false); _editingProviderId = 'p1'; closeModelSyncModal()");
  ui.elements.get('provModels').value = 'live-model, absent-model, unsaved-model';
  await ui.run(`reconcileRemovedModelDrafts([{ id: 'live-model', source: 'provider', providerId: 'p1' }], [])`);
  assert.equal(ui.elements.get('provModels').value, 'live-model, absent-model, unsaved-model');
  assert.equal(ui.evaluate('providerModelRemovalEpoch("p1")'), 0);
  await ui.run(`reconcileRemovedModelDrafts([{ id: 'absent-model', source: 'provider', providerId: 'p1' }], [])`);
  assert.equal(ui.elements.get('provModels').value, 'live-model, unsaved-model');
  assert.ok(ui.evaluate('providerModelRemovalEpoch("p1")') > 0);
});

test('a pending automatic batch cannot reopen removed models after cleanup', async () => {
  let resolveBatch;
  const ui = createUI({ providers: [{ ...provider, modelIds: [] }], batchRead: () => new Promise(resolve => { resolveBatch = resolve; }) });
  ui.addField('importPreviewModal');
  await ui.run('loadProviders(); closeModelSyncModal()');
  const pending = ui.evaluate("openAutoBatch('old-batch')");
  await new Promise(resolve => setImmediate(resolve));
  await ui.run(`reconcileRemovedModelDrafts([{ id: 'retired-model', source: 'provider', providerId: 'p1' }], [{ id: 'retired-model', source: 'provider', providerId: 'p1' }])`);
  resolveBatch({ batch: { providerId: 'p1', items: [{ modelId: 'retired-model', action: 'add' }] } });
  await pending;
  assert.equal(ui.elements.get('importPreviewModal').style.display, 'none');
  assert.equal(ui.evaluate('_importBatch'), null);
});

// Run with: node --test tools/admin_model_sync.test.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const assets = [
  '06_providers_custom.js', '07_providers_detail.js', '08_providers_detect.js',
  '11_upstreams.js', '13_models.js', '18_global.js',
];

function createUI({ models = [], providers = [], sync = {}, lastSync = {}, recheck = {}, zenSync = {}, offerings = [], modelRead } = {}) {
  const elements = new Map();
  for (const id of ['modelsList', 'providersList', 'modelSyncModal', 'modelSyncOverlay', 'provDetailRecheckOut']) {
    elements.set(id, { innerHTML: '', textContent: '', style: { display: 'none' } });
  }
  const calls = [];
  const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    document: { addEventListener: () => {} },
    localStorage: { getItem: () => null },
    t: value => value,
    esc: escapeHTML,
    escAttr: escapeHTML,
    uiIcon: () => '',
    formatDateTime: value => value || '',
    friendlyTestError: error => error.message,
    renderTestResultHTML: (_ok, value) => value,
    toast: () => {},
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      if (path === '/models') return modelRead ? modelRead() : { data: { models, lastSync } };
      if (path === '/providers') return { data: { providers } };
      if (path === '/providers/sync-status') return { data: { providers: sync } };
      if (path === '/providers/recheck') return { data: recheck };
      if (path === '/opencode/models/sync') return { data: zenSync };
      if (path === '/models/sync') return { data: lastSync };
      if (path === '/model-offerings') return { data: { offerings } };
      if (['/provider-instances', '/credentials', '/provider-state', '/egress-profiles'].includes(path)) return { data: {} };
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
    provCardHTML = () => '';
    refreshProvDetailIfOpen = () => {};
  `, context);
  return {
    calls, elements,
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

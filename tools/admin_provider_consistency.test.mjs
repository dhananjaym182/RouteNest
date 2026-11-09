// Run with: node --test tools/admin_provider_consistency.test.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const provider = { id: 'primary', name: 'Primary', modelIds: ['free-model'] };
const models = [
  { id: 'free-model', source: 'provider', provider: 'Primary', cost: 'free' },
  { id: 'paid-model', source: 'provider', provider: 'Primary', cost: 'pass' },
];

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function createUI({ providers = [provider], providerRead, deleteError, toggleError, confirm = true } = {}) {
  const list = { innerHTML: '' };
  const calls = [], closed = [], rendered = [], catalogRefreshes = [];
  let offerings = models.map(model => ({ modelId: model.id, providerId: provider.id, qualifiedId: 'p/' + model.id, disabled: false }));
  const context = vm.createContext({
    _: id => id === 'providersList' ? list : null,
    document: { addEventListener: () => {}, querySelectorAll: () => [] },
    localStorage: { getItem: () => null },
    t: value => value, esc: value => String(value), uiIcon: () => '',
    ICON_PROVIDERS: '', listEmptyState: (_icon, value) => value,
    showConfirm: async () => confirm,
    toast: () => {}, closeDrawer: id => closed.push(id),
    invalidateImportPreviewCache: () => {},
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      if (path === '/providers') return providerRead ? providerRead() : { data: { providers } };
      if (path === '/providers/sync-status') return { data: {} };
      if (path === '/providers/delete') {
        if (deleteError) throw new Error(deleteError);
        return { data: {} };
      }
      if (path === '/models/toggle') {
        if (toggleError) throw new Error(toggleError);
        offerings = offerings.map(offering => body.ids.includes(offering.modelId) ? { ...offering, disabled: body.disabled } : offering);
        return { data: {} };
      }
      if (path === '/model-offerings') return { data: { offerings } };
      if (path === '/provider-instances') return { data: { instances: providers } };
      if (['/credentials', '/provider-state', '/egress-profiles'].includes(path)) return { data: {} };
      throw new Error('Unexpected request: ' + method + ' ' + path);
    },
  });
  for (const asset of ['06_providers_custom.js', '07_providers_detail.js', '08_providers_detect.js', '13_models.js', '18_global.js']) {
    vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/' + asset, import.meta.url), 'utf8'), context, { filename: asset });
  }
  Object.assign(context, {
    showConfirm: async () => confirm,
    renderProviderSyncBanner: () => {}, renderProvCooldownBar: () => '',
    provCardHTML: value => value.id, renderProvDetail: value => rendered.push(value.id),
    observeProviderSyncDelistings: () => {}, renderGlobal: () => {}, renderModelList: () => {},
    loadModels: async () => catalogRefreshes.push(true),
  });
  const run = source => vm.runInContext(source, context);
  run('_cachedModels = ' + JSON.stringify(models) + '; _globalData = { offerings: ' + JSON.stringify(offerings) + ' };');
  const value = source => JSON.parse(JSON.stringify(run(source)));
  return { run, value, calls, closed, rendered, list, catalogRefreshes };
}

test('authoritative provider lists prune missing entries and close their details', async () => {
  const current = { ...provider, name: 'Updated' };
  const ui = createUI({ providers: [current] });
  ui.run('_provCache.removed = { id: "removed" }; _provDetailOpenId = "removed";');
  await ui.run('loadProviders()');
  assert.deepEqual(ui.value('Object.keys(_provCache)'), ['primary']);
  assert.equal(ui.run('_provCache.primary.name'), 'Updated');
  assert.equal(ui.run('_provDetailOpenId'), '');
  assert.deepEqual(ui.closed, ['provDetail']);
});

test('an empty authoritative provider list also evicts cache and closes details', async () => {
  const ui = createUI({ providers: [] });
  ui.run('_provCache.primary = { id: "primary" }; _provDetailOpenId = "primary";');
  await ui.run('loadProviders()');
  assert.deepEqual(ui.value('Object.keys(_provCache)'), []);
  assert.equal(ui.run('_provDetailOpenId'), '');
  assert.deepEqual(ui.closed, ['provDetail']);
  assert.match(ui.list.innerHTML, /暂无自定义 Provider/);
});

test('an older list response cannot overwrite a newer authoritative empty list', async () => {
  const old = deferred();
  let reads = 0;
  const ui = createUI({ providerRead: () => ++reads === 1 ? old.promise : { data: { providers: [] } } });
  const pending = ui.run('loadProviders()');
  await ui.run('loadProviders()');
  old.resolve({ data: { providers: [provider] } });
  await pending;
  assert.deepEqual(ui.value('Object.keys(_provCache)'), []);
  assert.match(ui.list.innerHTML, /暂无自定义 Provider/);
});

test('successful deletion immediately evicts detail and survives stale reads plus refresh failure', async () => {
  const old = deferred(), refresh = deferred();
  let reads = 0;
  const ui = createUI({ providerRead: () => ++reads === 1 ? old.promise : refresh.promise });
  ui.run('_provCache.primary = { id: "primary" }; _provDetailOpenId = "primary";');
  const pending = ui.run('loadProviders()');
  await ui.run('deleteProvider("primary")');
  assert.deepEqual(ui.value('Object.keys(_provCache)'), []);
  assert.equal(ui.run('_provDetailOpenId'), '');
  assert.deepEqual(ui.closed, ['provDetail']);
  refresh.reject(new Error('refresh failed'));
  await new Promise(resolve => setImmediate(resolve));
  old.resolve({ data: { providers: [provider] } });
  await pending;
  assert.deepEqual(ui.value('Object.keys(_provCache)'), []);
  assert.match(ui.list.innerHTML, /refresh failed/);
});

test('failed deletion preserves the cached provider and its detail', async () => {
  const ui = createUI({ deleteError: 'save failed' });
  ui.run('_provCache.primary = { id: "primary" }; _provDetailOpenId = "primary";');
  await ui.run('deleteProvider("primary")');
  assert.deepEqual(ui.value('Object.keys(_provCache)'), ['primary']);
  assert.equal(ui.run('_provDetailOpenId'), 'primary');
  assert.deepEqual(ui.closed, []);
  assert.equal(ui.calls.length, 1);
});

const toggles = [
  ['single', 'toggleModel("free-model", true)', true],
  ['filtered', 'bulkToggleFiltered(true)', true],
  ['group', 'toggleModelsBatch("src:prov:Primary", true)', true],
  ['free', 'bulkModelToggle("free")', false],
  ['paid', 'bulkModelToggle("paid")', true],
];
for (const [name, action, disabled] of toggles) {
  test(name + ' model toggle refreshes the global offering state after success', async () => {
    const ui = createUI();
    await ui.run(action);
    const write = ui.calls.findIndex(call => call.path === '/models/toggle');
    const read = ui.calls.findIndex(call => call.path === '/model-offerings');
    assert.ok(write >= 0 && read > write, 'global projection is read after the successful mutation');
    const changed = ui.calls[write].body.ids;
    assert.ok(ui.value('_globalData.offerings').filter(offering => changed.includes(offering.modelId)).every(offering => offering.disabled === disabled));
    assert.equal(ui.catalogRefreshes.length, 1);
  });

  test(name + ' model toggle does not refresh the global offering state after failure', async () => {
    const ui = createUI({ toggleError: 'save failed' });
    await ui.run(action);
    assert.ok(ui.calls.some(call => call.path === '/models/toggle'));
    assert.ok(!ui.calls.some(call => call.path === '/model-offerings'));
    assert.equal(ui.catalogRefreshes.length, 1);
  });
}

test('cancelled bulk toggles leave the catalog and global projection unchanged', async () => {
  const ui = createUI({ confirm: false });
  for (const [, action] of toggles.slice(1)) await ui.run(action);
  assert.deepEqual(ui.calls, []);
  assert.deepEqual(ui.catalogRefreshes, []);
});

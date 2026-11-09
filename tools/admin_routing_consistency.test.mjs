// Run with: node --test tools/admin_routing_consistency.test.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

function createUI({ canReadCredentials = true, credentialError, saveError, offeringError } = {}) {
  const field = value => ({ value, dataset: {}, innerHTML: '', textContent: '', hidden: false });
  const elements = new Map([
    ['settingStrategy', field('random')],
    ['settingDefModel', field('new-model')],
    ['settingModelChain', field('new-model, fallback, new-model')],
    ['settingOnlyFree', field('true')],
    ...['routeFlowModel', 'routeFlowStrategy', 'routeFlowFallback', 'routeInspectorSection', 'routeInspectorError',
      'routeInspectorModel', 'routeInspectorSummary', 'routeInspectorCandidates', 'routeInspectorFallback',
      'routeInspectorRuntime'].map(id => [id, field('')]),
  ]);
  elements.get('settingModelChain').dataset.dirty = '1';
  const calls = [], notices = [];
  let config = { defaultModel: 'old-model', strategy: 'round_robin', modelChain: ['old-model'] };
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    document: { addEventListener: () => {} },
    t: value => value,
    esc: value => String(value),
    escAttr: value => String(value),
    formatNumber: value => String(value),
    _cachedModels: [],
    _globalStatuses: {}, _globalAccess: {}, _globalObservations: {}, _globalConfidence: {},
    accessTone: () => '', globalStatusTone: () => '',
    toast: (message, kind) => notices.push({ message, kind }),
    adminHasPermission: permission => permission !== 'credential.write' || canReadCredentials,
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      if (path === '/config/update') {
        if (saveError) throw new Error(saveError);
        config = { ...body, modelChain: [...new Set(body.modelChain)] };
        // The real save response omits the normalized model chain.
        return { data: { strategy: config.strategy, defaultModel: config.defaultModel, onlyFree: config.onlyFree } };
      }
      if (path === '/config') return { data: config };
      if (path === '/credentials') {
        if (!canReadCredentials || credentialError) throw new Error(credentialError || 'permission_denied');
        return { data: { credentials: [{ id: 'cred-1', subject: 'Example account', providerIds: ['provider-1'] }] } };
      }
      if (path === '/provider-instances') return { data: { instances: [{ id: 'provider-1', name: 'Example Provider', enabled: true }] } };
      if (path === '/model-offerings') {
        if (offeringError) throw new Error(offeringError);
        return { data: { offerings: [{ modelId: config.defaultModel, providerId: 'provider-1' }] } };
      }
      if (path === '/provider-state') return { data: { observations: [] } };
      if (path === '/combos') return { data: { combos: [] } };
      if (path === '/request-logs?limit=100') return { data: { items: [] } };
      throw new Error('Unexpected request: ' + path);
    },
  });
  for (const asset of ['05_settings.js', '13_models.js', '20_routing_inspector.js']) {
    vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/' + asset, import.meta.url), 'utf8'), context, { filename: asset });
  }
  // Keep the actual summary renderer; the other config form fields are outside
  // this controller test and use their existing loadConfig contract.
  vm.runInContext(`loadConfig = async options => {
    if (!options.preserveEdits || !_('settingModelChain').dataset.dirty) {
      const response = await api('GET', '/config');
      _('settingModelChain').value = (response.data.modelChain || []).join(', ');
      renderRouteSummary(response.data);
    }
  };`, context);
  return { elements, calls, notices, run: source => vm.runInContext(source, context) };
}

test('successful routing save refreshes summary and inspector from normalized config', async () => {
  const ui = createUI();
  await ui.run('loadRouteInspector()');
  await ui.run('updateConfig()');
  assert.equal(ui.elements.get('routeFlowModel').textContent, 'new-model');
  assert.equal(ui.elements.get('routeFlowStrategy').textContent, 'random');
  assert.equal(ui.elements.get('routeFlowFallback').textContent, 'new-model → fallback');
  assert.equal(ui.elements.get('settingModelChain').value, 'new-model, fallback');
  assert.equal(ui.elements.get('settingModelChain').dataset.dirty, undefined);
  assert.match(ui.elements.get('routeInspectorFallback').innerHTML, /fallback/);
  assert.doesNotMatch(ui.elements.get('routeInspectorFallback').innerHTML, /old-model/);
  assert.equal(ui.notices.at(-1).kind, 'success');
});

test('failed routing save preserves dirty form and current inspector', async () => {
  const ui = createUI({ saveError: 'save_failed' });
  await ui.run('loadRouteInspector()');
  const before = ui.elements.get('routeInspectorFallback').innerHTML;
  ui.calls.length = 0;
  await ui.run('updateConfig()');
  assert.equal(ui.elements.get('settingModelChain').dataset.dirty, '1');
  assert.equal(ui.elements.get('routeInspectorFallback').innerHTML, before);
  assert.deepEqual(ui.calls.map(call => call.path), ['/config/update']);
  assert.equal(ui.notices.at(-1).kind, 'error');
});

test('provider admin sees routing projections without requesting forbidden credentials', async () => {
  const ui = createUI({ canReadCredentials: false });
  await ui.run('loadRouteInspector()');
  assert.equal(ui.calls.some(call => call.path === '/credentials'), false);
  assert.equal(ui.elements.get('routeInspectorError').hidden, true);
  assert.match(ui.elements.get('routeInspectorCandidates').innerHTML, /Example Provider/);
  assert.match(ui.elements.get('routeInspectorCandidates').innerHTML, /未知/);
  assert.match(ui.elements.get('routeInspectorSummary').innerHTML, /关联凭据数.*ri-unknown/s);
});

test('credential permission denial does not discard other routing projections', async () => {
  const ui = createUI({ credentialError: 'permission_denied' });
  await ui.run('loadRouteInspector()');
  assert.equal(ui.elements.get('routeInspectorError').hidden, true);
  assert.match(ui.elements.get('routeInspectorCandidates').innerHTML, /Example Provider/);
  assert.match(ui.elements.get('routeInspectorSummary').innerHTML, /关联凭据数.*ri-unknown/s);
});

test('authorized credential projection remains visible and counted', async () => {
  const ui = createUI();
  await ui.run('loadRouteInspector()');
  assert.equal(ui.calls.some(call => call.path === '/credentials'), true);
  assert.match(ui.elements.get('routeInspectorCandidates').innerHTML, /Example account/);
  assert.match(ui.elements.get('routeInspectorSummary').innerHTML, /关联凭据数<\/span><div class="ri-fact-value">1<\/div>/);
});

test('required projection errors still show an inspector failure', async () => {
  const ui = createUI({ offeringError: 'offerings_unavailable' });
  await ui.run('loadRouteInspector()');
  assert.equal(ui.elements.get('routeInspectorError').hidden, false);
  assert.match(ui.elements.get('routeInspectorError').innerHTML, /offerings_unavailable/);
});

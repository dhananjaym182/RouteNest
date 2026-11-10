import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

function quickStartUI({ origin = 'http://127.0.0.1:3467', configuredModel = '', models = [] } = {}) {
  const elements = new Map([
    ['settingDefModel', { value: configuredModel, dataset: configuredModel ? { configLoaded: '1' } : {} }],
    ...['quickstartClaudeCode', 'quickstartClineCode', 'quickstartOpenAICode', 'quickstartCurlCode']
      .map(id => [id, { textContent: '' }]),
  ]);
  let modelLoads = 0, configLoads = 0;
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    location: { origin },
    _cachedModels: models,
    adminHasPermission: permission => permission === 'provider.write',
    loadModels: async () => { modelLoads++; },
    loadConfig: async () => { configLoads++; },
  });
  vm.runInContext(readFileSync(new URL('../internal/admin/ui/js/15_quickstart.js', import.meta.url), 'utf8'), context);
  return { elements, run: source => vm.runInContext(source, context), counts: () => ({ modelLoads, configLoads }) };
}

test('Quick Start uses the current gateway origin and configured model', async () => {
  const ui = quickStartUI({ configuredModel: 'provider/model-a' });
  await ui.run('loadQuickStart()');
  assert.match(ui.elements.get('quickstartOpenAICode').textContent, /http:\/\/127\.0\.0\.1:3467\/v1/);
  assert.match(ui.elements.get('quickstartOpenAICode').textContent, /provider\/model-a/);
  assert.match(ui.elements.get('quickstartCurlCode').textContent, /"model":"provider\/model-a"/);
  assert.deepEqual(ui.counts(), { modelLoads: 1, configLoads: 1 });
});

test('Quick Start falls back to the first enabled model', () => {
  const ui = quickStartUI({ models: [{ id: 'disabled', disabled: true }, { id: 'ready-model', disabled: false }] });
  ui.run('renderQuickStartExamples()');
  assert.match(ui.elements.get('quickstartClaudeCode').textContent, /claude --model ready-model/);
});

test('Quick Start uses the owned HTTP gateway inside a native asset origin', () => {
  const ui = quickStartUI({ origin: 'null', configuredModel: 'safe-model' });
  ui.run("globalThis.nativeDesktopAvailable = () => true; globalThis.gatewayV1URL = () => 'http://127.0.0.1:3467/v1'");
  ui.run('renderQuickStartExamples()');
  assert.match(ui.elements.get('quickstartOpenAICode').textContent, /http:\/\/127\.0\.0\.1:3467\/v1/);
  assert.doesNotMatch(ui.elements.get('quickstartCurlCode').textContent, /null|wails:/);
});

test('Quick Start never inserts a real client key', () => {
  const ui = quickStartUI({ configuredModel: 'safe-model' });
  ui.run('renderQuickStartExamples()');
  for (const element of ui.elements.values()) {
    if ('textContent' in element) assert.doesNotMatch(element.textContent, /sk-|Bearer [A-Za-z0-9]/);
  }
  assert.match(ui.elements.get('quickstartCurlCode').textContent, /<your-api-key>/);
});

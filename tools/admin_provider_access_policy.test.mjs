import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { test } from 'node:test';
import vm from 'node:vm';

// The provider editor's "Free-only handling" control is the administrator's
// explicit choice. It defaults to auto-detect, is never filled in by a preset,
// reaches the save request, and is shown in the change preview and the detail
// view. Both real UI files run here against a small fake DOM.

const require = createRequire(import.meta.url);
const { parseDict } = require('./i18n_lib.js');
const read = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');

function fakeEditor() {
  const elements = new Map();
  const el = (id, value = '') => {
    const e = { id, value, textContent: '', innerHTML: '', classes: new Set(), dataset: {}, disabled: false,
      classList: { toggle(name, on) { on ? e.classes.add(name) : e.classes.delete(name); }, add: n => e.classes.add(n), remove: n => e.classes.delete(n), contains: n => e.classes.has(n) } };
    elements.set(id, e);
    return e;
  };
  [['provName', ''], ['provBaseURL', ''], ['provKey', ''], ['provPriority', '10'], ['provTimeout', '300'], ['provRpmLimit', '0'],
   ['provDailyLimit', '0'], ['provTokenLimit', '0'], ['provTokenWindow', 'monthly'], ['provProxyUrl', ''], ['provWeight', '0'],
   ['provModels', ''], ['provHeaders', ''], ['provEnabled', 'true'], ['provFree', 'false'], ['provAccessPolicy', 'detect'],
   ['provAccessPolicyHint', '']].forEach(([id, v]) => el(id, v));
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    t: v => v, esc: v => String(v), escAttr: v => String(v).replace(/"/g, '&quot;'),
    initConnPick: () => {}, renderProvModelChips: () => {}, syncProvTokenWindowUI: () => {},
    _provConnSel: [], _editingProviderId: 'prov_1',
    document: { addEventListener: () => {}, querySelectorAll: () => [] },
  });
  // The real diffFlat from 03_shell.js, so the change preview is the product's own.
  const shell = read('js/03_shell.js');
  const start = shell.indexOf('function diffFlat(');
  vm.runInContext(shell.slice(start, shell.indexOf('\n}\n', start) + 3), context);
  context.refreshSaveBar = () => {};
  vm.runInContext(read('js/08_providers_detect.js'), context);
  vm.runInContext(read('js/07_providers_detail.js'), context);
  return { elements, context, run: source => vm.runInContext(source, context) };
}

test('a provider with no saved choice shows auto-detect and says what it does', () => {
  const ui = fakeEditor();
  ui.run('applyProviderForm({ name: "Old", baseURL: "https://x", free: true, modelIds: [] })');
  assert.equal(ui.elements.get('provAccessPolicy').value, 'detect');
  assert.match(ui.elements.get('provAccessPolicyHint').textContent, /判定/);
  assert.ok(!ui.elements.get('provAccessPolicyHint').classes.has('hint-red'));
});

test('a new provider starts on auto-detect, so a preset can never pre-select a grant', () => {
  const ui = fakeEditor();
  ui.elements.get('provAccessPolicy').value = 'allow_all';
  ui.run('applyProviderForm(null)');
  assert.equal(ui.elements.get('provAccessPolicy').value, 'detect');
  // applyPreset() in 09_providers_pickers.js opens a blank editor and fills only
  // name, URL, headers, models and the free flag. It must never touch the policy.
  assert.doesNotMatch(read('js/09_providers_pickers.js'), /provAccessPolicy|accessPolicy/);
  assert.doesNotMatch(read('js/10_providers_import.js'), /provAccessPolicy|accessPolicy/);
});

test('every saved choice is shown back, and only allow-all warns about cost', () => {
  const ui = fakeEditor();
  for (const [policy, warns] of [['detect', false], ['all_free', false], ['allow_all', true]]) {
    ui.run(`applyProviderForm({ name: "P", baseURL: "https://x", accessPolicy: ${JSON.stringify(policy)}, modelIds: [] })`);
    assert.equal(ui.elements.get('provAccessPolicy').value, policy);
    assert.equal(ui.elements.get('provAccessPolicyHint').classes.has('hint-red'), warns, policy);
  }
  assert.match(ui.elements.get('provAccessPolicyHint').textContent, /可能产生费用/);
});

test('an unknown stored value is shown as the default, never as a grant', () => {
  const ui = fakeEditor();
  ui.run('applyProviderForm({ name: "P", baseURL: "https://x", accessPolicy: "both", modelIds: [] })');
  assert.equal(ui.elements.get('provAccessPolicy').value, 'detect');
  ui.elements.get('provAccessPolicy').value = 'garbage';
  assert.equal(ui.run('provAccessPolicyValue()'), 'detect');
});

test('the form snapshot carries the choice, so changing it shows up in the change preview', () => {
  const ui = fakeEditor();
  ui.run('applyProviderForm({ name: "P", baseURL: "https://x", accessPolicy: "detect", modelIds: [] })');
  ui.run('_provBaseJson = JSON.stringify(serializeProvForm())');
  assert.equal(ui.run('provFormChanges().length'), 0, 'an untouched form has nothing to confirm');
  ui.elements.get('provAccessPolicy').value = 'allow_all';
  const changes = ui.run('provFormChanges()');
  assert.equal(changes.length, 1);
  assert.equal(changes[0].label, 'Free-only 模式处理');
  assert.equal(changes[0].from, 'detect');
  assert.equal(changes[0].to, 'allow_all');
});

test('the choice reaches the save request, and the enable toggle keeps it', async () => {
  const ui = fakeEditor();
  ui.run('applyProviderForm({ name: "P", baseURL: "https://x", accessPolicy: "all_free", modelIds: [] })');
  assert.equal(ui.run('serializeProvForm().accessPolicy'), 'all_free');

  // The toggle fetches the provider and posts the whole record back.
  const posts = [];
  ui.context.api = async (method, path, body) => {
    if (method === 'GET') return { data: { providers: [{ id: 'prov_1', name: 'P', enabled: true, accessPolicy: 'allow_all', modelIds: [] }] } };
    posts.push({ path, body });
    return { data: {} };
  };
  ui.context.toast = () => {}; ui.context.loadProviders = () => {}; ui.context.loadGlobal = () => {};
  await ui.run('toggleProvider("prov_1", false, null)');
  assert.equal(posts.length, 1);
  assert.equal(posts[0].path, '/providers/save');
  assert.equal(posts[0].body.accessPolicy, 'allow_all', 'disabling a provider must not reset the administrator’s choice');
  assert.equal(posts[0].body.enabled, false);
});

test('the payload built by the save button includes the choice', () => {
  const source = read('js/08_providers_detect.js');
  const payload = source.slice(source.indexOf('const payload = {'), source.indexOf('// 变更预览'));
  assert.match(payload, /accessPolicy:\s*provAccessPolicyValue\(\)/);
});

test('the detail view shows the choice, with a plain default', () => {
  const ui = fakeEditor();
  const badge = policy => ui.run(`provAccessPolicyBadge(${JSON.stringify(policy)})`);
  assert.match(badge(undefined), /自动识别（默认）/);
  assert.match(badge('detect'), /自动识别（默认）/);
  assert.match(badge('all_free'), /全部模型视为免费/);
  assert.match(badge('allow_all'), /免费与付费混合：全部放行/);
  assert.doesNotMatch(badge('not-a-policy'), /全部放行|视为免费/, 'an unknown value reads as the default');
  const detail = read('js/07_providers_detail.js');
  assert.match(detail, /kv\(t\('Free-only 模式处理'\), provAccessPolicyBadge\(p\.accessPolicy\)\)/);
});

test('the editor offers exactly the three values the server accepts', () => {
  const html = read('html/dialogs/provider_editor.html');
  const select = html.slice(html.indexOf('id="provAccessPolicy"'), html.indexOf('</select>', html.indexOf('id="provAccessPolicy"')));
  assert.deepEqual([...select.matchAll(/value="([^"]+)"/g)].map(m => m[1]), ['detect', 'all_free', 'allow_all']);
  assert.match(select, /<option value="detect">/, 'the first option is the default');
  // Same names as internal/domain/access.go.
  const goSource = readFileSync(new URL('../internal/domain/access.go', import.meta.url), 'utf8');
  for (const value of ['"detect"', '"all_free"', '"allow_all"']) assert.ok(goSource.includes(value), value);
  // The old statistics-only field says so, so nobody mistakes it for the control.
  assert.match(html, /仅用于统计和分组，不影响「仅免费模型」/);
});

test('every new string has an English translation', () => {
  const dict = parseDict().keys;
  const ui = fakeEditor();
  const keys = ['Free-only 模式处理', '自动识别（默认）', '全部模型视为免费', '免费与付费混合：全部放行',
    '仅用于统计和分组，不影响「仅免费模型」。要控制放行范围，请用下面的设置。', ...Object.values(ui.run('PROV_ACCESS_POLICY_HINTS'))];
  assert.equal(keys.length, 8);
  for (const key of keys) {
    assert.ok(dict.has(key), 'missing dictionary key: ' + key);
    assert.match(dict.get(key).value, /[A-Za-z]/);
  }
  // The English text must name the toggle the way the Routing page does.
  assert.equal(dict.get('仅免费模型').value, 'Free models only');
});

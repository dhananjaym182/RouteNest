// Run with: node --test tools/admin_combo_editor.test.mjs
// Combos page (#combos): one-click copy of the combo ID, and the Edit Combo drawer:
// unsaved-change tracking, guarded close, pre-save validation that mirrors the server,
// duplicate/empty entry handling, change preview, double-submit protection, picker filters.
// Runs the real shipped scripts in a VM against a stubbed API; no browser.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const asset = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');
const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

function extractFunction(source, name) {
  const start = source.indexOf('function ' + name + '(');
  assert.ok(start >= 0, name + ' not found');
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw new Error('unterminated ' + name);
}

const catalog = [
  { id: 'alpha', source: 'remote', cost: 'free' },
  { id: 'beta', source: 'zen', provider: 'opencode', providerId: 'opencode', cost: 'free' },
  { id: 'gamma', source: 'provider', provider: 'Acme', providerId: 'prov_acme', providerName: 'Acme', cost: 'pass' },
  { id: 'delta', source: 'provider', provider: 'Acme', providerId: 'prov_acme', providerName: 'Acme', cost: 'free' },
  { id: 'hidden', source: 'remote', cost: 'free', disabled: true },
];
const existing = () => [
  { id: 'work', name: 'Work', enabled: true, emergencyFallback: false, entries: [{ model: 'alpha' }, { model: 'beta', source: 'zen' }] },
  { id: 'other', name: 'Other Combo', enabled: false, entries: [{ model: 'alpha', disabled: true }] },
];

function createUI({ saveError = '', saveDelay = false, reserved = ['free'] } = {}) {
  const elements = new Map();
  const field = id => {
    const classes = new Set();
    const attrs = {};
    return {
      id, value: '', checked: false, innerHTML: '', textContent: '', disabled: false, hidden: false, scrollTop: 0, style: {}, dataset: {}, attrs,
      classList: { add: n => classes.add(n), remove: n => classes.delete(n), contains: n => classes.has(n), toggle: (n, on) => (on ? classes.add(n) : classes.delete(n)) },
      setAttribute: (n, v) => { attrs[n] = String(v); }, removeAttribute: n => { delete attrs[n]; },
      focus() { focused.push(id); }, querySelectorAll: () => [], querySelector: () => null,
    };
  };
  const focused = [];
  const el = id => { if (!elements.has(id)) elements.set(id, field(id)); return elements.get(id); };
  const guards = {}, bars = {}, toasts = [], posts = [], copied = [], confirms = [], timers = [], keydown = [];
  const state = { confirmAnswer: true, modalOpen: false, saveBarRefreshes: 0 };
  const chips = ['', 'free', 'paid'].map(tier => { const chip = field('chip-' + tier); chip.dataset.tier = tier; return chip; });
  let savedRelease = null;
  const diffFlat = vm.runInNewContext('(' + extractFunction(asset('js/03_shell.js'), 'diffFlat') + ')');
  const context = vm.createContext({
    _: el, t: value => value, tx: value => value, esc: escapeHTML, escAttr: escapeHTML,
    uiIcon: (name, cls) => '<i data-icon="' + name + '" class="' + (cls || '') + '"></i>',
    listEmptyState: () => '<empty>', ICON_COMBOS: '', pickerMergeIds: (order, values) => values.forEach(v => { if (!order.includes(v)) order.push(v); }),
    friendlyTestError: error => error.message, diffFlat,
    toast: (message, kind) => toasts.push({ message, kind }),
    copyText: value => copied.push(value),
    showConfirm: async message => { confirms.push(message); return state.confirmAnswer; },
    setDrawerGuard: (name, fn) => { if (fn) guards[name] = fn; else delete guards[name]; },
    setSaveBarCtx: (name, ctx) => { if (ctx) bars[name] = ctx; else delete bars[name]; },
    refreshSaveBar: () => { state.saveBarRefreshes++; },
    openDrawer: name => el(name).classList.add('open'),
    closeDrawer: async name => { const guard = guards[name]; if (guard && !(await guard())) return; el(name).classList.remove('open'); },
    _modalLayerOpen: () => state.modalOpen,
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; }, clearTimeout() {},
    document: {
      addEventListener: (type, fn) => { if (type === 'keydown') keydown.push(fn); },
      querySelector: () => null,
      querySelectorAll: selector => (selector === '.combo-pick-tiers [data-tier]' ? chips : []),
    },
    api: async (method, path, body) => {
      if (method === 'POST' && path === '/combos/save') {
        if (saveDelay) await new Promise(resolve => { savedRelease = resolve; });
        if (saveError) throw new Error(saveError);
        posts.push(JSON.parse(JSON.stringify(body)));
        return { data: { combo: body } };
      }
      if (method === 'POST' && path === '/combos/delete') return { data: {} };
      if (method === 'GET' && path === '/combos') return { data: { combos: existing(), modelCatalog: catalog, reservedAliases: reserved } };
      throw new Error('Unexpected API request: ' + method + ' ' + path);
    },
  });
  for (const file of ['12_combo_catalog.js', '12_combos.js']) vm.runInContext(asset('js/' + file), context, { filename: file });
  const run = source => vm.runInContext(source, context);
  run('_comboList = ' + JSON.stringify(existing()) + '; _comboCatalog = ' + JSON.stringify(catalog) + '; _comboReserved = ' + JSON.stringify(reserved));
  return {
    run, el, guards, bars, toasts, posts, copied, confirms, timers, keydown, state, chips, focused,
    release: () => savedRelease && savedRelease(),
    open: combo => run('showComboEditor(' + (combo ? JSON.stringify(combo) : '') + ')'),
    form: values => {
      for (const [id, value] of Object.entries(values)) el(id)[typeof value === 'boolean' ? 'checked' : 'value'] = value;
      run('comboEditorSync()');
    },
    isOpen: () => el('comboEditor').classList.contains('open'),
  };
}
const edit = ui => ui.open(existing()[0]);
const decodeAttr = value => value.replace(/&(quot|#39|amp|lt|gt);/g, (_, name) => ({ quot: '"', '#39': "'", amp: '&', lt: '<', gt: '>' }[name]));

// ---------- one-click copy ----------
test('every row in the list has a button that copies its combo ID', () => {
  const ui = createUI();
  ui.run('renderCombosList()');
  const html = ui.el('combosList').innerHTML;
  const buttons = [...html.matchAll(/<button type="button" class="combo-id-copy" onclick="copyComboId\(&quot;([^&]*)&quot;, this\)"[^>]*aria-label="([^"]*)">/g)];
  assert.deepEqual(buttons.map(m => m[1]), ['work', 'other']);
  assert.deepEqual(buttons.map(m => m[2]), ['复制组合 ID: work', '复制组合 ID: other']);
  assert.match(html, /<span class="combo-id-text">work<\/span><i data-icon="copy" class="ic-copy"><\/i><i data-icon="check" class="ic-done"><\/i>/);
});

test('an ID with quotes cannot break out of the click handler or the attribute', () => {
  const ui = createUI();
  ui.run('_comboList = [{ id: "a\'b\\"c", name: "x", enabled: true, entries: [] }]; renderCombosList()');
  const html = ui.el('combosList').innerHTML;
  const [first] = [...html.matchAll(/ onclick="([^"]*)"/g)].map(m => decodeAttr(m[1]));
  let received;
  new vm.Script(first).runInContext(vm.createContext({ copyComboId: id => { received = id; }, this: {} }));
  assert.equal(received, 'a\'b"c');
});

test('one click copies the ID exactly once and shows a confirmation that clears itself', () => {
  const ui = createUI();
  const btn = ui.el('probe');
  ui.run('copyComboId("work", _("probe"))');
  assert.deepEqual(ui.copied, ['work']);
  assert.ok(btn.classList.contains('copied'));
  assert.equal(ui.timers.length, 1);
  ui.timers[0].fn();
  assert.ok(!btn.classList.contains('copied'));
});

test('copying nothing does nothing', () => {
  const ui = createUI();
  ui.run('copyComboId("  ", null)');
  assert.deepEqual(ui.copied, []);
});

test('the editor copy button copies the typed ID, or asks for one', () => {
  const ui = createUI();
  ui.open();
  ui.run('copyComboIdFromEditor(_("probe"))');
  assert.deepEqual(ui.copied, []);
  assert.equal(ui.toasts.at(-1).kind, 'error');
  ui.el('comboId').value = '  my-combo ';
  ui.run('copyComboIdFromEditor(_("probe"))');
  assert.deepEqual(ui.copied, ['my-combo']);
});

test('the editor copy button works while editing, when the ID field is locked', () => {
  const ui = createUI();
  edit(ui);
  assert.equal(ui.el('comboId').disabled, true);
  ui.run('copyComboIdFromEditor(_("probe"))');
  assert.deepEqual(ui.copied, ['work']);
});

// ---------- list layout ----------
test('the list uses the content-sized management table, not the fixed-width action table', () => {
  const ui = createUI();
  ui.run('renderCombosList()');
  const html = ui.el('combosList').innerHTML;
  assert.match(html, /^<div class="mgmt-table-wrap combos-list"><table class="mgmt-table combos-table">/);
  assert.doesNotMatch(html, /action-table/, 'action-table inherits table-layout:fixed, which splits the free width equally between columns');
  const headers = [...html.matchAll(/<th class="([^"]*)">|<th>/g)].map(m => m[1] || '');
  assert.deepEqual(headers, ['', '', 'combo-col-fit', 'mgmt-num combo-col-fit', 'combo-col-fit', 'combo-col-actions']);
});

test('only the short columns shrink to their content; ID and name take the free width', () => {
  const css = asset('css/41_pages_combos.css');
  assert.match(css, /\.combos-table \.combo-col-fit,\.combos-table \.combo-col-actions\{width:1%;white-space:nowrap\}/);
  const rule = name => css.match(new RegExp('\\.combos-table \\.' + name + '[^{]*\\{[^}]*\\}'));
  assert.ok(!/width:1%/.test(rule('combo-col-id')[0]) && !/width:1%/.test(rule('combo-col-name')[0]));
  assert.match(css, /\.combos-table \.mgmt-actions\{flex-wrap:nowrap\}/, 'five buttons stay on one line; the wrapper scrolls on narrow screens');
  assert.match(css, /\.combos-table \.combo-id-text\{max-width:/, 'a very long ID is clipped, not allowed to push other columns away');
  assert.match(css, /\.combos-table \.combo-name-text\{[^}]*text-overflow:ellipsis/);
});

test('the old fixed-width budget rules for this list are gone', () => {
  for (const file of ['css/41_pages_combos.css', 'css/42_pages_providers_import.css']) {
    const css = asset(file);
    assert.doesNotMatch(css, /\.combos-list \.action-table|\.combos-list \.row-count|\.combos-list\{/, file);
  }
});

test('rows show the full ID and name on hover, right-align the entry count and keep the status badge', () => {
  const ui = createUI();
  ui.run('_comboList = [{ id: "a-very-long-combo-identifier", name: "A very long human readable combo name", category: "coding", enabled: true, emergencyFallback: true, entries: [{ model: "alpha" }, { model: "beta", disabled: true }] }]; renderCombosList()');
  const html = ui.el('combosList').innerHTML;
  assert.match(html, /title="a-very-long-combo-identifier · 点击复制组合 ID"/);
  assert.match(html, /<span class="combo-name-text" title="A very long human readable combo name">A very long human readable combo name<\/span> <span class="model-source-badge provider"[^>]*>紧急回退<\/span>/);
  assert.match(html, /<td class="mgmt-num combo-col-fit">1 \/ 2<\/td>/);
  assert.match(html, /<td class="combo-col-fit"><span class="combo-category" title="coding">coding<\/span><\/td>/);
  assert.match(html, /<td class="combo-col-fit"><span class="status active">/);
});

test('an empty category shows a dash and an empty list shows the empty state', () => {
  const ui = createUI();
  ui.run('_comboList = [{ id: "x", name: "X", enabled: false, entries: [] }]; renderCombosList()');
  assert.match(ui.el('combosList').innerHTML, /<span class="mgmt-muted">-<\/span>/);
  ui.run('_comboList = []; renderCombosList()');
  assert.equal(ui.el('combosList').innerHTML, '<empty>');
});

test('every inline handler in the rows is valid JavaScript and calls the right function with the combo ID', () => {
  const ui = createUI();
  ui.run('_comboList = [{ id: "plain-id", name: "N", enabled: true, entries: [] }, { id: "q\'uote\\"d", name: "Q", enabled: false, entries: [] }]; renderCombosList()');
  const handlers = [...ui.el('combosList').innerHTML.matchAll(/ onclick="([^"]*)"/g)].map(m => decodeAttr(m[1]));
  assert.equal(handlers.length, 12, 'six buttons per row');
  const calls = [];
  const sandbox = vm.createContext(Object.fromEntries(['copyComboId', 'testCombo', 'editCombo', 'cloneCombo', 'toggleCombo', 'deleteCombo'].map(name => [name, (...args) => calls.push([name, args[0]])])));
  for (const handler of handlers) new vm.Script(handler).runInContext(vm.createContext({ ...sandbox, this: {} }));
  const names = calls.map(call => call[0]);
  assert.deepEqual(names.slice(0, 6), ['copyComboId', 'testCombo', 'editCombo', 'cloneCombo', 'toggleCombo', 'deleteCombo']);
  assert.deepEqual([...new Set(calls.slice(0, 6).map(call => call[1]))], ['plain-id']);
  assert.deepEqual([...new Set(calls.slice(6).map(call => call[1]))], ['q\'uote"d'], 'an ID with quotes arrives intact');
});

// ---------- unsaved changes ----------
test('an untouched existing combo is clean: save is disabled and nothing is marked unsaved', () => {
  const ui = createUI();
  edit(ui);
  assert.equal(ui.run('comboEditorDirty()'), false);
  assert.equal(ui.el('comboSaveBtn').disabled, true);
  assert.equal(ui.el('comboSaveBtn').textContent, '保存更改');
  assert.equal(ui.el('comboDirtyTag').hidden, true);
});

test('editing a field marks the combo unsaved and enables save; reverting the edit makes it clean again', () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  assert.equal(ui.run('comboEditorDirty()'), true);
  assert.equal(ui.el('comboSaveBtn').disabled, false);
  assert.equal(ui.el('comboDirtyTag').hidden, false);
  ui.form({ comboName: 'Work' });
  assert.equal(ui.run('comboEditorDirty()'), false);
  assert.equal(ui.el('comboSaveBtn').disabled, true);
});

test('reordering, toggling and removing entries all count as changes', () => {
  const ui = createUI();
  edit(ui);
  ui.run('moveComboEntry(0, 1)');
  assert.equal(ui.run('comboEditorDirty()'), true);
  ui.run('moveComboEntry(0, 1)');
  assert.equal(ui.run('comboEditorDirty()'), false, 'moving it back is not a change');
  ui.run('onComboEntryDisabled(1, true)');
  assert.equal(ui.run('comboEditorDirty()'), true);
  ui.run('onComboEntryDisabled(1, false); removeComboEntry(1)');
  assert.equal(ui.run('comboEditorDirty()'), true);
});

test('an added empty row is not a change, because it is ignored on save', () => {
  const ui = createUI();
  edit(ui);
  ui.run('addComboEntry()');
  assert.equal(ui.run('comboEditorDirty()'), false);
});

test('a new combo starts with save enabled and a "create" label', () => {
  const ui = createUI();
  ui.open();
  assert.equal(ui.el('comboSaveBtn').disabled, false);
  assert.equal(ui.el('comboSaveBtn').textContent, '创建组合');
  assert.equal(ui.run('comboEditorDirty()'), false);
  ui.form({ comboId: 'x' });
  assert.equal(ui.run('comboEditorDirty()'), true);
});

test('the floating save bar is registered while the editor is open and removed when it closes', async () => {
  const ui = createUI();
  edit(ui);
  assert.ok(ui.bars.combo && ui.guards.comboEditor);
  assert.equal(ui.bars.combo.host(), true);
  assert.equal(ui.bars.combo.dirty(), false);
  ui.form({ comboName: 'x' });
  assert.equal(ui.bars.combo.dirty(), true);
  ui.state.confirmAnswer = true;
  assert.equal(await ui.run('hideComboEditor()'), true);
  assert.equal(ui.bars.combo, undefined);
  assert.equal(ui.guards.comboEditor, undefined);
});

test('a stale "unsaved" state cannot leak after the drawer was closed some other way', () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'x' });
  ui.el('comboEditor').classList.remove('open'); // e.g. Esc, which goes through closeDrawer directly
  assert.equal(ui.bars.combo.dirty(), false, 'the page-leave warning must not stay armed');
});

test('closing with unsaved changes asks first; declining keeps the editor and every edit', async () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  ui.run('moveComboEntry(0, 1)');
  ui.state.confirmAnswer = false;
  assert.equal(await ui.run('hideComboEditor()'), false);
  assert.equal(ui.confirms.length, 1);
  assert.ok(ui.isOpen());
  assert.equal(ui.run('_comboEntries.length'), 2, 'entries are not wiped');
  assert.equal(ui.run('_editingComboId'), 'work');
  assert.equal(ui.run('comboEditorDirty()'), true);
});

test('the Routing link leaves the editor first and only navigates when it actually closed', async () => {
  const ui = createUI();
  ui.run("globalThis.showPageSubTab = (page, sub) => { globalThis.__visits = (globalThis.__visits || []).concat([[page, sub]]); }");
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  ui.state.confirmAnswer = false; // user keeps editing
  await ui.run('openRoutingPolicyFromComboEditor()');
  assert.equal(ui.run('JSON.stringify(globalThis.__visits || [])'), '[]', 'declining the discard prompt must not navigate away');
  assert.ok(ui.isOpen());
  ui.state.confirmAnswer = true; // user discards
  await ui.run('openRoutingPolicyFromComboEditor()');
  assert.equal(ui.run('JSON.stringify(globalThis.__visits)'), JSON.stringify([['routing', 'config']]));
});

test('closing with unsaved changes and confirming discards them', async () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  ui.state.confirmAnswer = true;
  assert.equal(await ui.run('hideComboEditor()'), true);
  assert.ok(!ui.isOpen());
  assert.equal(ui.run('_comboEntries.length'), 0);
});

test('closing a clean editor does not ask', async () => {
  const ui = createUI();
  edit(ui);
  assert.equal(await ui.run('hideComboEditor()'), true);
  assert.deepEqual(ui.confirms, []);
});

test('the guard also applies when the drawer is closed by Esc or the overlay (they call closeDrawer directly)', async () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'x' });
  ui.state.confirmAnswer = false;
  await ui.run('closeDrawer("comboEditor")');
  assert.ok(ui.isOpen());
  assert.equal(ui.confirms.length, 1);
});

test('"discard" on the save bar restores the opened values', () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'Scratch', comboEnabled: 'false' });
  ui.run('removeComboEntry(0)');
  ui.bars.combo.discard();
  assert.equal(ui.el('comboName').value, 'Work');
  assert.equal(ui.el('comboEnabled').value, 'true');
  assert.equal(ui.run('_comboEntries.length'), 2);
  assert.equal(ui.run('comboEditorDirty()'), false);
});

test('"discard" on a new combo clears the form', () => {
  const ui = createUI();
  ui.open();
  ui.form({ comboId: 'abc', comboName: 'Abc' });
  ui.bars.combo.discard();
  assert.equal(ui.el('comboId').value, '');
  assert.equal(ui.el('comboName').value, '');
});

// ---------- change preview ----------
test('the change preview lists field edits and entry additions, removals, toggles and reordering', () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'Work v2', comboEmergency: true });
  ui.run('moveComboEntry(0, 1)');
  ui.run('_comboEntries.push({ model: "delta", disabled: false, source: "provider", provider: "prov_acme" })');
  ui.run('onComboEntryDisabled(0, true)');
  const changes = ui.bars.combo.diff();
  const by = label => changes.find(change => change.label === label);
  assert.deepEqual([by('名称').from, by('名称').to], ['Work', 'Work v2']);
  assert.deepEqual([by('紧急回退').from, by('紧急回退').to], ['关闭', '开启']);
  assert.equal(by('添加条目').to, 'delta');
  assert.deepEqual([by('beta').from, by('beta').to], ['启用', '停用']);
  assert.match(by('优先级顺序').from, /alpha.*beta/);
  assert.match(by('优先级顺序').to, /beta.*alpha/);
  assert.equal(by('移除条目'), undefined);
  ui.run('removeComboEntry(1)');
  assert.equal(ui.bars.combo.diff().find(change => change.label === '移除条目').from, 'alpha');
});

test('the change preview of an untouched combo is empty', () => {
  const ui = createUI();
  edit(ui);
  assert.equal(ui.bars.combo.diff().length, 0);
});

// ---------- validation (mirrors internal/combos/combos_validation.go) ----------
const invalid = (ui, values, expectField, pattern) => {
  ui.run('_comboShowErrors = false');
  ui.form(values);
  const errors = ui.run('JSON.stringify(comboValidate())');
  const found = JSON.parse(errors).filter(error => error.field === expectField);
  assert.ok(found.length, 'expected an error on ' + expectField + ', got ' + errors);
  assert.match(found[0].msg, pattern);
};

test('new-combo IDs are checked against the server rules before saving', () => {
  const ui = createUI();
  ui.open();
  ui.run('_comboEntries = [{ model: "alpha", disabled: false, source: "", provider: "" }]');
  ui.form({ comboName: 'N' });
  invalid(ui, { comboId: '' }, 'id', /请填写组合 ID/);
  invalid(ui, { comboId: 'has space' }, 'id', /只能使用字母/);
  invalid(ui, { comboId: '-lead' }, 'id', /只能使用字母/);
  invalid(ui, { comboId: 'x'.repeat(65) }, 'id', /最长 64/);
  invalid(ui, { comboId: 'free' }, 'id', /保留别名/);
  invalid(ui, { comboId: 'work' }, 'id', /已存在/);
  ui.form({ comboId: 'ok.id_1-x' });
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(comboValidate())')), []);
});

test('names must be unique ignoring case, except for the combo being edited', () => {
  const ui = createUI();
  ui.open();
  ui.run('_comboEntries = [{ model: "alpha", disabled: false, source: "", provider: "" }]');
  ui.form({ comboId: 'new-one' });
  invalid(ui, { comboName: '' }, 'name', /请填写名称/);
  invalid(ui, { comboName: ' WORK ' }, 'name', /已被其他组合使用/);
  edit(ui);
  ui.form({ comboName: 'work' });
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(comboValidate())')), [], 'its own name is fine');
  invalid(ui, { comboName: 'other combo' }, 'name', /已被其他组合使用/);
});

test('entries: none, duplicate, "free", and enabled-without-an-enabled-entry are all caught', () => {
  const ui = createUI();
  ui.open();
  ui.form({ comboId: 'n1', comboName: 'N1' });
  invalid(ui, {}, 'entries', /至少需要一个模型条目/);
  ui.run('_comboEntries = [{ model: "alpha", disabled: false, source: "", provider: "" }, { model: "alpha", disabled: false, source: "", provider: "" }]');
  invalid(ui, {}, 'entries', /重复模型: alpha/);
  ui.run('_comboEntries = [{ model: "free", disabled: false, source: "", provider: "" }]');
  invalid(ui, {}, 'entries', /free/);
  ui.run('_comboEntries = [{ model: "alpha", disabled: true, source: "", provider: "" }]');
  invalid(ui, {}, 'entries', /至少需要一个启用的条目/);
  ui.form({ comboEnabled: 'false' });
  assert.deepEqual(JSON.parse(ui.run('JSON.stringify(comboValidate())')), [], 'a disabled combo may have only disabled entries');
});

test('more than 64 entries is rejected', () => {
  const ui = createUI();
  ui.open();
  ui.form({ comboId: 'big', comboName: 'Big' });
  ui.run('_comboEntries = Array.from({ length: 65 }, (_, i) => ({ model: "m" + i, disabled: false, source: "", provider: "" }))');
  invalid(ui, {}, 'entries', /最多 64/);
});

test('a rejected save posts nothing, explains itself under the field and moves focus there', async () => {
  const ui = createUI();
  ui.open();
  await ui.run('saveCombo()');
  assert.deepEqual(ui.posts, []);
  assert.equal(ui.el('comboIdError').hidden, false);
  assert.equal(ui.el('comboIdError').textContent, '请填写组合 ID');
  assert.equal(ui.el('comboId').attrs['aria-invalid'], 'true');
  assert.equal(ui.toasts.at(-1).message, '请填写组合 ID');
  assert.equal(ui.focused.at(-1), 'comboId');
});

test('after a failed attempt the messages follow the typing and clear when fixed', async () => {
  const ui = createUI();
  ui.open();
  ui.form({ comboName: 'N' });
  ui.run('_comboEntries = [{ model: "alpha", disabled: false, source: "", provider: "" }]');
  await ui.run('saveCombo()');
  assert.equal(ui.el('comboIdError').hidden, false);
  ui.form({ comboId: 'fixed-id' });
  assert.equal(ui.el('comboIdError').hidden, true);
  assert.equal(ui.el('comboId').attrs['aria-invalid'], undefined);
});

test('before any save attempt, nothing is flagged yet', () => {
  const ui = createUI();
  ui.open();
  ui.form({ comboId: '!!' });
  assert.equal(ui.el('comboIdError').hidden, true);
});

// ---------- saving ----------
test('a valid save posts the same payload shape as before, closes without asking, and reloads', async () => {
  const ui = createUI();
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  await ui.run('saveCombo()');
  assert.equal(ui.posts.length, 1);
  assert.deepEqual(ui.posts[0], {
    id: 'work', name: 'Work v2', description: '', category: '', enabled: true, emergencyFallback: false,
    entries: [{ model: 'alpha', disabled: false }, { model: 'beta', disabled: false, source: 'zen' }],
  });
  assert.ok(!ui.isOpen());
  assert.deepEqual(ui.confirms, [], 'saved edits are not "unsaved"');
  assert.equal(ui.toasts.at(-1).message, '组合已保存');
});

test('empty rows are dropped from the saved entries', async () => {
  const ui = createUI();
  edit(ui);
  ui.run('addComboEntry(); addComboEntry()');
  ui.form({ comboName: 'Work v2' });
  await ui.run('saveCombo()');
  assert.equal(ui.posts[0].entries.length, 2);
});

test('pressing save twice while the request is in flight sends one request', async () => {
  const ui = createUI({ saveDelay: true });
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  const first = ui.run('saveCombo()');
  assert.equal(ui.el('comboSaveBtn').disabled, true);
  assert.match(ui.el('comboSaveBtn').innerHTML, /loading/);
  await ui.run('saveCombo()');
  ui.release();
  await first;
  assert.equal(ui.posts.length, 1);
});

test('a server error keeps the editor open with the message, and save is usable again', async () => {
  const ui = createUI({ saveError: 'combo name "Work v2" is already in use' });
  edit(ui);
  ui.form({ comboName: 'Work v2' });
  await ui.run('saveCombo()');
  assert.ok(ui.isOpen());
  assert.match(ui.el('comboSaveResult').innerHTML, /already in use/);
  assert.equal(ui.toasts.at(-1).kind, 'error');
  assert.equal(ui.el('comboSaveBtn').disabled, false);
  assert.equal(ui.el('comboSaveBtn').textContent, '保存更改');
  assert.equal(ui.run('comboEditorDirty()'), true, 'the edits are still pending');
});

test('Ctrl/Cmd+S saves a changed combo; it does nothing when clean, with a dialog on top, or with the editor closed', async () => {
  const ui = createUI();
  const press = (extra = {}) => {
    let prevented = false;
    ui.keydown.forEach(fn => fn({ key: 's', ctrlKey: true, preventDefault: () => { prevented = true; }, ...extra }));
    return prevented;
  };
  assert.equal(press(), false, 'editor closed');
  edit(ui);
  assert.equal(press(), true);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ui.posts.length, 0, 'clean combo: nothing to save');
  ui.form({ comboName: 'Work v2' });
  ui.state.modalOpen = true;
  assert.equal(press(), false, 'a dialog is on top');
  ui.state.modalOpen = false;
  assert.equal(press({ ctrlKey: false }), false, 'plain "s" is typing');
  assert.equal(press({ metaKey: true, ctrlKey: false, key: 'S' }), true);
  await new Promise(resolve => setImmediate(resolve));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ui.posts.length, 1);
});

test('deleting the combo being edited closes its editor without asking about unsaved edits', async () => {
  const ui = createUI();
  ui.run('showConfirm = async message => message === "确定删除该组合？"'); // yes to deleting, no to discarding edits
  edit(ui);
  ui.form({ comboName: 'half-typed' });
  await ui.run('deleteCombo("work", null)');
  assert.ok(!ui.isOpen());
});

// ---------- entries ----------
test('duplicate and empty rows are marked, every dropdown has a name, and the summary counts them', () => {
  const ui = createUI();
  ui.open();
  ui.run('_comboEntries = [{ model: "alpha", disabled: false, source: "", provider: "" }, { model: "alpha", disabled: false, source: "", provider: "" }, { model: "", disabled: false, source: "", provider: "" }, { model: "beta", disabled: true, source: "", provider: "" }]; renderComboEntries()');
  const html = ui.el('comboEntries').innerHTML;
  assert.equal([...html.matchAll(/combo-entry combo-entry-dup/g)].length, 2);
  assert.equal([...html.matchAll(/combo-entry-empty/g)].length, 1);
  assert.equal([...html.matchAll(/class="delisted-badge">重复</g)].length, 2);
  assert.deepEqual([...html.matchAll(/<select aria-label="([^"]*)"/g)].map(m => m[1]), ['模型 1', '模型 2', '模型 3', '模型 4']);
  const summary = ui.el('comboEntriesSummary').innerHTML;
  assert.match(summary, /<b>3<\/b> 个条目/);
  assert.match(summary, /<b>2<\/b> 已启用/);
  assert.match(summary, /1 个空条目，保存时会被忽略/);
});

test('"remove empty entries" keeps the chosen ones in order and hides the warning', () => {
  const ui = createUI();
  ui.open();
  ui.run('_comboEntries = [{ model: "", disabled: false, source: "", provider: "" }, { model: "beta", disabled: false, source: "", provider: "" }, { model: "", disabled: false, source: "", provider: "" }]; renderComboEntries(); clearEmptyComboEntries()');
  assert.equal(ui.run('JSON.stringify(_comboEntries.map(e => e.model))'), '["beta"]');
  assert.doesNotMatch(ui.el('comboEntriesSummary').innerHTML, /空条目/);
});

test('with no entries the summary is empty and the hint points at both ways to add', () => {
  const ui = createUI();
  ui.open();
  assert.equal(ui.el('comboEntriesSummary').innerHTML, '');
  assert.match(ui.el('comboEntries').innerHTML, /多选添加模型.*添加条目/);
});

test('adding a row puts the cursor in its dropdown', () => {
  const ui = createUI();
  ui.open();
  const select = ui.el('newSelect');
  ui.el('comboEntries').querySelectorAll = () => [ui.el('firstSelect'), select];
  ui.run('addComboEntry()');
  assert.equal(ui.focused.at(-1), 'newSelect');
});

// ---------- model picker ----------
test('the picker filters by price, and by source; the count shows how many are visible', () => {
  const ui = createUI();
  ui.open();
  ui.run('openComboModelPicker()');
  const ids = () => [...ui.el('comboPickerList').innerHTML.matchAll(/class="combo-pick-id">([^<]+)</g)].map(m => m[1]).sort();
  assert.deepEqual(ids(), ['alpha', 'beta', 'delta', 'gamma'], 'a disabled model is never offered');
  assert.equal(ui.el('comboPickerCount').textContent, '已选 0 个模型');
  ui.run('setComboPickerTier("free")');
  assert.deepEqual(ids(), ['alpha', 'beta', 'delta']);
  assert.match(ui.el('comboPickerCount').textContent, /显示 3 \/ 4/);
  ui.run('setComboPickerTier("paid")');
  assert.deepEqual(ids(), ['gamma']);
  assert.deepEqual(ui.chips.map(chip => chip.attrs['aria-pressed']), ['false', 'false', 'true']);
  ui.run('setComboPickerTier("")');
  ui.el('comboPickerSource').value = 'zen';
  ui.run('renderComboPickerList()');
  assert.deepEqual(ids(), ['beta']);
  ui.el('comboPickerSource').value = 'prov:prov_acme';
  ui.run('setComboPickerTier("free")');
  assert.deepEqual(ids(), ['delta'], 'filters combine');
});

test('the source filter lists each source once, in the same order as the groups', () => {
  const ui = createUI();
  ui.open();
  ui.run('openComboModelPicker()');
  const values = [...ui.el('comboPickerSource').innerHTML.matchAll(/<option value="([^"]*)">([^<]*)</g)].map(m => m[1] + '=' + m[2]);
  assert.deepEqual(values, ['=全部来源', 'remote=Cline', 'zen=OpenCode Zen', 'prov:prov_acme=Acme']);
});

test('opening the picker again resets its filters', () => {
  const ui = createUI();
  ui.open();
  ui.run('openComboModelPicker(); setComboPickerTier("paid")');
  ui.el('comboPickerSource').value = 'zen';
  ui.run('closeComboModelPicker(); openComboModelPicker()');
  assert.equal(ui.run('_comboPickerTier'), '');
  assert.equal(ui.el('comboPickerSource').value, '');
  assert.equal(ui.chips[0].attrs['aria-pressed'], 'true');
});

test('"select all" selects only what is shown and not already in the combo', () => {
  const ui = createUI();
  ui.open();
  ui.run('_comboEntries = [{ model: "alpha", disabled: false, source: "cline", provider: "" }]');
  ui.run('openComboModelPicker(); setComboPickerTier("free"); comboPickerSelectAllShown()');
  const picked = JSON.parse(ui.run('JSON.stringify(_pickerOrder.map(k => JSON.parse(k.slice(9))[0]))'));
  assert.deepEqual(picked.sort(), ['beta', 'delta'], 'alpha is already in the combo; gamma is hidden by the filter');
  assert.match(ui.el('comboPickerCount').textContent, /已选 2 个模型/);
});

test('confirming the picker adds the chosen models and the editor shows them as unsaved', () => {
  const ui = createUI();
  edit(ui);
  ui.run('openComboModelPicker(); setComboPickerTier("paid"); comboPickerSelectAllShown(); comboPickerConfirm(false)');
  assert.equal(ui.run('_comboEntries.length'), 3);
  assert.equal(ui.run('comboEditorDirty()'), true);
});

// ---------- markup ----------
test('the editor markup has the copy button, announced error areas and the footer state', () => {
  const html = asset('html/dialogs/combo_editor.html');
  assert.match(html, /class="btn btn-sm combo-id-copy-btn" onclick="copyComboIdFromEditor\(this\)"[^>]*aria-label="复制组合 ID"/);
  for (const id of ['comboIdError', 'comboNameError', 'comboEntriesError']) assert.match(html, new RegExp('id="' + id + '" role="alert" hidden'));
  assert.match(html, /<input type="text" id="comboId"[^>]*aria-describedby="comboIdError"/);
  assert.match(html, /id="comboSaveBtn"[^>]*onclick="saveCombo\(\)"/);
  assert.match(html, /id="comboDirtyTag"[^>]*hidden/);
  assert.match(html, /id="comboEntriesSummary"[^>]*aria-live="polite"/);
  for (const id of ['comboId', 'comboName', 'comboDesc', 'comboCategory', 'comboEnabled', 'comboEmergency']) {
    assert.match(html, new RegExp('id="' + id + '"[^>]*(oninput|onchange)="comboEditorSync\\(\\)"'), id + ' feeds the dirty check');
  }
  assert.match(html, /onclick="openRoutingPolicyFromComboEditor\(\);return false"/, 'the link calls a named function; no inline logic in markup');
  assert.equal((html.match(/<div/g) || []).length, (html.match(/<\/div>/g) || []).length);
});

test('the picker markup has price chips and a source filter', () => {
  const html = asset('html/dialogs/model_sync.html');
  assert.match(html, /<div class="combo-pick-tiers" role="group" aria-label="计费筛选">/);
  for (const tier of ['', 'free', 'paid']) assert.match(html, new RegExp('data-tier="' + tier + '" aria-pressed="' + (tier === '' ? 'true' : 'false') + '" onclick="setComboPickerTier\\(\'' + tier + '\'\\)"'));
  assert.match(html, /<select id="comboPickerSource"[^>]*aria-label="来源"[^>]*onchange="renderComboPickerList\(\)">/);
});

test('the combo save bar takes part in the shared ordering and a copy icon exists', () => {
  assert.match(asset('js/03_shell.js'), /_SAVE_BAR_ORDER = \['provider', 'combo', 'tuning'\]/);
  assert.match(asset('js/01_core.js'), /^  copy: '/m);
});

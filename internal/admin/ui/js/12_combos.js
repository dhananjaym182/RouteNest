// ========== 模型组合（Combo） ==========
var _comboList = [];
var _comboCatalog = [];
var _comboReserved = [];
var _comboEntries = [];
var _editingComboId = '';
var _comboOrigin = null;      // 打开编辑器时的组合（新建为 null），「放弃更改」据此还原
var _comboBaseJson = '';      // 打开时的表单基线；与当前表单比较得到「未保存」
var _comboSaving = false;
var _comboShowErrors = false; // 保存失败过一次后，校验提示随输入实时更新
const COMBO_ID_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/; // 与服务端 combos_validation.go 一致
const COMBO_ID_MAX = 64;
const COMBO_MAX_ENTRIES = 64;

// loadCombos 拉取组合列表 + 模型目录 + 保留别名；可安全地重复调用
async function loadCombos() {
  try {
    const d = await api('GET', '/combos');
    _comboList = d.data.combos || [];
    _comboCatalog = d.data.modelCatalog || [];
    _comboReserved = d.data.reservedAliases || [];
    renderCombosList();
    // 编辑抽屉打开时目录可能已更新，重绘条目下拉（不丢失当前编辑内容）
    const ed = _('comboEditor');
    if (ed && ed.classList.contains('open')) renderComboEntries();
  } catch (e) {
    const box = _('combosList');
    if (box) box.innerHTML = '<div class="empty">' + t('加载失败') + ': ' + esc(e.message) + '</div>';
  }
}

// 组合列表渲染：id / 名称 / 分类 / 条目数 / 状态 / 操作
function renderCombosList() {
  const box = _('combosList');
  if (!box) return;
  const stats = _('combosStats');
  if (stats) {
    if (!_comboList.length) {
      stats.innerHTML = '';
    } else {
      const on = _comboList.filter(c => c.enabled !== false).length;
      const entryCount = _comboList.reduce((n, c) => n + ((c.entries || []).length), 0);
      stats.innerHTML = '<span><b>' + _comboList.length + '</b> ' + t('个组合') + '</span>' +
        '<span><span class="dot g"></span><b>' + on + '</b> ' + t('启用中') + '</span>' +
        '<span><b>' + entryCount + '</b> ' + t('条目总数') + '</span>';
    }
  }
  if (!_comboList.length) {
    box.innerHTML = listEmptyState(ICON_COMBOS, t('暂无模型组合'), '<button type="button" class="btn btn-sm btn-primary" onclick="showComboEditor()">' + t('新建组合') + '</button>');
    return;
  }
  // 与 Provider / 凭据表同一套 mgmt-table：列宽按内容自适应。全局 table-layout:fixed 会把
  // 剩余宽度平均分给 ID / 名称 / 分类 / 条目 / 状态：短列被撑得过宽，长 ID 与名称反而被挤。
  // 内联 handler 里的 ID：先 JSON 字符串化（在 JS 层合法），再做属性转义（在 HTML 层合法）
  const q = id => escAttr(JSON.stringify(String(id)));
  box.innerHTML = '<div class="mgmt-table-wrap combos-list"><table class="mgmt-table combos-table"><thead><tr>' +
    '<th>' + t('组合 ID') + '</th><th>' + t('名称') + '</th><th class="combo-col-fit">' + t('分类') + '</th>' +
    '<th class="mgmt-num combo-col-fit">' + t('条目') + '</th><th class="combo-col-fit">' + t('状态') + '</th><th class="combo-col-actions">' + t('操作') + '</th>' +
    '</tr></thead><tbody>' +
    _comboList.map(c => {
      const entries = c.entries || [];
      const enabledEntries = entries.filter(e => !e.disabled).length;
      const badge = c.enabled
        ? '<span class="status active"><span class="status-dot active"></span>' + t('已启用') + '</span>'
        : '<span class="status disabled"><span class="status-dot disabled"></span>' + t('已停用') + '</span>';
      const fb = c.emergencyFallback
        ? ' <span class="model-source-badge provider" title="' + t('允许跳出组合回退') + '">' + t('紧急回退') + '</span>'
        : '';
      return '<tr>' +
        '<td class="combo-col-id"><button type="button" class="combo-id-copy" onclick="copyComboId(' + q(c.id) + ', this)" title="' + escAttr(c.id + ' · ' + t('点击复制组合 ID')) + '" aria-label="' + escAttr(t('复制组合 ID') + ': ' + c.id) + '"><span class="combo-id-text">' + esc(c.id) + '</span>' + uiIcon('copy', 'ic-copy') + uiIcon('check', 'ic-done') + '</button></td>' +
        '<td class="combo-col-name"><span class="combo-name-text" title="' + escAttr(c.name || '') + '">' + esc(c.name || '') + '</span>' + fb + '</td>' +
        '<td class="combo-col-fit">' + (c.category ? '<span class="combo-category" title="' + escAttr(c.category) + '">' + esc(c.category) + '</span>' : '<span class="mgmt-muted">-</span>') + '</td>' +
        '<td class="mgmt-num combo-col-fit">' + enabledEntries + ' / ' + entries.length + '</td>' +
        '<td class="combo-col-fit">' + badge + '</td>' +
        '<td class="combo-col-actions"><div class="mgmt-actions">' +
          '<button class="btn btn-sm" onclick="testCombo(' + q(c.id) + ', this)">' + t('测试') + '</button>' +
          '<button class="btn btn-sm" onclick="editCombo(' + q(c.id) + ')">' + t('编辑') + '</button>' +
          '<button class="btn btn-sm" onclick="cloneCombo(' + q(c.id) + ', this)">' + t('克隆') + '</button>' +
          '<button class="btn btn-sm" onclick="toggleCombo(' + q(c.id) + ', ' + !c.enabled + ', this)">' + t(c.enabled ? '停用' : '启用') + '</button>' +
          '<button class="btn btn-sm btn-danger" onclick="deleteCombo(' + q(c.id) + ', this)" title="' + t('删除') + '" aria-label="' + t('删除') + '">' + uiIcon('close') + '</button>' +
        '</div></td>' +
      '</tr>';
    }).join('') +
    '</tbody></table></div>';
}

// 一键复制组合 ID（列表与编辑器共用）
function copyComboId(id, btn) {
  const value = String(id || '').trim();
  if (!value) return;
  copyText(value);
  if (!btn || !btn.classList) return;
  btn.classList.add('copied');
  clearTimeout(btn._copiedTimer);
  btn._copiedTimer = setTimeout(() => btn.classList.remove('copied'), 1600);
}

function copyComboIdFromEditor(btn) {
  const value = (_('comboId').value || '').trim();
  if (!value) { toast(t('请先填写组合 ID'), 'error'); return; }
  copyComboId(value, btn);
}

function showComboEditor(c) {
  _editingComboId = c ? c.id : '';
  _comboOrigin = c || null;
  _comboShowErrors = false;
  _comboSaving = false;
  const title = _('comboEditorTitle');
  if (title) title.textContent = c ? t('编辑组合') : t('新建组合');
  _('comboId').value = c ? c.id : '';
  _('comboId').disabled = !!c; // 编辑既有组合时 id 不可改（主键）
  _('comboName').value = c ? (c.name || '') : '';
  _('comboDesc').value = c ? (c.description || '') : '';
  _('comboCategory').value = c ? (c.category || '') : '';
  _('comboEnabled').value = c ? String(c.enabled !== false) : 'true';
  _('comboEmergency').checked = c ? !!c.emergencyFallback : false;
  _comboEntries = c ? (c.entries || []).map(e => ({ model: e.model || '', disabled: !!e.disabled, source: e.source || '', provider: e.provider || '' })) : [];
  _('comboSaveResult').textContent = '';
  showComboErrors([]);
  _comboBaseJson = JSON.stringify(comboPayload());
  // 关闭守卫覆盖 Esc / 遮罩 / 取消 / ✕；悬浮保存条提供「查看更改」与「放弃」
  if (typeof setDrawerGuard === 'function') setDrawerGuard('comboEditor', async () => !comboEditorDirty() || (await showConfirm(t('有未保存的修改，确定放弃？'))));
  if (typeof setSaveBarCtx === 'function') setSaveBarCtx('combo', {
    host: () => { const d = _('comboEditor'); return !!d && d.classList.contains('open'); },
    dirty: comboEditorDirty,
    diff: comboChanges,
    discard: () => showComboEditor(_comboOrigin),
    save: () => saveCombo(),
  });
  renderComboEntries();
  openDrawer('comboEditor');
  try { _(c ? 'comboName' : 'comboId').focus(); } catch (e) {}
}

// Link in the combo editor: leave the editor (unless the user cancels discarding edits), then open
// the global fallback chain. Kept out of the markup so the HTML carries no inline logic.
function openRoutingPolicyFromComboEditor() {
  return hideComboEditor().then(closed => { if (closed) showPageSubTab('routing', 'config'); });
}

// 返回 true 表示编辑器确实关闭了（守卫可能因未保存的修改而被用户拒绝）
async function hideComboEditor(force) {
  if (force) _comboBaseJson = JSON.stringify(comboPayload()); // 刚保存/刚删除：没有需要保留的修改
  await closeDrawer('comboEditor');
  const drawer = _('comboEditor');
  if (drawer && drawer.classList && drawer.classList.contains('open')) return false;
  _editingComboId = '';
  _comboEntries = [];
  _comboOrigin = null;
  _comboBaseJson = '';
  if (typeof setDrawerGuard === 'function') setDrawerGuard('comboEditor', null);
  if (typeof setSaveBarCtx === 'function') setSaveBarCtx('combo', null);
  return true;
}

// 与保存请求体同形：脏检查、差异预览、保存共用同一份数据
function comboPayload() {
  return {
    id: _('comboId').value.trim(),
    name: _('comboName').value.trim(),
    description: _('comboDesc').value.trim(),
    category: _('comboCategory').value.trim(),
    enabled: _('comboEnabled').value === 'true',
    emergencyFallback: _('comboEmergency').checked,
    entries: _comboEntries.filter(e => e.model).map(e => {
      const out = { model: e.model, disabled: !!e.disabled };
      if (e.source) {
        out.source = e.source;
        if (e.source === 'provider' && e.provider) out.provider = e.provider;
      }
      return out;
    }),
  };
}

function comboEditorDirty() {
  const drawer = _('comboEditor');
  if (!drawer || !drawer.classList || !drawer.classList.contains('open') || !_comboBaseJson) return false;
  return JSON.stringify(comboPayload()) !== _comboBaseJson;
}

// 字段或条目变化后统一刷新：未保存标记、保存按钮、条目摘要、悬浮保存条
function comboEditorSync() {
  const dirty = comboEditorDirty();
  const tag = _('comboDirtyTag');
  if (tag) tag.hidden = !dirty;
  const btn = _('comboSaveBtn');
  if (btn) {
    btn.disabled = _comboSaving || (!!_editingComboId && !dirty);
    if (!_comboSaving) btn.textContent = _editingComboId ? t('保存更改') : t('创建组合');
  }
  renderComboEntriesSummary();
  if (_comboShowErrors) showComboErrors(comboValidate());
  if (typeof refreshSaveBar === 'function') refreshSaveBar();
}

function renderComboEntriesSummary() {
  const box = _('comboEntriesSummary');
  if (!box) return;
  const filled = _comboEntries.filter(e => e.model).length;
  const empty = _comboEntries.length - filled;
  if (!_comboEntries.length) { box.innerHTML = ''; return; }
  const enabled = _comboEntries.filter(e => e.model && !e.disabled).length;
  box.innerHTML = '<span><b>' + filled + '</b> ' + t('个条目') + '</span>' +
    '<span><span class="dot g"></span><b>' + enabled + '</b> ' + t('已启用') + '</span>' +
    (empty ? '<span class="combo-summary-warn">' + empty + ' ' + t('个空条目，保存时会被忽略') +
      ' <button type="button" class="btn btn-sm" onclick="clearEmptyComboEntries()">' + t('清理空条目') + '</button></span>' : '');
}

function clearEmptyComboEntries() {
  _comboEntries = _comboEntries.filter(e => e.model);
  renderComboEntries();
}

// 与服务端 ValidateCombo 同一套规则：提前在对应字段下提示，而不是点保存后才失败
function comboValidate() {
  const errors = [];
  const add = (field, msg) => errors.push({ field: field, msg: msg });
  const editing = !!_editingComboId;
  const id = _('comboId').value.trim();
  const name = _('comboName').value.trim();
  if (!id) add('id', t('请填写组合 ID'));
  else if (!editing) {
    if (id.length > COMBO_ID_MAX) add('id', t('组合 ID 最长 64 个字符'));
    else if (!COMBO_ID_RE.test(id)) add('id', t('只能使用字母、数字、点、下划线和连字符，且须以字母或数字开头'));
    else if (_comboReserved.indexOf(id) !== -1) add('id', t('该 ID 为保留别名，请更换'));
    else if (_comboList.some(c => c.id === id)) add('id', t('该组合 ID 已存在'));
  }
  if (!name) add('name', t('请填写名称'));
  else if (_comboList.some(c => c.id !== _editingComboId && String(c.name || '').trim().toLowerCase() === name.toLowerCase())) add('name', t('该名称已被其他组合使用'));
  const filled = _comboEntries.filter(e => e.model);
  const seen = {};
  let duplicate = '';
  for (const e of filled) {
    const key = comboEntryKey(e);
    if (seen[key] && !duplicate) duplicate = e.model;
    seen[key] = true;
  }
  if (filled.some(e => e.model === 'free')) add('entries', t('「free」是保留别名，不能作为条目；请添加具体模型'));
  else if (duplicate) add('entries', t('条目中存在重复模型: ') + duplicate);
  else if (!filled.length) add('entries', t('至少需要一个模型条目'));
  else if (filled.length > COMBO_MAX_ENTRIES) add('entries', t('条目过多（最多 64 个）'));
  else if (_('comboEnabled').value === 'true' && !filled.some(e => !e.disabled)) add('entries', t('启用的组合至少需要一个启用的条目；也可以先把组合设为停用'));
  return errors;
}

function showComboErrors(errors) {
  const first = {};
  errors.forEach(e => { if (!first[e.field]) first[e.field] = e.msg; });
  [['id', 'comboIdError', 'comboId'], ['name', 'comboNameError', 'comboName'], ['entries', 'comboEntriesError', '']].forEach(row => {
    const box = _(row[1]);
    if (box) { box.hidden = !first[row[0]]; box.textContent = first[row[0]] || ''; }
    const input = row[2] ? _(row[2]) : null;
    if (input && typeof input.setAttribute === 'function') {
      if (first[row[0]]) input.setAttribute('aria-invalid', 'true'); else input.removeAttribute('aria-invalid');
    }
  });
}

function focusComboError(error) {
  try {
    if (error.field === 'entries') {
      const target = document.querySelector('#comboEntries .combo-entry-dup select, #comboEntries .combo-entry-empty select') || document.querySelector('#comboEntries select');
      if (target) { target.focus(); return; }
      const add = document.querySelector('#comboEditor .form-actions .btn-primary');
      if (add) add.focus();
      return;
    }
    _(error.field === 'id' ? 'comboId' : 'comboName').focus();
  } catch (e) {}
}

// 悬浮保存条的「查看更改」：标量字段 + 条目的增删、启停与顺序
function comboChanges() {
  const base = JSON.parse(_comboBaseJson || '{}');
  const cur = comboPayload();
  const scalars = p => ({
    [t('组合 ID')]: p.id, [t('名称')]: p.name, [t('描述')]: p.description, [t('分类')]: p.category,
    [t('启用')]: t(p.enabled ? '启用' : '停用'), [t('紧急回退')]: t(p.emergencyFallback ? '开启' : '关闭'),
  });
  const out = diffFlat(scalars(base), scalars(cur));
  const label = e => comboEntryLabel(e) || e.model;
  const baseEntries = base.entries || [], curEntries = cur.entries || [];
  const baseMap = {}, curMap = {};
  baseEntries.forEach(e => { baseMap[comboEntryKey(e)] = e; });
  curEntries.forEach(e => { curMap[comboEntryKey(e)] = e; });
  baseEntries.forEach(e => { if (!curMap[comboEntryKey(e)]) out.push({ label: t('移除条目'), from: label(e), to: '' }); });
  curEntries.forEach(e => { if (!baseMap[comboEntryKey(e)]) out.push({ label: t('添加条目'), from: '', to: label(e) }); });
  curEntries.forEach(e => {
    const before = baseMap[comboEntryKey(e)];
    if (before && !!before.disabled !== !!e.disabled) out.push({ label: label(e), from: t(before.disabled ? '停用' : '启用'), to: t(e.disabled ? '停用' : '启用') });
  });
  const common = list => list.map(comboEntryKey).filter(k => baseMap[k] && curMap[k]);
  const before = common(baseEntries), after = common(curEntries);
  if (before.join('\n') !== after.join('\n')) {
    out.push({ label: t('优先级顺序'), from: before.map(k => label(baseMap[k])).join(' → '), to: after.map(k => label(curMap[k])).join(' → ') });
  }
  return out;
}

document.addEventListener('keydown', e => {
  if (!(e.ctrlKey || e.metaKey) || String(e.key).toLowerCase() !== 's') return;
  const drawer = _('comboEditor');
  if (!drawer || !drawer.classList.contains('open') || _modalLayerOpen()) return;
  e.preventDefault();
  if (_editingComboId && !comboEditorDirty()) return; // 没有改动，无需保存（保存按钮此时也是禁用的）
  saveCombo();
});

// 条目编辑器渲染 + 优先级预览
// 行结构：拖拽手柄 / 优先级序号 / 模型下拉 / 上游钉选 / 徽章 / 启停与排序操作
// 拖拽：以 ⠿ 手柄作为 HTML5 drag 源，行本身作为落点（dragover/drop），落点用
//      inset 指示线 + 序号 + 优先级预览同步"模拟落位"后的新序号（live preview）。
function renderComboEntries() {
  const box = _('comboEntries');
  if (!box) return;
  if (!_comboEntries.length) {
    box.innerHTML = '<div class="combo-empty-entries">' + t('尚未添加条目，点击下方「多选添加模型」或「添加条目」开始。') + '</div>';
    renderComboPreview();
    comboEditorSync();
    return;
  }
  const chosen = {}, counts = {};
  _comboEntries.forEach(e => { if (e.model) { const k = comboEntryKey(e); chosen[k] = true; counts[k] = (counts[k] || 0) + 1; } });
  box.innerHTML = _comboEntries.map((e, i) => {
    const opts = buildComboModelOptions(e, chosen);
    const dup = e.model && counts[comboEntryKey(e)] > 1;
    const dis = (e.disabled ? ' disabled-entry' : '') + (dup ? ' combo-entry-dup' : '') + (e.model ? '' : ' combo-entry-empty');
    return '<div class="combo-entry' + dis + '" data-entry-idx="' + i + '" ondragover="comboDragOver(event, ' + i + ')" ondrop="comboDrop(event, ' + i + ')">' +
      '<span class="combo-entry-grip" draggable="true" ondragstart="comboDragStart(event, ' + i + ')" ondragend="comboDragEnd(event)" title="' + t('拖动调整顺序') + '" role="button" tabindex="0" aria-label="' + t('拖动调整顺序') + '">⠿</span>' +
      '<span class="combo-entry-idx">' + (i + 1) + '</span>' +
      '<span class="combo-entry-select"><select aria-label="' + escAttr(t('模型') + ' ' + (i + 1)) + '" onchange="onComboEntryModelChange(' + i + ', this.value)">' + opts + '</select></span>' +
      comboSourceSelectHtml(i, e) +
      '<span class="combo-entry-badges">' + comboEntryBadges(e) + (dup ? '<span class="delisted-badge">' + t('重复') + '</span>' : '') + '</span>' +
      '<span class="combo-entry-actions">' +
        '<label class="switch" title="' + t('启用/停用该条目') + '"><input type="checkbox"' + (e.disabled ? '' : ' checked') + ' onchange="onComboEntryDisabled(' + i + ', !this.checked)"><span class="slider"></span></label>' +
        '<button class="btn btn-sm" onclick="moveComboEntry(' + i + ', -1)"' + (i === 0 ? ' disabled' : '') + ' title="' + t('上移') + '" aria-label="' + t('上移') + '">↑</button>' +
        '<button class="btn btn-sm" onclick="moveComboEntry(' + i + ', 1)"' + (i === _comboEntries.length - 1 ? ' disabled' : '') + ' title="' + t('下移') + '" aria-label="' + t('下移') + '">↓</button>' +
        '<button class="btn btn-sm btn-danger" onclick="removeComboEntry(' + i + ')" title="' + t('移除') + '" aria-label="' + t('移除') + '">'+uiIcon('close')+'</button>' +
      '</span>' +
    '</div>';
  }).join('');
  renderComboPreview();
  comboEditorSync();
}

// 优先级预览：order 传入原下标数组时显示"模拟落位"后的顺序（拖拽 live preview 用）
function renderComboPreview(order) {
  const box = _('comboPreview');
  if (!box) return;
  const idxs = order || _comboEntries.map((_, k) => k);
  const enabled = idxs.filter(k => { const e = _comboEntries[k]; return e && !e.disabled && e.model; });
  if (!enabled.length) {
    box.innerHTML = '<span class="mut">' + t('（暂无启用的条目）') + '</span>';
    return;
  }
  box.innerHTML = enabled.map((k, i) =>
    '<span class="step">' + (i + 1) + '. ' + esc(comboEntryLabel(_comboEntries[k])) + '</span>'
  ).join('<span class="arrow">→</span>');
}

function addComboEntry() {
  _comboEntries.push({ model: '', disabled: false, source: '', provider: '' });
  renderComboEntries();
  const box = _('comboEntries');
  const selects = box && typeof box.querySelectorAll === 'function' ? box.querySelectorAll('.combo-entry-select select') : [];
  if (selects.length) selects[selects.length - 1].focus();
}

function removeComboEntry(i) {
  if (i < 0 || i >= _comboEntries.length) return;
  _comboEntries.splice(i, 1);
  renderComboEntries();
}

function onComboEntryModelChange(i, val) {
  const e = _comboEntries[i];
  if (!e) return;
  const offering = comboDecodeOfferingKey(val);
  if (offering) {
    const selected = comboSelectionEntry(val);
    e.model = selected.model; e.source = selected.source; e.provider = selected.provider;
    renderComboEntries();
    return;
  }
  e.model = val;
  // 新模型若不再提供当前 pin 的上游，则清空为「自动」，避免残留无效钉选
  if (e.source && !comboCatalogEntry(e)) { e.source = ''; e.provider = ''; }
  renderComboEntries();
}

// 上游选择变化："" 清空；cline/zen 设定来源；provider|<Name> 设定 Provider 来源
function onComboEntrySourceChange(i, value) {
  const e = _comboEntries[i];
  if (!e) return;
  if (value === 'cline' || value === 'zen') {
    e.source = value; e.provider = '';
  } else if (value && value.indexOf('provider|') === 0) {
    e.source = 'provider'; e.provider = value.slice(9);
  } else if (value) {
    try {
      const pin = JSON.parse(value);
      if (!Array.isArray(pin) || pin.length !== 2 || !pin.every(part => typeof part === 'string') || !['cline', 'zen', 'provider'].includes(pin[0])) return;
      e.source = pin[0]; e.provider = pin[0] === 'provider' ? pin[1] : '';
    } catch (error) { return; }
  } else {
    e.source = ''; e.provider = '';
  }
  renderComboEntries();
}

function onComboEntryDisabled(i, disabled) {
  if (_comboEntries[i]) _comboEntries[i].disabled = !!disabled;
  renderComboEntries();
}

function moveComboEntry(i, dir) {
  const j = i + dir;
  if (j < 0 || j >= _comboEntries.length) return;
  const tmp = _comboEntries[i];
  _comboEntries[i] = _comboEntries[j];
  _comboEntries[j] = tmp;
  renderComboEntries();
}

// ========== 组合条目拖拽排序（HTML5 drag & drop；↑↓ 按钮为无障碍兜底） ==========
let _comboDragFrom = -1; // 拖起条目的原下标
let _comboDragTo = -1;   // 最近 preview 的插入位置（移除原位后的坐标系，0..len）

function comboDragStart(ev, i) {
  _comboDragFrom = i;
  _comboDragTo = -1;
  if (ev.dataTransfer) {
    ev.dataTransfer.effectAllowed = 'move';
    try { ev.dataTransfer.setData('text/plain', String(i)); } catch (e) {}
  }
  const row = ev.target && ev.target.closest ? ev.target.closest('.combo-entry') : null;
  if (row) row.classList.add('combo-dragging');
}

// 计算插入位置：光标在落点行上半 → 插到该行前，否则插到该行后；
// 若拖起项在插入点之前，需先扣除自身占位。
function comboDragInsert(ev, overIdx) {
  if (_comboDragFrom < 0 || overIdx < 0 || overIdx >= _comboEntries.length) return -1;
  const rect = ev.currentTarget.getBoundingClientRect();
  const before = ev.clientY < rect.top + rect.height / 2;
  let ins = before ? overIdx : overIdx + 1;
  if (_comboDragFrom < ins) ins--;
  return ins;
}

// 模拟"移除 from，插入 ins"后原下标的新顺序（0..len-1）
function comboSimulatedOrder(ins) {
  const arr = _comboEntries.map((_, k) => k);
  arr.splice(_comboDragFrom, 1);
  arr.splice(Math.max(0, Math.min(arr.length, ins)), 0, _comboDragFrom);
  return arr;
}

// live preview：inset 指示线 + 各行序号 + 优先级预览同步为模拟落位后的顺序
function comboDragPreview(hoverRow, before, ins) {
  const box = _('comboEntries');
  if (!box) return;
  const rows = box.querySelectorAll('.combo-entry');
  rows.forEach(r => r.classList.remove('drop-before', 'drop-after'));
  if (hoverRow) hoverRow.classList.add(before ? 'drop-before' : 'drop-after');
  const order = comboSimulatedOrder(ins);
  const pos = new Array(_comboEntries.length);
  order.forEach((origIdx, p) => { pos[origIdx] = p; });
  rows.forEach(r => {
    const badge = r.querySelector('.combo-entry-idx');
    const oi = Number(r.getAttribute('data-entry-idx'));
    if (badge && oi >= 0 && Number.isFinite(pos[oi])) badge.textContent = pos[oi] + 1;
  });
  renderComboPreview(order);
}

function comboDragOver(ev, i) {
  ev.preventDefault();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  if (_comboDragFrom < 0) return;
  const rect = ev.currentTarget.getBoundingClientRect();
  const before = ev.clientY < rect.top + rect.height / 2;
  const ins = comboDragInsert(ev, i);
  if (ins < 0) return;
  _comboDragTo = ins;
  comboDragPreview(ev.currentTarget, before, ins);
}

function comboDrop(ev, i) {
  ev.preventDefault();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  let ins = _comboDragTo;
  if (_comboDragFrom >= 0 && (ins < 0 || ins === null || ins === undefined)) ins = comboDragInsert(ev, i);
  if (_comboDragFrom >= 0 && ins >= 0) {
    const item = _comboEntries.splice(_comboDragFrom, 1)[0];
    _comboEntries.splice(Math.max(0, Math.min(_comboEntries.length, ins)), 0, item);
    toast(t('已调整顺序'), 'success');
    renderComboEntries(); // 重建行：序号 / 上移下移按钮禁用态 / 预览全部复位
  }
  _comboDragFrom = -1;
  _comboDragTo = -1;
}

// dragend 兜底（取消拖拽 / 松手感复位）；成功 drop 后行已重建，此处为幂等清理
function comboDragEnd() {
  const box = _('comboEntries');
  if (box) box.querySelectorAll('.combo-entry').forEach(r => r.classList.remove('combo-dragging', 'drop-before', 'drop-after'));
  if (_comboDragFrom >= 0) renderComboEntries();
  _comboDragFrom = -1;
  _comboDragTo = -1;
}

// ========== 组合：多选模型选择器（从 /admin/api/combos 的 modelCatalog 取数） ==========
var _pickerOrder = []; // Offering keys in selection order.
var _comboPickerTier = '';  // '' | 'free' | 'paid'
var _comboPickerShown = 0, _comboPickerTotal = 0;

function setComboPickerTier(tier) {
  _comboPickerTier = tier === 'free' || tier === 'paid' ? tier : '';
  syncComboPickerTierChips();
  renderComboPickerList();
}

function syncComboPickerTierChips() {
  document.querySelectorAll('.combo-pick-tiers [data-tier]').forEach(btn => {
    const on = btn.dataset.tier === _comboPickerTier;
    btn.classList.toggle('active', on);
    btn.setAttribute('aria-pressed', String(on));
  });
}

// 来源筛选的选项取自当前目录（与分组标题同一套名称）
function fillComboPickerSources() {
  const sel = _('comboPickerSource');
  if (!sel) return;
  const groups = new Map();
  _comboCatalog.forEach(m => { if (!m.disabled && !groups.has(comboOptgroupKey(m))) groups.set(comboOptgroupKey(m), comboGroupLabel(m)); });
  sel.innerHTML = '<option value="">' + esc(t('全部来源')) + '</option>' +
    Array.from(groups.keys()).sort(comboGroupSortKey).map(k => '<option value="' + escAttr(k) + '">' + esc(groups.get(k)) + '</option>').join('');
  sel.value = '';
}

// 已在组合里的模型（选择器里标记「已添加」且不可再选）
function comboKeysInUse() {
  const used = {};
  _comboEntries.forEach(e => { if (e.model) used[comboEntryKey(e)] = true; });
  return used;
}

function openComboModelPicker() {
  if (!_comboCatalog || !_comboCatalog.length) {
    toast(t('模型目录为空，请先同步模型'), 'error');
    return;
  }
  _pickerOrder = [];
  _comboPickerTier = '';
  _('comboPickerSearch').value = '';
  syncComboPickerTierChips();
  fillComboPickerSources();
  const editing = _comboEntries.some(e => e.model);
  _('comboPickerReplaceBtn').style.display = editing ? '' : 'none';
  renderComboPickerList();
  _('comboModelPicker').style.display = 'flex';
  try { _('comboPickerSearch').focus(); } catch (e) {}
}

function closeComboModelPicker() {
  _('comboModelPicker').style.display = 'none';
  _pickerOrder = [];
}

function comboPickerToggle(model, checked) {
  const key = comboDecodeOfferingKey(model) ? comboEntryKey(comboDecodeOfferingKey(model)) : comboEntryKey({ model: model });
  const idx = _pickerOrder.indexOf(key);
  if (checked && idx === -1) _pickerOrder.push(key);
  if (!checked && idx !== -1) _pickerOrder.splice(idx, 1);
  renderComboPickerList();
}

// 工具条「全选」：把当前筛选结果里所有可选模型按显示顺序补进勾选序列
function comboPickerSelectAllShown() {
  const used = comboKeysInUse();
  pickerMergeIds(_pickerOrder, comboPickerVisibleModels().map(comboOfferingKey).filter(key => !used[key]));
  renderComboPickerList();
}

function comboPickerClear() {
  _pickerOrder = [];
  renderComboPickerList();
}

// 当前筛选条件下可展示的模型（跳过管理页已停用；与条目下拉同一条规则）
function comboPickerVisibleModels() {
  const q = (_('comboPickerSearch').value || '').trim().toLowerCase();
  const rx = q.replace(/[.*+\-?^${}()|[\]\\]/g, '\\$&');
  const sourceEl = _('comboPickerSource'), source = sourceEl ? sourceEl.value : '';
  return _comboCatalog.filter(m => !m.disabled &&
    (!_comboPickerTier || (_comboPickerTier === 'free') === (m.cost === 'free')) &&
    (!source || comboOptgroupKey(m) === source) &&
    (!rx || (m.id + ' ' + (m.qualifiedId || '') + ' ' + comboSourceName(m)).toLowerCase().search(rx) !== -1));
}

// 已在组合中的模型不允许重复勾选（标记「已添加」且复选框禁用）
function renderComboPickerList() {
  const box = _('comboPickerList');
  if (!box) return;
  const scrollTop = box.scrollTop;
  const inCombo = comboKeysInUse();
  const visible = comboPickerVisibleModels();
  _comboPickerTotal = new Set(_comboCatalog.filter(m => !m.disabled).map(comboOfferingKey)).size;
  _comboPickerShown = 0;
  if (!visible.length) {
    box.innerHTML = '<div class="empty">' + t('没有符合条件的模型') + '</div>';
    renderComboPickerCount();
    return;
  }
  // 按来源归组（与条目下拉一致：Provider 用真实名）
  const groups = new Map();
  const seenSrc = {}; // 同一模型多上游时只展示一次
  for (const m of visible) {
    const gk = comboOptgroupKey(m);
    if (!groups.has(gk)) groups.set(gk, { label: comboGroupLabel(m), items: [] });
    const g = groups.get(gk);
    const key = comboOfferingKey(m);
    if (!seenSrc[key]) {
      seenSrc[key] = true;
      _comboPickerShown++;
      g.items.push(m);
    }
  }
  const gkeys = Array.from(groups.keys()).sort(comboGroupSortKey);
  let html = '';
  for (const gk of gkeys) {
    const g = groups.get(gk);
    html += '<div class="combo-pick-group">' + esc(g.label) + '</div>';
    g.items.sort((a, b) => a.id.localeCompare(b.id));
    for (const m of g.items) {
      const key = comboOfferingKey(m), rank = _pickerOrder.indexOf(key);
      const used = inCombo[key];
      html += '<label class="combo-pick-row' + (used ? ' combo-pick-used' : '') + '">' +
        '<input type="checkbox" value="' + escAttr(key) + '"' + (rank !== -1 ? ' checked' : '') + (used ? ' disabled' : '') + ' onchange="comboPickerToggle(this.value, this.checked)">' +
        '<span class="combo-pick-id">' + esc(m.qualifiedId || m.id) + '</span>' +
        '<span class="model-source-badge ' + (m.source === 'provider' ? 'provider' : (m.source === 'zen' || m.source === 'seed' ? 'zen' : (m.source === 'remote' ? 'remote' : 'custom'))) + '">' + esc(comboSourceName(m)) + '</span>' +
        (m.cost === 'free'
          ? '<span class="model-tag free tag-xs">' + t('免费') + '</span>'
          : '<span class="model-tag pass tag-xs">' + t('付费') + '</span>') +
        (used ? '<span class="model-source-badge" title="' + t('已在组合条目中，无需重复添加') + '">' + t('已添加') + '</span>' : '') +
        (rank !== -1 ? '<span class="combo-pick-rank" title="' + t('勾选顺序即尝试优先级') + '">#' + (rank + 1) + '</span>' : '') +
      '</label>';
    }
  }
  box.innerHTML = html;
  box.scrollTop = scrollTop;
  renderComboPickerCount();
}

function renderComboPickerCount() {
  const el = _('comboPickerCount');
  if (!el) return;
  const n = _pickerOrder.length;
  el.textContent = t('已选 ') + n + ' ' + t('个模型') +
    (_comboPickerShown !== _comboPickerTotal ? '  ·  ' + t('显示 ') + _comboPickerShown + ' / ' + _comboPickerTotal : '');
}

// 确认：append=false 追加到现有条目之后；append=true（替换整表）用勾选顺序重构全部条目
function comboPickerConfirm(replace) {
  if (!_pickerOrder.length) { toast(t('请先勾选至少一个模型'), 'error'); return; }
  const selected = _pickerOrder.map(comboSelectionEntry).filter(entry => entry.model);
  if (replace) {
    _comboEntries = selected;
  } else {
    for (const entry of selected) {
      if (_comboEntries.some(current => comboEntryKey(current) === comboEntryKey(entry))) continue;
      _comboEntries.push(entry);
    }
  }
  closeComboModelPicker();
  renderComboEntries();
  toast(replace ? t('已按勾选顺序替换全部条目') : t('已按勾选顺序追加 ') + selected.length + t(' 个条目'), 'success');
}

async function saveCombo() {
  if (_comboSaving) return;
  const res = _('comboSaveResult');
  if (res) res.textContent = '';
  const errors = comboValidate();
  _comboShowErrors = errors.length > 0;
  showComboErrors(errors);
  if (errors.length) {
    toast(errors[0].msg, 'error');
    focusComboError(errors[0]);
    return;
  }
  const payload = comboPayload();
  _comboSaving = true;
  const btn = _('comboSaveBtn');
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/combos/save', payload);
    toast(t('组合已保存'), 'success');
    _comboSaving = false;
    await hideComboEditor(true);
    await loadCombos();
  } catch (e) {
    if (res) res.innerHTML = '<span class="save-result err">' + esc(e.message) + '</span>';
    toast(t('保存失败: ') + e.message, 'error');
  } finally {
    _comboSaving = false;
    comboEditorSync();
  }
}

function editCombo(id) {
  const c = _comboList.find(x => x.id === id);
  if (c) showComboEditor(c);
}

async function toggleCombo(id, enabled, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/combos/toggle', { id: id, enabled: !!enabled });
    toast(t(enabled ? '已启用' : '已停用') + ': ' + id, 'success');
    await loadCombos();
  } catch (e) { toast(t('保存失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

async function deleteCombo(id, btn) {
  if (!(await showConfirm(t('确定删除该组合？')))) return;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('删除中...'); }
  try {
    await api('POST', '/combos/delete', { id: id });
    toast(t('已删除'), 'success');
    if (_editingComboId === id) await hideComboEditor(true);
    await loadCombos();
  } catch (e) { toast(t('删除失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

async function cloneCombo(id, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('克隆中...'); }
  try {
    await api('POST', '/combos/clone', { id: id });
    toast(t('已克隆'), 'success');
    await loadCombos();
  } catch (e) { toast(t('克隆失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

// ========== 组合连通性测试（/combos/test 纯只读探测 + 结果内启停/移除工作副本） ==========
let _comboTestCopy = null;    // 正在测试的组合的工作副本（结构同 _comboList 条目，保存前不落盘）
let _comboTestChanged = false;
let _comboTestResults = [];   // 服务端返回的逐条目结果（仅含启用条目，顺序与条目一致）

async function testCombo(id, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('测试中...'); }
  try {
    const c = _comboList.find(x => x.id === id);
    if (!c) { toast(t('未找到该组合'), 'error'); return; }
    const d = await api('POST', '/combos/test', { id: id });
    const r = d.data || {};
    // 工作副本：所有行内启停/移除只改这里，保存更改后才经既存 /combos/save 流落盘
    _comboTestCopy = {
      id: c.id, name: c.name || '', description: c.description || '', category: c.category || '',
      enabled: c.enabled !== false, emergencyFallback: !!c.emergencyFallback,
      entries: (c.entries || []).map(e => ({ model: e.model || '', disabled: !!e.disabled, source: e.source || '', provider: e.provider || '' })),
    };
    _comboTestChanged = false;
    _comboTestResults = r.results || [];
    renderComboTestModal();
    _('comboTestModal').style.display = 'flex';
  } catch (e) {
    // 400（组合无启用条目）等业务错误原样透出；404/接口缺失 → 重启提示
    const box = _('comboTestList');
    if (box) box.innerHTML = '<div class="empty save-result err">' + esc(friendlyTestError(e)) + '</div>';
    const summary = _('comboTestSummary');
    if (summary) summary.style.display = 'none';
    const saveBtn = _('comboTestSaveBtn');
    if (saveBtn) saveBtn.style.display = 'none';
    const resLbl = _('comboTestResult');
    if (resLbl) resLbl.textContent = '';
    const idLbl = _('comboTestId');
    if (idLbl) idLbl.textContent = '';
    _comboTestCopy = null;
    _comboTestChanged = false;
    _comboTestResults = [];
    _('comboTestModal').style.display = 'flex';
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

// Results retain the original offering identity when entries are toggled or removed.
function comboTestResultDetails() {
  const results = new Map(_comboTestResults.map(result => [comboEntryKey(result), result]));
  return (_comboTestCopy ? _comboTestCopy.entries : []).map(entry => ({
    entry,
    res: entry.model && !entry.disabled ? results.get(comboEntryKey(entry)) || null : null,
  }));
}

// 复用 comboPinLabel 的上游-pin 文案：结果自带 upstream（"provider"|"opencode"|"cline"）
function comboTestUpstreamBadge(upstream, entry, result) {
  if (!upstream) return '';
  const map = { opencode: 'zen', cline: 'remote', provider: 'provider' };
  const label = upstream === 'opencode' ? 'OpenCode Zen' : (upstream === 'cline' ? 'Cline' : comboPinLabel({ source: 'provider', provider: result && result.provider || entry && entry.provider || '', providerName: result && result.providerName }));
  return '<span class="model-source-badge ' + (map[upstream] || '') + '" title="' + t('上游') + '">' + esc(label) + '</span>';
}

function comboTestSourcePin(e) {
  const src = comboRouteSource(e.source);
  if (src === 'provider') return '<span class="model-source-badge provider">' + esc(comboPinLabel(e)) + '</span>';
  if (src === 'zen') return '<span class="model-source-badge zen">OpenCode Zen</span>';
  if (src === 'cline') return '<span class="model-source-badge remote">Cline</span>';
  return '<span class="model-source-badge" title="' + t('自动') + '">' + t('自动') + '</span>';
}

function renderComboTestModal() {
  const box = _('comboTestList');
  if (!box) return;
  const idLbl = _('comboTestId');
  if (idLbl) idLbl.textContent = _comboTestCopy ? _comboTestCopy.id : '';
  const details = comboTestResultDetails();
  const active = details.filter(d => d.entry.model && !d.entry.disabled);
  const results = active.map(d => d.res).filter(Boolean);
  const pending = active.length - results.length;
  // 摘要头：全绿 → 紧凑 ✓ 徽章；有失败 → 失败计数徽章（明细在行内）
  const allOk = results.length > 0 && pending === 0 && results.every(r => !!r.ok);
  const summary = _('comboTestSummary');
  if (summary) {
    const fails = results.filter(r => !r.ok).length;
    if (!active.length) {
      summary.style.display = 'none';
    } else if (allOk) {
      summary.textContent = t('✓ 全部通过');
      summary.className = 'model-source-badge zen';
      summary.style.display = '';
    } else if (fails) {
      summary.textContent = fails + ' ✗ ' + t('失败');
      summary.className = 'model-source-badge remote';
      summary.style.display = '';
    } else {
      summary.textContent = pending + ' · ' + t('未测试');
      summary.className = 'model-source-badge';
      summary.style.display = '';
    }
  }
  const resLbl = _('comboTestResult');
  if (resLbl) resLbl.textContent = '';
  const saveBtn = _('comboTestSaveBtn');
  if (saveBtn) saveBtn.style.display = _comboTestChanged ? '' : 'none';
  if (!details.length) {
    box.innerHTML = '<div class="empty">' + t('（暂无启用的条目）') + '</div>';
    return;
  }
  box.innerHTML = details.map((d, i) => {
    const e = d.entry, res = d.res;
    const verdict = !res
      ? '<span class="combo-test-verdict mut">—</span>'
      : (res.ok
        ? '<span class="combo-test-verdict save-result ok">' + esc((res.durationMs || 0) + 'ms') + '</span>'
        : '<span class="combo-test-verdict save-result err">✗</span>');
    const errHtml = res && !res.ok
      ? '<span class="combo-test-error" title="' + escAttr(res.error || '') + '">' + esc(res.error || '') + '</span>'
      : '';
    // 移除只对已测失败的条目开放（保存确认前仅改工作副本，取消即放弃）
    const removeBtn = res && !res.ok
      ? '<button class="btn btn-sm btn-danger" onclick="comboTestRemoveEntry(' + i + ')" title="' + t('移除') + '" aria-label="' + t('移除') + '">'+uiIcon('close')+'</button>'
      : '';
    return '<div class="combo-test-row' + (e.disabled ? ' combo-test-disabled' : '') + '">' +
      '<span class="combo-entry-idx">' + (i + 1) + '</span>' +
      '<span class="combo-pick-id" style="flex:1 1 160px">' + esc(comboEntryLabel(e)) + '</span>' +
      comboTestSourcePin(e) +
      (res ? comboTestUpstreamBadge(res.upstream, e, res) : '') +
      verdict + errHtml +
      '<span class="combo-entry-actions">' +
        '<label class="switch" title="' + t('启用/停用该条目') + '"><input type="checkbox"' + (e.disabled ? '' : ' checked') + ' onchange="comboTestToggleEntry(' + i + ', !this.checked)"><span class="slider"></span></label>' +
        removeBtn +
      '</span>' +
    '</div>';
  }).join('');
}

function comboTestToggleEntry(i, disabled) {
  if (!_comboTestCopy || !_comboTestCopy.entries[i]) return;
  _comboTestCopy.entries[i].disabled = !!disabled;
  _comboTestChanged = true;
  renderComboTestModal();
}

function comboTestRemoveEntry(i) {
  if (!_comboTestCopy || !_comboTestCopy.entries[i]) return;
  _comboTestCopy.entries.splice(i, 1);
  _comboTestChanged = true;
  renderComboTestModal();
}

function closeComboTest() {
  const m = _('comboTestModal');
  if (m) m.style.display = 'none';
  _comboTestCopy = null;
  _comboTestChanged = false;
  _comboTestResults = [];
}

// 保存更改：仅在有修改时可用；走既存保存接口（/combos/save，与 saveCombo 同一载荷结构）
async function comboTestSave(btn) {
  if (!_comboTestCopy || !_comboTestChanged) { closeComboTest(); return; }
  const entries = _comboTestCopy.entries.filter(e => e.model).map(e => {
    const out = { model: e.model, disabled: !!e.disabled };
    if (e.source) {
      out.source = e.source;
      if (e.source === 'provider' && e.provider) out.provider = e.provider;
    }
    return out;
  });
  const payload = {
    id: _comboTestCopy.id,
    name: _comboTestCopy.name,
    description: _comboTestCopy.description,
    category: _comboTestCopy.category,
    enabled: _comboTestCopy.enabled,
    emergencyFallback: _comboTestCopy.emergencyFallback,
    entries: entries,
  };
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/combos/save', payload);
    toast(t('组合已保存'), 'success');
    closeComboTest();
    await loadCombos();
  } catch (e) {
    const resLbl = _('comboTestResult');
    if (resLbl) resLbl.innerHTML = '<span class="save-result err">' + esc(e.message) + '</span>';
    toast(t('保存失败: ') + e.message, 'error');
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

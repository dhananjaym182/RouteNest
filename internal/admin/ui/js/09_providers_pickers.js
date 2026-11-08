// ========== Provider：模型多选弹窗（免费/付费分组 + §9.3 分级信号） ==========
let _provPickerModels = [];   // 上游返回的全部模型 id（服务端已去重排序，保留原顺序）
let _provPickerTiers = {};    // id → 'free' | 其他（来自分级预览的有序信号判定）
let _provPickerOrder = [];    // 勾选顺序（确认后即追加顺序）
let _provPickerExisting = {}; // 编辑器中已有的模型 id（预勾选且禁用，确认时跳过）
let _provPickerStats = null;  // 本批次服务端 stats（免费/付费全量口径，与导入预览同一批次）

// 已有 Provider 用 {id}（服务端补全 baseURL/key/headers）；新建直接传表单未保存值
function provEditorPayload() {
  return _editingProviderId
    ? { id: _editingProviderId }
    : { baseURL: _('provBaseURL').value.trim(), apiKey: _('provKey').value.trim(), headers: parseHeaderText(_('provHeaders').value) };
}

async function fetchProviderModels(btn) {
  if (btn && btn.disabled) return;
  const providerID = _editingProviderId, revision = _provEditorRevision;
  const removalEpoch = providerModelRemovalEpoch(providerID);
  const current = () => _editingProviderId === providerID && _provEditorRevision === revision && providerModelRemovalEpoch(providerID) === removalEpoch && !!_('providerEditor') && _('providerEditor').classList.contains('open');
  const payload = provEditorPayload();
  if (!_editingProviderId && !payload.baseURL) { toast(t('请先填写 Base URL'), 'info'); return; }
  if (!_editingProviderId) {
    payload.free = _('provFree').value === 'true';
    payload.name = _('provName').value.trim();
  }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('拉取中...'); }
  const out = _('provTestResult');
  if (out) out.innerHTML = '';
  try {
    // 与导入预览共用同一批次：同一 Provider/草稿在 60s 内已预览过就直接复用，
    // 保证选择器与导入预览的免费/付费数完全一致（不再各自拉一次上游）。
    const key = importPreviewCacheKey(payload);
    let batch = importPreviewCacheGet(key);
    if (!batch) {
      // 分级预览端点一次给回 id + free/paid 判定（pricing/后缀/白名单/整源免费有序信号）
      const d = await api('POST', '/models/import/preview', payload);
      if (!current()) return;
      const r = d.data || {};
      batch = r.batch;
      if (!batch) {
        // 上游失败（HTTP 200 + {error}）：不是 API 错误，就地提示排查 Base URL / Key
        if (out) out.innerHTML = renderTestResultHTML(false,
          t('未获取到模型：请先检查 Base URL 与 API Key') + (r.error ? ' — ' + r.error : ''));
        return;
      }
      importPreviewCacheSet(key, batch);
    }
    const items = ((batch && batch.items) || []).filter(it => it && it.modelId);
    if (!items.length) {
      if (out) out.innerHTML = renderTestResultHTML(false,
        t('未获取到模型：请先检查 Base URL 与 API Key'));
      return;
    }
    _provPickerModels = Array.from(new Set(items.map(it => it.modelId)));
    _provPickerTiers = {};
    items.forEach(it => { _provPickerTiers[it.modelId] = it.tier; });
    _provPickerStats = importStatsFromBatch(batch);
    _provPickerOrder = [];
    _provPickerExisting = {};
    _('provModels').value.split(',').map(s => s.trim()).filter(Boolean).forEach(id => { _provPickerExisting[id] = true; });
    _('provPickerSearch').value = '';
    // 静默拉取（保存后自动预览）期间抽屉可能已被用户关闭：此时不弹选择器，避免孤儿弹层
    const drawer = _('providerEditor');
    if (!drawer || !drawer.classList.contains('open')) return;
    renderProvPickerList();
    _('provPickerModal').style.display = 'flex';
    try { _('provPickerSearch').focus(); } catch (e) {}
  } catch (e) {
    if (current() && out) out.innerHTML = renderTestResultHTML(false, friendlyTestError(e));
  } finally {
    if (btn && _provEditorRevision === revision) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

function closeProvModelPicker() {
  const m = _('provPickerModal');
  if (m) m.style.display = 'none';
  _provPickerOrder = [];
}

function provPickerVisible() {
  const q = (_('provPickerSearch').value || '').trim().toLowerCase();
  const rx = q.replace(/[.*+\-?^${}()|[\]\\]/g, '\\$&');
  return _provPickerModels.filter(id => !rx || id.toLowerCase().search(rx) !== -1);
}

function provPickerToggle(id, checked) {
  const idx = _provPickerOrder.indexOf(id);
  if (checked && idx === -1) _provPickerOrder.push(id);
  if (!checked && idx !== -1) _provPickerOrder.splice(idx, 1);
  renderProvPickerList();
}

// 工具条「全选」：把当前筛选结果里所有可勾选的模型补进勾选序列（已有的跳过，不可再选）
function provPickerSelectAllShown() {
  pickerMergeIds(_provPickerOrder, provPickerVisible().filter(id => !_provPickerExisting[id]));
  renderProvPickerList();
}

// 分组「全选」：只把当前筛选结果里该层级（free / 非 free）可勾选的模型补进序列
function provPickerSelectAllTier(tier) {
  pickerMergeIds(_provPickerOrder, provPickerVisible().filter(id =>
    ((tier === 'free') === (_provPickerTiers[id] === 'free')) && !_provPickerExisting[id]));
  renderProvPickerList();
}

function provPickerClear() { _provPickerOrder = []; renderProvPickerList(); }

function renderProvPickerList() {
  const box = _('provPickerList');
  if (!box) return;
  const scrollTop = box.scrollTop;
  const visible = provPickerVisible();
  if (!visible.length) {
    box.innerHTML = '<div class="empty">' + t('没有匹配的模型') + '</div>';
    renderProvPickerCount();
    return;
  }
  const row = id => {
    const rank = _provPickerOrder.indexOf(id);
    const used = !!_provPickerExisting[id];
    const checked = used || rank !== -1;
    return '<label class="combo-pick-row' + (used ? ' combo-pick-used' : '') + '">' +
      '<input type="checkbox"' + (checked ? ' checked' : '') + (used ? ' disabled' : '') + ' onchange="provPickerToggle(\'' + escAttr(id) + '\', this.checked)">' +
      '<span class="combo-pick-id">' + esc(id) + '</span>' +
      (used ? '<span class="model-source-badge" title="' + t('已在 Provider 中，无需重复添加') + '">' + t('已添加') + '</span>' : '') +
      (rank !== -1 ? '<span class="combo-pick-rank" title="' + t('勾选顺序即尝试优先级') + '">#' + (rank + 1) + '</span>' : '') +
    '</label>';
  };
  const groupTitle = (label, n, tier, total) =>
    '<div class="preset-group-title">' + label + ' ' + n + (total > n ? ' / ' + total : '') +
    '<span class="grp-scope">' + t('（全部可拉取）') + '</span>' +
    '<button type="button" class="btn btn-sm float-note" onclick="event.preventDefault();provPickerSelectAllTier(\'' + tier + '\')">' + t('全选') + '</button></div>';
  const freeIds = visible.filter(id => _provPickerTiers[id] === 'free');
  const paidIds = visible.filter(id => _provPickerTiers[id] !== 'free');
  const localFree = _provPickerModels.filter(id => _provPickerTiers[id] === 'free').length;
  const localPaid = _provPickerModels.length - localFree;
  const st = _provPickerStats || {};
  const totalFree = Number.isFinite(Number(st.free)) ? Number(st.free) : localFree;
  const totalPaid = Number.isFinite(Number(st.paid)) ? Number(st.paid) : localPaid;
  box.innerHTML =
    (freeIds.length ? groupTitle(t('免费'), freeIds.length, 'free', totalFree) + freeIds.map(row).join('') : '') +
    (paidIds.length ? groupTitle(t('付费'), paidIds.length, 'pass', totalPaid) + paidIds.map(row).join('') : '');
  box.scrollTop = scrollTop;
  renderProvPickerCount();
}

function renderProvPickerCount() {
  const el = _('provPickerCount');
  if (!el) return;
  const nFree = _provPickerOrder.filter(id => _provPickerTiers[id] === 'free').length;
  // 这里统计的是「勾选里」的免费数，与批次全量免费数区分开
  el.textContent = t('已选 ') + _provPickerOrder.length + ' / ' + _provPickerModels.length + t(' 个模型') +
    (nFree ? ' · ' + t('勾选中免费') + ' ' + nFree : '');
}

// 确认：写入「模型 ID」真值（隐藏的高级输入框），芯片即时回显
function provPickerConfirm() {
  if (!_provPickerOrder.length) { toast(t('请先勾选至少一个模型'), 'error'); return; }
  const cur = _('provModels').value.split(',').map(s => s.trim()).filter(Boolean);
  const seen = {};
  cur.forEach(id => { seen[id] = true; });
  let added = 0;
  for (const id of _provPickerOrder) {
    if (seen[id]) continue; // 防重复
    cur.push(id);
    seen[id] = true;
    added++;
  }
  closeProvModelPicker();
  if (added) {
    _('provModels').value = cur.join(', ');
    renderProvModelChips();
    toast(t('已按勾选顺序追加 ') + added + t(' 个模型'), 'success');
  } else {
    toast(t('所选模型均已在 Provider 中'), 'info');
  }
  try { _('provModelsGroup').scrollIntoView({ block: 'nearest' }); } catch (e) {}
}

// ========== 预设选择弹窗（/providers/presets，按接入成本分组） ==========
let _presetList = [];
let _presetFilter = ''; // ''=全部 | kind | 'login'（需登录）
const _presetKinds = [
  {kind:'free',     label:'免密钥直连', desc:'无需 API Key；应用后自动拉取模型',        badge:'免密钥',  cls:'model-source-badge custom'},
  {kind:'freeTier', label:'免费额度',   desc:'注册领取免费 API Key',                    badge:'免费额度', cls:'model-source-badge zen'},
  {kind:'hasFree',  label:'含免费模型', desc:'混合目录：需 Key，部分模型免费',           badge:'含免费',  cls:'model-source-badge provider'},
  {kind:'local',    label:'本地服务',   desc:'本机推理端点（LM Studio、llama.cpp 等）', badge:'本地',    cls:'model-source-badge'},
  {kind:'other',    label:'其他',       desc:'',                                       badge:'',       cls:'model-source-badge'},
];

async function loadProviderPresets() {
  try {
    if (!_presetList.length) {
      const d = await api('GET', '/providers/presets');
      _presetList = d.data.presets || [];
    }
    if (!_presetList.length) { toast(t('加载失败'), 'error'); return; }
    _('presetSearch').value = '';
    _presetFilter = '';
    renderPresetFilterChips();
    renderPresetPickerList();
    _('presetPickerModal').style.display = 'flex';
    try { _('presetSearch').focus(); } catch (e) {}
  } catch (e) {
    toast(t('加载失败') + ': ' + esc(e.message), 'error');
  }
}

function closePresetPicker() {
  _('presetPickerModal').style.display = 'none';
}

function renderPresetFilterChips() {
  const box = _('presetFilterChips');
  if (!box) return;
  const defs = [{ f: '', label: '全部' }]
    .concat(_presetKinds.map(g => ({ f: g.kind, label: g.label })))
    .concat([{ f: 'login', label: '需登录' }]);
  box.innerHTML = defs.map(d =>
    '<span class="chip chip-btn' + (_presetFilter === d.f ? ' active' : '') + '" onclick="setPresetFilter(\'' + d.f + '\')">' + t(d.label) + '</span>').join('');
}

function setPresetFilter(f) {
  _presetFilter = (_presetFilter === f && f !== '') ? '' : f;
  renderPresetFilterChips();
  renderPresetPickerList();
}

function presetRowHTML(p) {
  const g = _presetKinds.find(x => x.kind === (p.kind || 'other')) || _presetKinds[_presetKinds.length - 1];
  return '<div class="preset-row" onclick="applyPreset(\'' + esc(p.key) + '\')" title="' + esc(p.baseURL || '') + '">' +
    '<span class="preset-name">' + esc(p.name) + '</span>' +
    '<span class="preset-key">' + esc(p.baseURL || p.key) + '</span>' +
    '<span class="row-inline">' +
    (p.loginFlow ? '<span class="model-source-badge zen" title="' + t('设备码登录：在「凭据 → Provider OAuth」添加，再在编辑器「Provider OAuth 账号」中选择；无需 API Key') + '">' + t('需登录') + '</span>' : (g.badge ? '<span class="' + g.cls + '">' + t(g.badge) + '</span>' : '')) +
    (p.modelIDs && p.modelIDs.length ? '<span class="model-source-badge">' + p.modelIDs.length + ' ' + t('个模型') + '</span>' : '') +
    '</span></div>';
}

function renderPresetPickerList() {
  const q = (_('presetSearch').value || '').trim().toLowerCase();
  const list = _presetList.filter(p => {
    if (_presetFilter === 'login') { if (!p.loginFlow) return false; }
    else if (_presetFilter && (p.kind || 'other') !== _presetFilter) return false;
    return !q || p.key.toLowerCase().includes(q) || (p.name || '').toLowerCase().includes(q) || (p.baseURL || '').toLowerCase().includes(q);
  });
  _('presetCount').textContent = list.length + ' / ' + _presetList.length;
  if (!list.length) {
    _('presetPickerList').innerHTML = '<div class="empty">' + t('没有匹配的预设') + '</div>';
    return;
  }
  const byName = (a, b) => (a.name || '').localeCompare(b.name || '');
  if (_presetFilter === 'login') {
    _('presetPickerList').innerHTML = '<div class="preset-group-title">' + t('需登录') + '</div>' +
      list.slice().sort(byName).map(presetRowHTML).join('');
    return;
  }
  _('presetPickerList').innerHTML = _presetKinds.map(g => {
    const items = list.filter(p => (p.kind || 'other') === g.kind).sort(byName);
    if (!items.length) return '';
    return '<div class="preset-group-title">' + t(g.label) +
      (g.desc ? '<span class="preset-group-desc">· ' + t(g.desc) + '</span>' : '') + '</div>' +
      items.map(presetRowHTML).join('');
  }).join('');
}

function applyPreset(key) {
  const p = _presetList.find(x => x.key === key);
  if (!p) return;
  closePresetPicker();
  showProviderEditor(null);
  _('provName').value = p.name;
  _('provBaseURL').value = p.baseURL;
  _('provHeaders').value = Object.entries(p.headers || {}).map(([k, v]) => k + ': ' + v).join('\n');
  _('provModels').value = (p.modelIDs || []).join(', ');
  renderProvModelChips();
  // hasFree/other 是混合目录：整源标 free 会把付费模型误标为免费（审计 §9.2 缺口），默认标为付费。
  // 混合目录（freeTier/hasFree/other）整源标 free 会把付费模型误标为免费（审计 §9.2 缺口），
  // 仅免密钥/本地整源免费；逐模型层级交给 §9.3 分级预览判定。
  const wholeFree = p.kind === 'free' || p.kind === 'local';
  _('provFree').value = wholeFree ? 'true' : 'false';
  // 带种子模型或目录端点的预设：新建保存后自动开分级预览（勾选后才写入，绝不静默导入）
  _previewAfterSave = !p.loginFlow && (!!p.modelIDs || !!p.modelsFetcher || wholeFree);
  if (p.loginFlow) {
    prepareProviderLogin(p);
  } else if (p.kind === 'free' || p.kind === 'local') {
    // 免密钥/本地源：直接走上游探测，弹出模型多选（免费/付费分组）
    toast(t('免密钥预设：正在自动拉取模型…'), 'info');
    fetchProviderModels(_('provFetchModelsBtn'));
  } else {
    toast(t('已填充预设，请补 API Key 和模型 ID'), 'info');
  }
}

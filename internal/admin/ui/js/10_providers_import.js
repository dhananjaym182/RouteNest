// ========== §9.3 受控导入：preview → 勾选 → apply ==========
let _importBatch = null;
let _importActionFilter = '';
// 计费筛选：''|'free'|'paid' — 与操作筛选(AND)与搜索(AND)三者取交集
let _importTierFilter = '';

// 预览批次短缓存：选择模型弹窗（fetchProviderModels）与导入预览共用同一批次，
// 避免两次拉取（池状态变化 / 上游抖动）给出不一致的免费/付费数。
// key = id:<providerId>（已有 Provider）或 draft:<baseURL>|free:<0|1>（未保存草稿），TTL 60s；
// 保存 Provider / 切换编辑对象 / 导入落盘后主动失效。
const IMPORT_PREVIEW_TTL_MS = 60000;
const _importPreviewCache = {}; // key -> { batch, ts }

function importPreviewCacheKey(payload) {
  if (payload && payload.id) return 'id:' + payload.id;
  const url = String((payload && payload.baseURL) || '').trim().replace(/\/+$/, '').toLowerCase();
  return 'draft:' + url + '|free:' + (payload && payload.free === true ? '1' : '0');
}
function importPreviewCacheGet(key) {
  const hit = _importPreviewCache[key];
  if (!hit) return null;
  if (Date.now() - hit.ts > IMPORT_PREVIEW_TTL_MS) { delete _importPreviewCache[key]; return null; }
  return hit.batch || null;
}
function importPreviewCacheSet(key, batch) {
  if (key && batch) _importPreviewCache[key] = { batch: batch, ts: Date.now() };
}
function invalidateImportPreviewCache(key) {
  if (key) { delete _importPreviewCache[key]; return; }
  Object.keys(_importPreviewCache).forEach(k => { delete _importPreviewCache[k]; });
}

// 本地兜底统计：仅当服务端 batch.stats 缺失/不完整时用来补齐，字段口径与 Go 侧 BatchStats 一致。
function computeImportStats(items) {
  const s = {
    total: 0, free: 0, paid: 0,
    add: 0, addFree: 0, addPaid: 0,
    tierChange: 0, tierChangeFree: 0, tierChangePaid: 0,
    noChange: 0, protected: 0,
    selectable: 0, selectableFree: 0, selectablePaid: 0,
  };
  (items || []).forEach(it => {
    if (!it) return;
    const free = it.tier === 'free';
    s.total++;
    if (free) s.free++; else s.paid++;
    switch (it.action) {
      case 'add':
        s.add++; if (free) s.addFree++; else s.addPaid++;
        break;
      case 'tier_change':
        s.tierChange++; if (free) s.tierChangeFree++; else s.tierChangePaid++;
        break;
      case 'nochange': s.noChange++; break;
      case 'protected': s.protected++; break;
    }
  });
  s.selectable = s.add + s.tierChange;
  s.selectableFree = s.addFree + s.tierChangeFree;
  s.selectablePaid = s.addPaid + s.tierChangePaid;
  return s;
}

// 服务端 stats 优先；缺失字段用本地同口径值补齐（不回退整个对象，避免部分字段缺失时数字跳变）。
function importStatsFromBatch(batch) {
  const items = (batch && batch.items) || [];
  const local = computeImportStats(items);
  const src = batch && batch.stats;
  if (!src || typeof src !== 'object') return local;
  const num = (v, d) => (v === null || v === undefined || v === '' || !Number.isFinite(Number(v)) ? d : Number(v));
  return {
    total: num(src.total, local.total),
    free: num(src.free, local.free),
    paid: num(src.paid, local.paid),
    add: num(src.add, local.add),
    addFree: num(src.addFree, local.addFree),
    addPaid: num(src.addPaid, local.addPaid),
    tierChange: num(src.tierChange, local.tierChange),
    tierChangeFree: num(src.tierChangeFree, local.tierChangeFree),
    tierChangePaid: num(src.tierChangePaid, local.tierChangePaid),
    noChange: num(src.noChange, local.noChange),
    protected: num(src.protected, local.protected),
    selectable: num(src.selectable, local.selectable),
    selectableFree: num(src.selectableFree, local.selectableFree),
    selectablePaid: num(src.selectablePaid, local.selectablePaid),
  };
}

// Per-signal explanation shown as the row tooltip, so the tier basis is explicit
// instead of just a badge ("free"/"paid") with no justification.
const _tierSignalWhy = {
  'pricing-zero': '上游返回 prompt/completion 价格为 0',
  'flag-free': '上游返回 free 标志或 mode=free',
  'suffix-free': '模型 id 以 :free 或 -free 结尾',
  'name-token-free': '模型 id 含独立的 free 词段',
  'display-name-free': '模型显示名含 free 词段',
  'allowlist': '免费标签或内置白名单命中',
  'provider-free': '整个 Provider 标记为免费',
  'pricing-paid': '上游定价非零 → 付费',
  'paid-default': '没有任何免费信号，保守归为付费',
};
function tierBasisTitle(it) {
  const why = _tierSignalWhy[it.signal];
  const basis = why ? t(why) : (it.reason ? tx(t(it.reason)) : '');
  return basis;
}
function tierBadge(tier) {
  return tier === 'free'
    ? '<span class="model-source-badge custom">' + t('免费') + '</span>'
    : '<span class="model-source-badge provider">' + t('付费') + '</span>';
}
// 批次摘要条：全部/免费/付费 + 新增/层级变化/无变化/受保护，全部数字取服务端 batch.stats。
// 免费/付费明确标注「全部」（不是只统计新增），新增/层级变化的括号内是该组免费数。
function importSummaryHTML(st) {
  const stat = (label, n, cls, title, sub) =>
    '<span class="imp-stat' + (cls ? ' ' + cls : '') + '" role="listitem"' +
    (title ? ' title="' + escAttr(title) + '"' : '') + '>' +
    label + '<b>' + n + '</b>' + (sub ? '<span class="imp-sub">' + sub + '</span>' : '') + '</span>';
  const subFree = n => '<span class="imp-sub">' + t('· 免费 ') + n + '</span>';
  return '<div class="imp-stats" role="list" aria-label="' + t('批次统计') + '">' +
    stat(t('全部'), st.total, '', t('批次内全部条目')) +
    '<span class="imp-sep" aria-hidden="true"></span>' +
    '<span class="imp-stat imp-free" role="listitem" title="' + escAttr(t('全部条目中免费的数量（不是仅新增）')) + '">' +
      '<span class="imp-dot" aria-hidden="true"></span>' + t('免费') + '<b>' + st.free + '</b><span class="imp-sub">' + t('（全部）') + '</span></span>' +
    '<span class="imp-stat imp-pass" role="listitem" title="' + escAttr(t('全部条目中付费的数量（不是仅新增）')) + '">' +
      '<span class="imp-dot" aria-hidden="true"></span>' + t('付费') + '<b>' + st.paid + '</b><span class="imp-sub">' + t('（全部）') + '</span></span>' +
    '<span class="imp-sep" aria-hidden="true"></span>' +
    stat(t('新增'), st.add, 'imp-add', t('可导入的新模型'), subFree(st.addFree)) +
    stat(t('层级变化'), st.tierChange, '', t('可导入的层级变化'), subFree(st.tierChangeFree)) +
    stat(t('无变化'), st.noChange, '', t('已在池内且层级一致')) +
    stat(t('受保护'), st.protected, '', t('池内已存在且受保护，导入不会改动')) +
  '</div>';
}

// 分组表头计数：add/tier_change 直接取 stats 的组内 total/免费/付费；
// nochange/protected 的 total 取 stats，免费/付费拆分按本组行计算（仅用于组内展示）。
function importGroupStats(action, st, rows) {
  if (action === 'add') return { total: st.add, free: st.addFree, paid: st.addPaid };
  if (action === 'tier_change') return { total: st.tierChange, free: st.tierChangeFree, paid: st.tierChangePaid };
  const total = action === 'nochange' ? st.noChange : st.protected;
  const free = (rows || []).filter(x => x && x.tier === 'free').length;
  return { total: total, free: free, paid: Math.max(0, total - free) };
}

async function previewModelImport(btn) {
  if (btn && btn.disabled) return;
  const providerID = _editingProviderId, revision = _provEditorRevision;
  const removalEpoch = providerModelRemovalEpoch(providerID);
  const current = () => _editingProviderId === providerID && _provEditorRevision === revision && providerModelRemovalEpoch(providerID) === removalEpoch && !!_('providerEditor') && _('providerEditor').classList.contains('open');
  const body = _editingProviderId
    ? { id: _editingProviderId }
    : { baseURL: _('provBaseURL').value.trim(), apiKey: _('provKey').value.trim(),
        headers: parseHeaderText(_('provHeaders').value),
        free: _('provFree').value === 'true', name: _('provName').value.trim() };
  if (!_editingProviderId && !body.baseURL) { toast(t('请先填写 Base URL'), 'info'); return; }
  const key = importPreviewCacheKey(body);
  const cached = importPreviewCacheGet(key);
  if (cached) {
    _importBatch = cached;
    renderImportPreviewList();
    _('importPreviewModal').style.display = 'flex';
    return;
  }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('扫描中...'); }
  try {
    const d = await api('POST', '/models/import/preview', body);
    if (!current()) return;
    const r = d.data || {};
    if (r.error) { toast(t('上游拉取失败') + ': ' + r.error, 'error'); return; }
    _importBatch = r.batch;
    importPreviewCacheSet(key, _importBatch);
    renderImportPreviewList();
    _('importPreviewModal').style.display = 'flex';
  } catch (e) {
    if (current()) toast(t('加载失败') + ': ' + esc(e.message), 'error');
  } finally {
    if (btn && _provEditorRevision === revision) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

function closeImportPreview() {
  _('importPreviewModal').style.display = 'none';
  _importActionFilter = '';
  _importTierFilter = '';
  const s = _('importPreviewSearch');
  if (s) s.value = '';
  document.querySelectorAll('.import-act-filter').forEach(b => b.classList.toggle('active', !b.dataset.act));
  document.querySelectorAll('.import-tier-filter').forEach(b => b.classList.toggle('active', !b.dataset.tier));
}

let _importTests = {}; // modelId → 行内测试结果（批次切换时清空）
let _importTestsFor = null;
// 导入前试用：provider 已保存（批次属于某个 provider ID），所以按 ID 测试，名称原样发送。
async function testImportModel(btn) {
  const id = btn.dataset.model;
  const batch = _importBatch;
  if (!batch || !batch.providerId) return;
  _importTests[id] = { running: true };
  paintModelTestResult('#importPreviewList .import-test-result', id, _importTests[id]);
  btn.disabled = true;
  const result = await provTestModel({ id: batch.providerId }, id, '');
  btn.disabled = false;
  if (_importBatch !== batch) return;
  _importTests[id] = result;
  paintModelTestResult('#importPreviewList .import-test-result', id, result);
}
function renderImportPreviewList() {
  if (_importTestsFor !== _importBatch) { _importTests = {}; _importTestsFor = _importBatch; }
  const items = (_importBatch && _importBatch.items) || [];
  const st = importStatsFromBatch(_importBatch);
  _('importPreviewProv').textContent = _importBatch ? _importBatch.providerName : '';
  _('importPreviewSummary').innerHTML = items.length
    ? importSummaryHTML(st)
    : '<div class="empty">' + t('没有可导入的模型') + '</div>';
  _importActionFilter = '';
  _importTierFilter = '';
  document.querySelectorAll('.import-act-filter').forEach(b => b.classList.toggle('active', !b.dataset.act));
  document.querySelectorAll('.import-tier-filter').forEach(b => b.classList.toggle('active', !b.dataset.tier));
  // 计费 chip 数字：批次 stats 的全量免费/付费（不是当前筛选结果）
  const tcF = _('tierCntFree'), tcP = _('tierCntPaid');
  if (tcF) tcF.textContent = st.free;
  if (tcP) tcP.textContent = st.paid;
  const chipF = document.querySelector('.import-tier-filter[data-tier="free"]');
  const chipP = document.querySelector('.import-tier-filter[data-tier="paid"]');
  if (chipF) chipF.title = t('免费') + ': ' + st.free + ' / ' + st.total + ' ' + t('全部条目');
  if (chipP) chipP.title = t('付费') + ': ' + st.paid + ' / ' + st.total + ' ' + t('全部条目');
  if (!items.length) {
    _('importPreviewList').innerHTML = '<div class="empty">' + t('没有可导入的模型') + '</div>';
    _('importPreviewCount').textContent = '';
    updateImportSelectFreeLabel();
    return;
  }
  // Fresh batch: clear any filter from the previous preview.
  const searchEl = _('importPreviewSearch');
  if (searchEl) searchEl.value = '';
  const matchEl = _('importPreviewMatch');
  if (matchEl) matchEl.textContent = '';
  const groups = [
    { action: 'add', label: t('新增') },
    { action: 'tier_change', label: t('层级变化') },
    { action: 'nochange', label: t('无变化') },
    { action: 'protected', label: t('受保护') },
  ];
  _('importPreviewList').innerHTML = groups.map(g => {
    const rows = items.filter(it => it.action === g.action);
    if (!rows.length) return '';
    rows.sort((a, b) => String(a.modelId).localeCompare(String(b.modelId)));
    const gs = importGroupStats(g.action, st, rows);
    const head = '<div class="import-group-head" title="' + escAttr(t('本组计数，与批次统计同口径')) + '">' + g.label +
      ' <span class="import-group-count">' + gs.total + '</span>' +
      ' <span class="import-group-tiers">' + t('组内免费') + ' ' + gs.free + ' · ' + t('付费') + ' ' + gs.paid + '</span></div>';
    const body = rows.map(it => {
      const selectable = it.action === 'add' || it.action === 'tier_change';
      const why = tierBasisTitle(it);
      const sig = it.signal || it.reason
        ? '<span class="import-basis" title="' + escAttr(why) + '">' + t('依据: ') + esc(tx(t(_tierSignals[it.signal] || it.reason || it.signal))) + '</span>'
        : '';
      let action;
      if (it.action === 'add') action = '<span class="model-source-badge zen">' + t('新增') + '</span>';
      else if (it.action === 'tier_change') action = '<span class="model-source-badge provider">' + t('层级变化') + ': ' + esc(it.oldTier === 'free' ? t('免费') : t('付费')) + ' → ' + esc(it.tier === 'free' ? t('免费') : t('付费')) + '</span>';
      else if (it.action === 'protected') action = '<span class="model-source-badge" title="' + escAttr(tx(it.reason || '')) + '">' + t('受保护') + '</span>';
      else action = '<span class="model-source-badge dim-op">' + t('无变化') + '</span>';
      return '<label class="combo-pick-row prov-flow-row" data-action="' + escAttr(it.action) + '" data-tier="' + (it.tier === 'free' ? 'free' : 'paid') + '">' +
        '<input type="checkbox" class="import-approve" onchange="updateImportCount()" value="' + esc(it.modelId) + '" ' + (it.action === 'add' ? 'checked' : '') + (selectable ? '' : ' disabled') + '>' +
        '<span class="combo-pick-id">' + esc(it.modelId) + '</span>' + tierBadge(it.tier) + sig + action +
        (selectable ? '<button type="button" class="btn btn-sm import-test" data-model="' + escAttr(it.modelId) + '" onclick="event.preventDefault();event.stopPropagation();testImportModel(this)" title="' + escAttr(t('测试此模型')) + '">' + t('测试') + '</button>' +
          '<span class="import-test-result" data-model="' + escAttr(it.modelId) + '">' + modelTestResultHTML(_importTests[it.modelId]) + '</span>' : '') + '</label>';
    }).join('');
    return '<div class="import-group" data-action="' + g.action + '">' + head + body + '</div>';
  }).join('');
  updateImportCount();
}

function importFilterActive() {
  const q = ((_('importPreviewSearch') || {}).value || '').trim();
  return !!(q || _importActionFilter || _importTierFilter);
}

// 当前可见且可勾选的免费行数（仅用于筛选态下按钮标签，避免按钮承诺一个当前看不到的数字）
function visibleSelectableFreeCount() {
  let n = 0;
  document.querySelectorAll('#importPreviewList .import-approve').forEach(cb => {
    if (cb.disabled) return;
    const row = cb.closest('.combo-pick-row');
    if (row && row.style.display === 'none') return;
    if (row && row.dataset.tier !== 'free') return;
    n++;
  });
  return n;
}

// 「只选免费」标签：无筛选 → stats.selectableFree（全部可导入免费数）；
// 有筛选 → 当前可见可勾选免费数（操作仍是只作用于可见行，标签必须同步）。
function updateImportSelectFreeLabel() {
  const btn = _('importSelectFreeBtn');
  if (!btn) return;
  const st = importStatsFromBatch(_importBatch);
  const filtered = importFilterActive();
  const n = filtered ? visibleSelectableFreeCount() : st.selectableFree;
  btn.textContent = filtered
    ? t('只选免费') + '·' + t('当前') + ' ' + n
    : t('只选免费') + ' (' + n + ')';
}

function updateImportCount() {
  const n = document.querySelectorAll('#importPreviewList .import-approve:checked').length;
  const st = importStatsFromBatch(_importBatch);
  // stats 缺失时用 DOM 里实际可勾选行数兜底；stats.selectable = add + tier_change
  const selectable = st.total ? st.selectable : document.querySelectorAll('#importPreviewList .import-approve:not([disabled])').length;
  _('importPreviewCount').textContent = n + t('个已选') + ' / ' + selectable + t('个可导入');
  updateImportSelectFreeLabel();
}

// Hide/show preview rows by id substring and/or action group and/or tier —
// 三重交集（action-chip AND tier-chip AND search），checkbox 状态始终留在 DOM。
function filterImportPreview() {
  const q = ((_('importPreviewSearch') || {}).value || '').trim().toLowerCase();
  const act = _importActionFilter || '';
  const tier = _importTierFilter || '';
  let shown = 0;
  document.querySelectorAll('#importPreviewList .import-group').forEach(group => {
    let groupShown = 0;
    group.querySelectorAll('.combo-pick-row').forEach(row => {
      const idEl = row.querySelector('.combo-pick-id');
      const hit = (!act || group.dataset.action === act) &&
        (!tier || row.dataset.tier === tier) &&
        (!q || (idEl && idEl.textContent.toLowerCase().indexOf(q) !== -1));
      row.style.display = hit ? 'flex' : 'none';
      if (hit) groupShown++;
    });
    group.style.display = groupShown ? '' : 'none';
    shown += groupShown;
  });
  const st = importStatsFromBatch(_importBatch);
  const total = st.total;
  const matchEl = _('importPreviewMatch');
  if (matchEl) matchEl.textContent = (q || act || tier) ? shown + ' / ' + total : '';
  updateImportSelectFreeLabel();
}

// Action-group filter (all / add / tier_change / nochange / protected).
function setImportActionFilter(btn) {
  _importActionFilter = (btn && btn.dataset.act) || '';
  document.querySelectorAll('.import-act-filter').forEach(b => b.classList.toggle('active', b === btn));
  filterImportPreview();
}

// 计费筛选 chip（免费 / 付费）：与操作 chip、搜索取交集；再点当前 chip 不取消（用「全部」清空）。
function setImportTierFilter(btn) {
  _importTierFilter = (btn && btn.dataset.tier) || '';
  document.querySelectorAll('.import-tier-filter').forEach(b => b.classList.toggle('active', b === btn));
  filterImportPreview();
}

// 只选免费：把当前可见且可勾选的行按计费置位（免费勾上、付费取消），隐藏行的选择状态不动。
function importPreviewSelectTier() {
  document.querySelectorAll('#importPreviewList .import-approve').forEach(cb => {
    if (cb.disabled) return;
    const row = cb.closest('.combo-pick-row');
    if (row && row.style.display === 'none') return;
    cb.checked = row && row.dataset.tier === 'free';
  });
  updateImportCount();
}

// Toggle every enabled + currently visible approval checkbox (select all / clear all).
function importPreviewSelectAll(on) {
  document.querySelectorAll('#importPreviewList .import-approve').forEach(cb => {
    if (cb.disabled) return;
    const row = cb.closest('.combo-pick-row');
    if (row && row.style.display === 'none') return;
    cb.checked = !!on;
  });
  updateImportCount();
}

async function importPreviewApply(btn) {
  if (!_importBatch) return;
  const approve = Array.from(document.querySelectorAll('#importPreviewList .import-approve:checked')).map(el => el.value);
  if (!approve.length) { toast(t('请先勾选要导入的模型'), 'info'); return; }
  // 草稿批次（未保存 Provider）：服务端无 provider 可写 → 所选并入表单模型列表并触发保存
  if (!_importBatch.providerId) {
    const input = _('provModels');
    const seen = {};
    const merged = input.value.split(',').map(s => s.trim()).filter(Boolean)
      .concat(approve)
      .filter(id => !seen[id] && (seen[id] = true));
    input.value = merged.join(', ');
    renderProvModelChips();
    closeImportPreview();
    _importBatch = null;
    invalidateImportPreviewCache();
    toast(t('所选模型已并入，正在保存 Provider...'), 'info');
    await saveProvider(null);
    return;
  }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('导入中...'); }
  try {
    const d = await api('POST', '/models/import/apply', { batchId: _importBatch.id, approve: approve });
    const r = d.data || {};
    toast(t('已导入 ') + (r.added || 0) + t(' 个新模型，调整 ') + (r.changed || 0) + t(' 个层级'), 'success');
    closeImportPreview();
    _importBatch = null;
    invalidateImportPreviewCache();
    loadProviders();
    loadModels();
  } catch (e) {
    toast(t('导入失败: ') + esc(e.message), 'error');
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

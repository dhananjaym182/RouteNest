// ========== §供应商详情（master–detail）：列表紧凑，全量数据进只读抽屉 ==========
// Free-only 处理方式的详情徽标。默认「自动识别」用灰字；两个会放行付费模型的选项用徽标 + 说明，
// 这样在列表详情里一眼能看出哪个 Provider 不再被「仅免费模型」过滤。
function provAccessPolicyBadge(policy) {
  if (policy === 'all_free') {
    return '<span class="model-source-badge custom" title="' + escAttr(t(PROV_ACCESS_POLICY_HINTS.all_free)) + '">' + t('全部模型视为免费') + '</span>';
  }
  if (policy === 'allow_all') {
    return '<span class="model-source-badge custom" title="' + escAttr(t(PROV_ACCESS_POLICY_HINTS.allow_all)) + '">' + t('免费与付费混合：全部放行') + '</span>';
  }
  return '<span class="mut" title="' + escAttr(t(PROV_ACCESS_POLICY_HINTS.detect)) + '">' + t('自动识别（默认）') + '</span>';
}

function openProvDetail(id) {
  const p = _provCache[id];
  if (!p) { toast(t('加载失败'), 'error'); return; }
  _provDetailOpenId = id;
  renderProvDetail(p);
  ensureProvDelisted(); // 异步取该 provider 的已下架模型清单，到达后自动重绘详情
  if (!_connList.length) loadConnections();
  openDrawer('provDetail');
}

function closeProvDetail() { _provDetailOpenId = ''; closeDrawer('provDetail'); }

function provGlobalViewButton(tab, providerID, label) {
  return '<button type="button" class="btn btn-sm" onclick="closeProvDetail();openGlobalProviderView(' +
    escAttr(JSON.stringify(tab)) + ',' + escAttr(JSON.stringify(providerID)) + ')">' + t(label) + ' →</button>';
}

//组成员 loadConnections / loadProviders 后若详情开着 → 就地刷新内容
function refreshProvDetailIfOpen() {
  if (!_provDetailOpenId) return;
  const p = _provCache[_provDetailOpenId];
  if (p) renderProvDetail(p); else closeProvDetail();
}

function provDetailSectionsHTML(p) {
  const h = _provHealth[p.id] || {};
  const lim = h.limits || {};
  const st = _provSync[p.id] || {};
  const ids = p.connectionIds && p.connectionIds.length
    ? p.connectionIds
    : (p.connectionId ? [p.connectionId] : []);
  const conns = _connList.filter(c => ids.includes(c.id));

  // §1 概览：键值网格（不入表格，避免 fixed 布局挤压）
  const kv = (k, v) => '<div class="detail-k">' + esc(k) + '</div><div class="detail-v">' + v + '</div>';
  const overview =
    kv(t('状态'), p.enabled
      ? '<span class="status active"><span class="status-dot active"></span>' + t('已启用') + '</span>'
      : '<span class="status disabled"><span class="status-dot disabled"></span>' + t('已停用') + '</span>') +
    kv('Base URL', '<span class="mono detail-clip">' + esc(p.baseURL) + '</span>') +
    kv('API Key', p.apiKey ? t('已设置') : '<span class="mut">' + t('未设置') + '</span>') +
    kv(t('免费来源（整源）'), p.free
      ? '<span class="model-source-badge custom" title="' + escAttr(t('整源免费标记，不代表池内每个模型都免费；逐模型计费以分级预览为准')) + '">' + t('整源免费') + '</span>'
      : '<span class="model-source-badge provider" title="' + escAttr(t('整源按付费标记；池内个别模型仍可能被判定为免费')) + '">' + t('整源付费') + '</span>') +
    kv(t('Free-only 模式处理'), provAccessPolicyBadge(p.accessPolicy)) +
    kv(t('优先级'), esc(String(p.priority))) +
    kv(t('权重'), esc(String(p.weight))) +
    kv(t('代理'), p.proxyUrl ? '<span class="mono detail-clip">' + esc(p.proxyUrl) + '</span>' : '<span class="mut">' + t('未设置') + '</span>') +
    kv(t('创建时间'), p.createdAt ? esc(new Date(p.createdAt).toLocaleString(LC())) : '<span class="mut">—</span>');

  // §2 模型：全量芯片 + 待审计数；已下架模型就地标记并提供一键移除
  const totalModes = (p.modelIds || []).length;
  const delistedSet = {};
  ((_provDelisted && _provDelisted[p.id]) || []).forEach(r => { delistedSet[r.id] = true; });
  const models = '<div class="model-chips model-chips-all">' +
      (p.modelIds || []).map(m => delistedSet[m]
        ? '<span class="model-tag chip-clip delisted-chip" title="' + escAttr(t('已从上游官方列表移除，实测可能仍可用；同步只标记不删除，上游报模型不存在时自动清理')) + '">' +
            esc(m) + ' <span class="delisted-badge">' + t('已下架') + '</span>' +
            '<a href="javascript:void(0)" class="delist-inline-x" onclick="removeProvDelistedModel(\'' + escAttrJS(p.id) + '\',\'' + escAttrJS(m) + '\')" title="' + escAttr(t('移除')) + '" aria-label="' + escAttr(t('移除') + ' ' + m) + '">' + uiIcon('close') + '</a>' +
          '</span>'
        : '<span class="model-tag chip-clip" title="' + escAttr(m) + '">' + esc(m) + '</span>').join('') +
    '</div>' +
    '<div class="detail-meta">' +
      '<span class="hint-xs">' + totalModes + t(' 个模型') + '</span>' +
      (st.newQueued && st.batchId
        ? ' <a href="javascript:void(0)" class="pending-pill pending-open" onclick="openAutoBatch(\'' + esc(st.batchId) + '\')"><span class="pp-num mono">' + st.newQueued + '</span>' + t('待同步模型') + '</a>'
        : '') +
      (st.delistedMarked
        ? ' <span class="pending-pill"><span class="pp-num mono">' + st.delistedMarked + '</span>' + t('已下架') + ' ' + uiIcon('arrowDown') + '</span>'
        : '') +
    '</div>';

  // §3 限额与健康：完整计量表 + 热度 + 最近检测/错误全文 + last result
  const errBlock = st.lastError
    ? '<div class="detail-err" title="' + esc(st.lastError) + '">' + esc(st.lastError) + '</div>' : '';
  const health =
    (provQuotaHtml(p) || '<div class="hint-sm">—</div>') +
    '<div class="detail-meta mt-6"><span class="hint-xs">' + t('近期成败') + '</span> ' + provHeatCellHtml(p) + '</div>' +
    '<div class="detail-meta mt-6"><span class="hint-xs">' + t('最近检测') + '</span> ' +
      (st.lastCheckedMs
        ? '<span class="hint-sm">' + esc(new Date(st.lastCheckedMs).toLocaleString(LC())) + ' · ' + fmtAgo(st.lastCheckedMs) + '</span>'
        : '<span class="hint-sm">' + t('未检测') + '</span>') +
    '</div>' + errBlock +
    '<div class="detail-meta mt-6"><span class="hint-xs">' + t('重试') + '</span> ' + provRowResultHTML('test', p.id) + ' ' + provRowResultHTML('recheck', p.id) + '</div>';

  // §4 凭据不在 Provider 详情内复制；跳转到带 Provider 筛选的全局清单。
  const connList = '<div class="detail-linked-summary"><span>' +
    (conns.length ? (conns.length + ' ' + t('个关联凭据')) : t('未链接任何连接')) +
    '</span>' + provGlobalViewButton('credentials', p.id, '查看凭据') + '</div>';

  // §5 同步：批次入口 + 立即检测
  const sync =
    '<div class="detail-meta">' +
      '<span class="hint-xs">' + t('最近检测') + '</span> ' + (st.lastCheckedMs ? '<span class="hint-sm">' + fmtAgo(st.lastCheckedMs) + '</span>' : '<span class="hint-sm">' + t('未检测') + '</span>') +
      (st.newQueued ? ' <span class="pending-pill"><span class="pp-num mono">' + st.newQueued + '</span>' + t('待同步模型') + '</span>' : '') +
      (st.delistedMarked ? ' <span class="pending-pill"><span class="pp-num mono">' + st.delistedMarked + '</span>' + t('已下架') + '</span>' : '') +
    '</div>' +
    '<div class="form-actions mt-10">' +
      (st.newQueued && st.batchId
        ? '<button class="btn btn-sm btn-primary" onclick="openAutoBatch(\'' + esc(st.batchId) + '\')">' + t('打开审阅') + '</button>'
        : (st.newQueued ? '<button class="btn btn-sm" onclick="openNewestAutoBatch()">' + t('打开审阅') + '</button>' : '')) +
      '<button class="btn btn-sm" onclick="provDetailRecheck(this)">' + t('立即检测') + '</button>' +
    '</div>';

  return { overview, models, health, connList, sync, hasConns: conns.length };
}

function renderProvDetail(p) {
  const title = _('provDetailTitle');
  if (title) title.textContent = p.name || p.id;
  const sec = provDetailSectionsHTML(p);
  const body = _('provDetailBody');
  if (!body) return;
  body.innerHTML =
    '<div class="detail-back"><a href="javascript:void(0)" class="lnk-a" onclick="closeProvDetail()">‹ ' + t('返回列表') + '</a></div>' +
    '<div class="detail-view-links" aria-label="' + t('关联视图') + '">' +
      provGlobalViewButton('credentials', p.id, '凭据') +
      provGlobalViewButton('models', p.id, '模型') +
      provGlobalViewButton('health', p.id, '使用与健康') +
      provGlobalViewButton('network', p.id, '网络') +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('概览') + '</div>' +
      '<div class="detail-actions">' +
        '<button class="btn btn-sm btn-primary" onclick="showProviderEditor(_provCache[\'' + escAttrJS(p.id) + '\'])">' + t('编辑') + '</button>' +
        '<button class="btn btn-sm" onclick="provDetailTest(this)">' + t('测试') + '</button>' +
        '<button class="btn btn-sm" onclick="toggleProvider(\'' + p.id + '\', ' + !p.enabled + ', this)">' + t(p.enabled ? '停用' : '启用') + '</button>' +
        '<button class="btn btn-sm btn-danger" onclick=\'deleteProvider("' + p.id + '")\'>' + t('删除') + '</button>' +
        '<span id="provDetailTestOut"></span>' +
      '</div>' +
      '<div class="detail-kv">' + sec.overview + '</div>' +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('模型') + ' <span class="grp-count">' + ((p.modelIds || []).length || '') + '</span> <button type="button" class="text-action" onclick="closeProvDetail();openGlobalProviderView(' + escAttr(JSON.stringify('models')) + ',' + escAttr(JSON.stringify(p.id)) + ')">' + t('查看全局模型') + ' →</button></div>' + sec.models + provDelistedHTML(p) +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('限额与健康') + ' <button type="button" class="text-action" onclick="closeProvDetail();openGlobalProviderView(' + escAttr(JSON.stringify('health')) + ',' + escAttr(JSON.stringify(p.id)) + ')">' + t('查看运行状态') + ' →</button></div>' + sec.health +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('关联凭据') + ' <span class="grp-count">' + (sec.hasConns ? sec.hasConns : '') + '</span></div>' + sec.connList +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('同步') + '</div>' + sec.sync +
      '<div id="provDetailRecheckOut" class="mt-6"></div>' +
    '</div>';
}

// escAttrJS: id 用于 JS 字符串字面量拼进 onclick —— 复用 escAttr 的转义即可（id 无引号风险）
function escAttrJS(s) { return escAttr(s); }

// ========== §已下架模型清理（详情抽屉）：按稳定 provider ID 读取同步状态 =====
let _provDelisted = null; // providerId -> [{id, cost}]（每次打开详情时刷新一次）

async function ensureProvDelisted() {
  try {
    const d = await api('GET', '/providers/sync-status');
    _provSync = (d.data && d.data.providers) || {};
    _provDelisted = {};
    Object.keys(_provSync).forEach(providerId => {
      const state = _provSync[providerId] || {};
      if (!Array.isArray(state.delistedModels)) return;
      _provDelisted[providerId] = state.delistedModels.map(id => ({
        id: id,
        cost: state.offerings && state.offerings[id] && state.offerings[id].cost || '',
      }));
    });
  } catch (e) {
    _provDelisted = null;
  }
  // 到数据后重绘详情（下架子节只在确实有数据时出现，首帧不闪占位）
  refreshProvDetailIfOpen();
}

// 子行 + 移除全部入口（仅当该 provider 有已下架条目时渲染）
function provDelistedHTML(p) {
  if (!_provDelisted) return '';
  const rows = _provDelisted[p.id] || [];
  if (!rows.length) return '';
  const list =
    '<div class="delist-rows">' +
    rows.map(m =>
      '<div class="detail-conn-row delist-row">' +
        '<span class="mono detail-clip">' + esc(m.id) + '</span>' +
        '<span class="delisted-badge" title="' + escAttr(t('已从上游官方列表移除，实测可能仍可用；同步只标记不删除，上游报模型不存在时自动清理')) + '">' + t('已下架') + '</span>' +
        (m.cost === 'free' ? '<span class="model-tag free">' + t('免费') + '</span>' : '') +
        '<button type="button" class="btn btn-sm btn-xs btn-danger delist-remove" onclick="removeProvDelistedModel(\'' + escAttrJS(p.id) + '\',\'' + escAttrJS(m.id) + '\')" title="' + t('移除') + '" aria-label="' + t('移除') + ' ' + escAttr(m.id) + '">' + t('移除') + '</button>' +
      '</div>').join('') +
    '</div>' +
    '<div class="form-actions mt-6"><button type="button" class="btn btn-sm" onclick="removeAllProvDelisted(this)">' + t('移除全部已下架') + '</button></div>';
  return '<div class="delist-sub"><div class="form-group-label">' + t('已下架') + ' <span class="grp-count">' + rows.length + '</span>' +
    '<span class="hint-xs ml-8">' + t('已从上游官方列表移除，可在此手动清理') + '</span></div>' + list + '</div>';
}

async function requestProviderDelistedRemoval(providerId, rows) {
  const result = { removed: [], skipped: [], errors: [] };
  const targets = rows.map(row => ({ id: row.id, source: 'provider', providerId: providerId }));
  for (const batch of modelRemovalBatches(targets)) {
    try {
      const response = await api('POST', '/models/delisted/remove', { models: batch });
      const data = response.data || {};
      if (!Array.isArray(data.removed) || !Array.isArray(data.skipped) || !Array.isArray(data.errors)) throw new Error(t('移除结果无效，请刷新后重试'));
      result.removed.push(...data.removed); result.skipped.push(...data.skipped); result.errors.push(...data.errors);
    } catch (e) {
      result.errors.push({ error: e.message });
      break;
    }
  }
  return result;
}

async function refreshProviderDelistedRemoval(result) {
  const completed = result.removed.concat(result.skipped);
  await Promise.allSettled([loadModels(), loadProviders(), loadGlobal(false), loadCombos()]);
  await loadConfig({ preserveEdits: true });
  if (typeof reconcileRemovedModelDrafts === 'function') reconcileRemovedModelDrafts(completed, result.removed);
  _provDelisted = null;
  await ensureProvDelisted();
}

// 单个移除始终携带稳定 providerId；显示名重名或重命名不改变目标。
async function removeProvDelistedModel(providerId, id) {
  if (!(await showConfirm(t('确认删除模型 ') + id + ' ?'))) return;
  const result = await requestProviderDelistedRemoval(providerId, [{ id: id }]);
  if (result.removed.length) toast(t('模型已删除: ') + id, 'success');
  else if (result.skipped.length) toast(t('模型已恢复或已移除，无需清理'), 'info');
  if (result.errors.length) toast(t('删除失败: ') + result.errors.map(item => item.error).filter(Boolean).join('; '), 'error');
  await refreshProviderDelistedRemoval(result);
}

// 一键移除该 provider 全部已下架模型；批次仍限定为同一个稳定 provider ID。
async function removeAllProvDelisted(btn) {
  const id = _provDetailOpenId;
  const p = id ? _provCache[id] : null;
  const rows = (p && _provDelisted && _provDelisted[p.id]) || [];
  if (!rows.length) { toast(t('没有已下架模型'), 'info'); return; }
  if (!(await showConfirm(t('确定移除 ') + rows.length + t(' 个已下架模型？')))) return;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>'; }
  const result = await requestProviderDelistedRemoval(p.id, rows);
  const okN = result.removed.length + result.skipped.length;
  if (okN) toast(t('已移除 ') + okN + t(' 个已下架模型'), 'success');
  if (result.errors.length) toast(t('删除失败: ') + result.errors.map(item => item.error).filter(Boolean).join('; '), 'error');
  if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  await refreshProviderDelistedRemoval(result);
}

// 详情内「测试连通性」：定位 #provDetailTestOut，结果同样入 _provRowResults 跨刷新保留
async function provDetailTest(btn) {
  const id = _provDetailOpenId;
  if (!id) return;
  const out = _('provDetailTestOut');
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('测试中...'); }
  if (out) out.innerHTML = '';
  try {
    const d = await api('POST', '/providers/test', { id });
    const r = d.data || {};
    const text = r.ok ? (r.durationMs + 'ms') : (tx(r.error) || t('失败'));
    _provRowResults['test:' + id] = { ok: !!r.ok, text: text };
    if (out) out.innerHTML = renderTestResultHTML(r.ok, text);
  } catch (e) {
    _provRowResults['test:' + id] = { ok: false, text: friendlyTestError(e) };
    if (out) out.innerHTML = renderTestResultHTML(false, _provRowResults['test:' + id].text);
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

// 详情内「立即检测」：与列表 recheck 同接口，结果写入 #provDetailRecheckOut
async function provDetailRecheck(btn) {
  const id = _provDetailOpenId;
  if (!id) return;
  const out = _('provDetailRecheckOut');
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('检测中...'); }
  if (out) out.innerHTML = '';
  try {
    const d = await api('POST', '/providers/recheck', { id: id });
    const st = d.data || {};
    const bits = [];
    if (st.newQueued) bits.push('+' + st.newQueued + ' ' + t('待审'));
    if (st.delistedMarked) bits.push(st.delistedMarked + ' ' + t('已下架'));
    const ok = !(st.lastError && !st.lastCheckedMs);
    const text = ok ? (bits.length ? bits.join(' · ') : t('无变化')) : friendlyTestError(new Error(st.lastError));
    _provRowResults['recheck:' + id] = { ok: ok, text: text };
    if (out) out.innerHTML = renderTestResultHTML(ok, text);
    await loadProviders(); // 刷新列表状态列（同时经 refreshProvDetailIfOpen 重绘详情）
    await loadModels();
  } catch (e) {
    _provRowResults['recheck:' + id] = { ok: false, text: friendlyTestError(e) };
    if (out) out.innerHTML = renderTestResultHTML(false, _provRowResults['recheck:' + id].text);
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

// 冷却快照条：所有活跃冷却按最早恢复排序，支持逐项/整组手动重置
function renderProvCooldownBar(list) {
  const names = {};
  list.forEach(p => { names[p.id] = p.name; });
  const rows = [];
  list.forEach(p => {
    (( _provHealth[p.id] || {}).cooldowns || []).forEach(c => {
      rows.push({ id: p.id, name: p.name, model: c.model, until: c.until });
    });
  });
  if (!rows.length) return '';
  rows.sort((a, b) => new Date(a.until) - new Date(b.until));
  return '<div class="sync-banner on sync-banner-warn cooldown-banner"><div class="pcb-title">' + t('冷却中的 Provider · 按最早恢复排序') + '</div>' +
    rows.map(r =>
      '<div class="pcb-row">' +
        '<span class="pcb-name">' + esc(r.name) + '</span>' +
        '<span class="mono pcb-model">' + esc(r.model) + '</span>' +
        '<span class="pcb-left">' + fmtLeft(r.until) + '</span>' +
        '<button class="btn btn-sm" onclick="providersResetCooldown(\'' + escAttr(r.id) + '\', \'' + escAttr(r.model) + '\', this)">' + t('重置') + '</button>' +
      '</div>').join('') + '</div>';
}

async function providersResetCooldown(id, model, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>'; }
  try {
    const d = await api('POST', '/providers/cooldown-reset', { id: id, model: model || undefined });
    const n = (d.data && d.data.removed) || 0;
    toast(t('已清除 ') + n + t(' 条冷却'), 'success');
    loadProviders();
  } catch (e) { toast(t('重置失败: ') + e.message, 'error'); if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

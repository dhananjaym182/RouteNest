// ========== §W3-C 上游自动检测：列表状态列 + 待审横幅 ==========
let _provSync = {};
// 已加载 Provider 缓存（供应商详情抽屉直接复用，避免额外请求）
const _provCache = {};
let _provDetailOpenId = '';

function fmtAgo(ms) {
  if (!ms) return '';
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 60) return t('刚刚');
  if (s < 3600) return Math.floor(s / 60) + ' ' + t('分钟前');
  if (s < 86400) return Math.floor(s / 3600) + ' ' + t('小时前');
  return Math.floor(s / 86400) + ' ' + t('天前');
}

function provSyncCellHTML(id) {
  // 列表内单行：时间 ∕ 错误(仅图标,原因在详情) ∕ 待同步计数 pill —— 不再纵向堆叠
  const st = _provSync[id] || {};
  let html = st.lastCheckedMs
    ? '<span class="hint-xs" title="' + esc(new Date(st.lastCheckedMs).toLocaleString(LC())) + '">' + fmtAgo(st.lastCheckedMs) + '</span>'
    : '<span class="hint-xs">' + t('未检测') + '</span>';
  if (st.lastError) html += ' <span class="hint-red hint-red-ic" title="' + esc(st.lastError) + '">' + uiIcon('close') + '</span>';
  if (st.newQueued && st.batchId) html += ' <a href="javascript:void(0)" class="pending-pill pending-open" onclick="event.stopPropagation();openAutoBatch(\'' + esc(st.batchId) + '\')" title="' + escAttr(t('多少模型变更待同步')) + '"><span class="pp-num mono">' + st.newQueued + '</span>' + t('待同步模型') + '</a>';
  return html;
}

async function recheckProvider(id, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('检测中...'); }
  try {
    const d = await api('POST', '/providers/recheck', { id: id });
    const st = d.data || {};
    const host = btn ? btn.closest('.prov-card, tr, .drawer-group') : null;
    const out = host ? host.querySelector('.prov-row-recheck-out') : null;
    if (st.lastError && !st.lastCheckedMs) {
      _provRowResults['recheck:' + id] = { ok: false, text: friendlyTestError(new Error(st.lastError)) };
      if (out) out.innerHTML = renderTestResultHTML(false, _provRowResults['recheck:' + id].text);
    } else {
      const bits = [];
      if (st.newQueued) bits.push('+' + st.newQueued + ' ' + t('待审'));
      if (st.delistedMarked) bits.push(st.delistedMarked + ' ' + t('已下架'));
      _provRowResults['recheck:' + id] = { ok: true, text: bits.length ? bits.join(' · ') : t('无变化') };
      if (out) out.innerHTML = renderTestResultHTML(true, _provRowResults['recheck:' + id].text);
    }
    await loadProviders();
    await loadModels();
  } catch (e) {
    toast(t('检测失败') + ': ' + e.message, 'error');
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

function renderProviderSyncBanner(list) {
  const box = _('providerSyncBanner');
  if (!box) return;
  let delisted = 0, queued = 0;
  (list || []).forEach(p => {
    const st = _provSync[p.id] || {};
    delisted += st.delistedMarked || 0;
    if (st.newQueued && st.batchId) queued += st.newQueued;
  });
  if (!queued && !delisted) { box.classList.remove('on'); box.innerHTML = ''; return; }
  box.classList.add('on');
  box.innerHTML = '<span class="hint-mut">' +
    (queued ? '<span class="acc-strong">' + queued + t('个新模型待审') + '</span>' : '') +
    (queued && delisted ? ' · ' : '') +
    (delisted ? '<span class="mut">' + delisted + ' ' + t('已下架') + '</span>' : '') +
    '</span>' +
    '<span class="hint-xs ml-8">' + t('后台每 6 小时自动检测已启用 Provider 的上游模型清单') + '</span>' +
    '<span class="row-bulk">' +
    (queued ? '<button class="btn btn-sm btn-primary" onclick="openNewestAutoBatch()">' + t('查看审阅') + '</button>' +
             '<button class="btn btn-sm" onclick="discardAutoBatches(this)">' + t('全部忽略') + '</button>' : '') +
    '</span>';
}

async function openNewestAutoBatch() {
  try {
    const d = await api('GET', '/models/import/pending');
    const autos = ((d.data || {}).batches || []).filter(b => b.auto);
    if (!autos.length) { toast(t('没有可导入的模型'), 'info'); return; }
    openAutoBatch(autos[0].id);
  } catch (e) { toast(t('加载失败') + ': ' + esc(e.message), 'error'); }
}

async function openAutoBatch(batchId) {
  try {
    const d = await api('GET', '/models/import/batch?id=' + encodeURIComponent(batchId));
    const r = d.data || {};
    if (!r.batch) { toast(t('批次不存在或已过期'), 'info'); loadProviders(); return; }
    _importBatch = r.batch;
    renderImportPreviewList();
    _('importPreviewModal').style.display = 'flex';
  } catch (e) { toast(t('加载失败') + ': ' + esc(e.message), 'error'); }
}

async function discardAutoBatches(btn) {
  if (!(await showConfirm(t('确定忽略全部待审的新模型批次？')))) return;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; }
  try {
    const d = await api('GET', '/models/import/pending');
    const autos = ((d.data || {}).batches || []).filter(b => b.auto);
    for (const b of autos) { await api('POST', '/models/import/discard', { batchId: b.id }); }
    toast(t('已忽略全部待审批次'), 'success');
    loadProviders();
  } catch (e) { toast(t('忽略失败') + ': ' + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

// toggleProvider 快速切换 provider 启停（不打开编辑表单）：拉取最新配置，
// 翻转 enabled 后原样回存，其余字段不动。停用即时退出路由与模型同步。
async function toggleProvider(id, enable, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    const d = await api('GET', '/providers');
    const p = (d.data.providers || []).find(x => x.id === id);
    if (!p) { toast(t('Provider 未找到'), 'error'); return; }
    p.enabled = !!enable;
    p.modelIds = p.modelIds || [];
    await api('POST', '/providers/save', p);
    toast(t(enable ? '已启用' : '已停用') + ': ' + p.name, 'success');
    loadProviders();
    loadGlobal(false);
  } catch (e) { toast(t('保存失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

function showProviderEditor(p) {
  if (typeof _providerSetupAuthContext !== 'undefined') _providerSetupAuthContext = null;
  const authSetup = _('providerAuthSetup');
  if (authSetup) { authSetup.hidden = true; authSetup.innerHTML = ''; }
  _editingProviderId = p ? p.id : '';
  _previewAfterSave = false;
  // 切换编辑对象 = 新上下文：丢弃上一轮的预览批次缓存（池/源变化后不得复用旧统计）
  invalidateImportPreviewCache();
  const adv = _('provModelsAdv');
  if (adv) adv.style.display = 'none';
  const title = _('providerEditorTitle');
  if (title) title.textContent = p ? t('编辑 Provider') : t('添加 Provider');
  applyProviderForm(p);
  if (!_connList.length) loadConnections();
  const testOut = _('provTestResult');
  if (testOut) testOut.innerHTML = '';
  _provBaseData = p;
  rebaselineProvForm();
  // 关闭守卫：表单相对打开时基线有改动 → 先确认再丢弃（覆盖 Esc / 遮罩 / 取消按钮全部路径）
  setDrawerGuard('providerEditor', async () => !provFormDirty() || (await showConfirm(t('有未保存的修改，确定放弃？'))));
  setSaveBarCtx('provider', {
    host: () => { const d = _('providerEditor'); return !!d && d.classList.contains('open'); },
    dirty: provFormDirty,
    diff: provFormChanges,
    discard: discardProviderForm,
    save: () => saveProvider(null),
  });
  openDrawer('providerEditor');
}

// Token 配额窗口联动：数字框 label/tooltip 与配额提示行随「重置周期」（月度/每日）切换
function syncProvTokenWindowUI() {
  const win = _('provTokenWindow') && _('provTokenWindow').value === 'daily' ? 'daily' : 'monthly';
  const lab = _('provTokenLimitLabel');
  if (lab) {
    lab.textContent = t(win === 'daily' ? 'Token 配额/日' : 'Token 配额/月');
    lab.title = t(win === 'daily' ? '每日 Token 上限（如 10000000 = 10M），0=不限' : '月度 Token 上限（如 10000000 = 10M），0=不限');
  }
  const hint = _('provQuotaWinHint');
  if (hint) hint.textContent = t(win === 'daily' ? 'Token 配额按每日重置（每天 0 点 UTC），0=不限。' : 'Token 配额按月度重置（每月 1 日 UTC），0=不限。');
}

// 表单字段回填（showProviderEditor 与「放弃更改」共用）
function applyProviderForm(p) {
  _('provName').value = p ? p.name : '';
  _('provBaseURL').value = p ? p.baseURL : '';
  _('provKey').value = p ? (p.apiKey || '') : '';
  _('provPriority').value = p ? p.priority : 10;
  _('provTimeout').value = p ? (p.timeoutSec || 300) : 300;
  _('provRpmLimit').value = p ? (p.rpmLimit || 0) : 0;
  _('provDailyLimit').value = p ? (p.dailyRequestLimit || 0) : 0;
  _('provTokenLimit').value = p ? (p.tokenLimit || 0) : 0;
  // tokenWindow 后端缺省 monthly；仅接受显式 "daily"
  _('provTokenWindow').value = p && p.tokenWindow === 'daily' ? 'daily' : 'monthly';
  _('provProxyUrl').value = p ? (p.proxyUrl || '') : '';
  _('provWeight').value = p ? (p.weight || 0) : 0;
  _('provModels').value = p ? (p.modelIds || []).join(', ') : '';
  _('provHeaders').value = p ? Object.entries(p.headers || {}).map(([k, v]) => k + ': ' + v).join('\n') : '';
  _('provEnabled').value = p ? String(p.enabled !== false) : 'true';
  // 手动新建默认付费：整源标 free 会把付费模型误标为免费（审计 §9.2）；free 来源应由预设/导入按模型判定
  _('provFree').value = p ? String(!!p.free) : 'false';
  initConnPick(p ? (p.connectionIds && p.connectionIds.length ? p.connectionIds : (p.connectionId ? [p.connectionId] : [])) : []);
  syncProvTokenWindowUI();
  renderProvModelChips();
}

// 脏检查基线：稳定 ASCII 键（切语言不影响快照）；展示时再经 PROV_FIELD_LABELS 本地化
const PROV_FIELD_LABELS = {
  name: '名称', baseURL: 'Base URL', apiKey: 'API Key', priority: '优先级', timeoutSec: '超时(秒)',
  rpmLimit: '分钟请求上限', dailyLimit: '每日请求上限', tokenLimit: 'Token 配额/月', tokenWindow: '重置周期', proxyUrl: '出口代理', weight: '权重',
  models: '模型', headers: '自定义请求头', enabled: '启用', free: '免费来源', connections: '连接池',
};
function serializeProvForm() {
  return {
    name: _('provName').value.trim(),
    baseURL: _('provBaseURL').value.trim(),
    apiKey: _('provKey').value.trim(),
    priority: _('provPriority').value,
    timeoutSec: _('provTimeout').value,
    rpmLimit: _('provRpmLimit').value,
    dailyLimit: _('provDailyLimit').value,
    tokenLimit: _('provTokenLimit').value,
    tokenWindow: _('provTokenWindow').value === 'daily' ? 'daily' : 'monthly',
    proxyUrl: _('provProxyUrl').value.trim(),
    weight: _('provWeight').value,
    models: _('provModels').value.split(',').map(s => s.trim()).filter(Boolean).join(', '),
    headers: _('provHeaders').value.trim(),
    enabled: _('provEnabled').value,
    free: _('provFree').value,
    connections: (_provConnSel || []).slice().sort().join(','),
  };
}
let _provBaseData = null;
let _provBaseJson = '';
function rebaselineProvForm() { _provBaseJson = JSON.stringify(serializeProvForm()); }
function provFormDirty() { return JSON.stringify(serializeProvForm()) !== _provBaseJson; }
function provFormChanges() {
  // tokenLimit 的展示名随窗口变化（月→「Token 配额/月」，日→「Token 配额/日」）
  const winDaily = _('provTokenWindow') && _('provTokenWindow').value === 'daily';
  return diffFlat(JSON.parse(_provBaseJson || '{}'), serializeProvForm())
    .map(ch => ({
      label: t(ch.label === 'tokenLimit' && winDaily ? 'Token 配额/日' : (PROV_FIELD_LABELS[ch.label] || ch.label)),
      from: ch.from, to: ch.to,
    }));
}
function discardProviderForm() {
  applyProviderForm(_provBaseData);
  rebaselineProvForm();
  refreshSaveBar();
}

function hideProviderEditor() { closeDrawer('providerEditor'); }

// 模型 ID 输入实时回显为芯片（真值仍是输入框文本，保存逻辑不变）
function renderProvModelChips() {
  const box = _('provModelsChips');
  const ids = _('provModels').value.split(',').map(s => s.trim()).filter(Boolean);
  refreshSaveBar(); // 覆盖程序化改值的路径（移除芯片 / 勾选模型回填），input 路径由委托触发
  const cnt = _('provModelsCount');
  if (cnt) cnt.textContent = ' · ' + (ids.length ? ids.length + t(' 个模型') : t('未选择'));
  if (!box) return;
  if (!ids.length) { box.style.display = 'none'; box.innerHTML = ''; return; }
  box.style.display = '';
  box.innerHTML = ids.map(id => '<span class="model-tag mono">' + esc(id) +
    ' <a href="javascript:void(0)" onclick="removeProvModel(\'' + escAttr(id) + '\')" title="' + t('移除') + '" aria-label="' + t('移除') + '" class="mut">' + uiIcon('close') + '</a></span>').join('');
}

function removeProvModel(id) {
  const kept = _('provModels').value.split(',').map(s => s.trim()).filter(Boolean).filter(x => x !== id);
  _('provModels').value = kept.join(', ');
  renderProvModelChips();
}

function toggleProvModelsAdv() {
  const d = _('provModelsAdv');
  if (!d) return;
  const open = d.style.display !== 'none' && d.style.display !== '';
  // .adv-body CSS 默认 display:none，展开必须显式设为 block（设 '' 会落回 CSS 的 none）
  d.style.display = open ? 'none' : 'block';
  if (!open) { try { _('provModels').focus(); } catch (e) {} }
}

async function saveProvider(btn) {
  // Token 配额：int64（可达 10^12）超出 parseInt 安全解析习惯，用 Number；空=0，非数值/负数校验拦截
  const tokenRaw = _('provTokenLimit').value.trim();
  const tokenNum = tokenRaw === '' ? 0 : Number(tokenRaw);
  if (!Number.isFinite(tokenNum) || tokenNum < 0) {
    toast(t(_('provTokenWindow').value === 'daily' ? 'Token 配额/日需为 ≥0 的整数' : 'Token 配额/月需为 ≥0 的整数'), 'error');
    return;
  }
  const payload = {
    id: _editingProviderId,
    name: _('provName').value.trim(),
    baseURL: _('provBaseURL').value.trim(),
    apiKey: _('provKey').value.trim(),
    priority: parseInt(_('provPriority').value, 10) || 10,
    timeoutSec: parseInt(_('provTimeout').value, 10) || 300,
    rpmLimit: parseInt(_('provRpmLimit').value, 10) || 0,
    dailyRequestLimit: parseInt(_('provDailyLimit').value, 10) || 0,
    tokenLimit: Math.floor(tokenNum),
    // 契约：tokenWindow = "monthly"|"daily"，后端缺省 monthly
    tokenWindow: _('provTokenWindow').value === 'daily' ? 'daily' : 'monthly',
    proxyUrl: _('provProxyUrl').value.trim(),
    weight: parseInt(_('provWeight').value, 10) || 0,
    modelIds: _('provModels').value.split(',').map(s => s.trim()).filter(Boolean),
    headers: parseHeaderText(_('provHeaders').value),
    enabled: _('provEnabled').value === 'true',
    free: _('provFree').value === 'true',
    connectionIds: _provConnSel.slice(),
    connectionId: '',
  };
  // 变更预览：编辑已有 Provider 时列出与基线的字段差异，确认后才 POST；无改动直接关闭
  const changes = provFormChanges();
  if (!changes.length && _editingProviderId) { hideProviderEditor(); return; }
  if (changes.length && !(await showDiffModal(changes))) return;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  const isNew = !payload.id;
  try {
    const d = await api('POST', '/providers/save', payload);
    _provBaseData = payload; // 刚保存的状态即新基线（不重刷表单，避免服务端脱敏字段覆盖输入）
    rebaselineProvForm();
    toast(t('Provider 已保存'), 'success');
    hideProviderEditor();
    loadProviders();
    loadGlobal(false);
    // 保存后池状态已变：先失效预览缓存，再按需重新拉取（不能复用保存前的批次）
    invalidateImportPreviewCache();
    // 从免费/免密钥预设新建后，自动跑一次分级预览，弹出勾选导入（仍受控，不静默写入）
    if (isNew && _previewAfterSave) {
      const newId = d && d.data && d.data.provider && d.data.provider.id;
      if (newId) {
        _previewAfterSave = false;
        try {
          const pd = await api('POST', '/models/import/preview', { id: newId });
          const pr = pd.data || {};
          if (!pr.error && pr.batch) {
            _importBatch = pr.batch;
            importPreviewCacheSet('id:' + newId, pr.batch);
            renderImportPreviewList();
            _('importPreviewModal').style.display = 'flex';
          }
        } catch (e) {}
      }
    }
  } catch (e) { toast(t('保存失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

function editProvider(id) {
  api('GET', '/providers').then(d => {
    const p = (d.data.providers || []).find(x => x.id === id);
    if (p) showProviderEditor(p);
  });
}

async function deleteProvider(id) {
  if (!(await showConfirm(t('确定删除该 Provider？')))) return;
  try {
    await api('POST', '/providers/delete', { id });
    delete _provRowResults['test:' + id];
    delete _provRowResults['recheck:' + id];
    invalidateImportPreviewCache('id:' + id);
    toast(t('已删除'), 'success');
    loadProviders();
    loadGlobal(false);
  } catch (e) { toast(t('删除失败: ') + e.message, 'error'); }
}

// 内联测试结果统一渲染：绿 ✓ + 摘要 / 红 ✗ + 原因（一律就地展示，不弹 toast-only）
function renderTestResultHTML(ok, text) {
  // 与全站保存反馈同款：showSaveResult / .save-result（✓/✗ + SVG 图标 + 状态色）
  return '<span class="save-result ' + (ok ? 'ok' : 'err') + '">' +
    uiIcon(ok ? 'check' : 'close') + esc(text) + '</span>';
}

// 老接口/网关异常的降级文案：404「page not found」/ 非 JSON 响应（旧版本进程）→ 提示重启代理；
// 其余业务错误（如「无启用条目」）原样透出。
function friendlyTestError(e) {
  const msg = (e && e.message || '').toString();
  if (!msg || /unexpected token|json|404|not found|not allowed/i.test(msg)) {
    return t('接口不可用，请重启代理更新到新版本后再试');
  }
  return t('测试失败: ') + msg;
}

// 列/卡片「测试」：结果写入所在容器（卡片或表格 td）的 .prov-row-test-out（此前写到编辑器内的结果位，编辑器关闭时不可见）
async function testProviderById(id, cell) {
  const host = cell && cell.closest ? cell.closest('.prov-card, td, .drawer-group') : null;
  const out = host ? host.querySelector('.prov-row-test-out') : null;
  const orig = cell ? cell.innerHTML : '';
  if (cell) { cell.disabled = true; cell.innerHTML = '<span class="loading"></span> ' + t('测试中...'); }
  if (out) out.innerHTML = '';
  try {
    const d = await api('POST', '/providers/test', { id });
    const r = d.data || {};
    const text = r.ok ? (r.durationMs + 'ms') : (r.error || t('失败'));
    _provRowResults['test:' + id] = { ok: !!r.ok, text: r.ok ? (r.durationMs + 'ms') : (r.error || t('失败')) };
    if (out) out.innerHTML = renderTestResultHTML(r.ok, text);
  } catch (e) {
    const text = friendlyTestError(e);
    _provRowResults['test:' + id] = { ok: false, text: text };
    if (out) out.innerHTML = renderTestResultHTML(false, text);
  } finally {
    if (cell) { cell.disabled = false; cell.innerHTML = orig; }
  }
}

// 编辑器内「测试连通性」：已有 Provider → {id, model?}；新建（未保存）→ 直接用
// 表单的未保存值（baseURL/apiKey/headers），不再「先保存再按名称匹配」。
// 结果一律内联展示在按钮旁的 #provTestResult。
async function testProvider(btn) {
  const out = _('provTestResult');
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('测试中...'); }
  if (out) out.innerHTML = '<span class="mut"><span class="loading"></span> ' + t('测试中...') + '</span>';
  // 表单里的「模型 ID」是逗号分隔列表，探测取第一个（显式给出的模型按原样使用）
  const model = _('provModels').value.split(',').map(s => s.trim()).filter(Boolean)[0] || '';
  const body = _editingProviderId
    ? { id: _editingProviderId, model: model }
    : { baseURL: _('provBaseURL').value.trim(), apiKey: _('provKey').value.trim(), headers: parseHeaderText(_('provHeaders').value), model: model };
  try {
    const d = await api('POST', '/providers/test', body);
    const r = d.data || {};
    if (out) out.innerHTML = renderTestResultHTML(r.ok, r.ok ? (r.durationMs + 'ms' + (r.model ? ' · ' + r.model : '')) : (r.error || t('失败')));
  } catch (e) {
    if (out) out.innerHTML = renderTestResultHTML(false, friendlyTestError(e));
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

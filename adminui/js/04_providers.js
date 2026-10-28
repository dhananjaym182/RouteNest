// ========== 自定义 Provider ==========
let _editingProviderId = '';
// 从免费类预设新建并保存后，自动触发一次分级导入预览（用户勾选后才写入）
let _previewAfterSave = false;

// 首次加载占位：列表尚未渲染过且请求在途时显示骨架，避免空白闪动
let _provFirstLoad = true;
// 行内测试/检测结果跨 re-render 保留（loadProviders 重绘后回填到结果位）
const _provRowResults = {};
function provRowResultHTML(kind, id) {
  const r = _provRowResults[kind + ':' + id];
  return r ? renderTestResultHTML(r.ok, r.text) : '';
}

async function loadProviders() {
  const box0 = _('providersList');
  if (!box0) return;
  if (_provFirstLoad) {
    // 骨架与卡片同构：占多个卡位而非一条，避免加载完成后从「一条」跳到「网格」的布局闪动
    const skCard = '<div class="prov-card sk-card"><div class="sk-stack"><span class="skeleton"></span><span class="skeleton w70"></span><span class="skeleton w45"></span><span class="skeleton w70"></span><span class="skeleton w45"></span></div></div>';
    box0.innerHTML = '<div class="prov-cards">' + skCard.repeat(6) + '</div>';
  }
  try {
    const [d, sd] = await Promise.all([
      api('GET', '/providers'),
      api('GET', '/providers/sync-status').catch(() => ({ data: {} })),
    ]);
    _provSync = (sd && sd.data && sd.data.providers) || {};
    _provHealth = (d.data && d.data.health) || {};
    const list = d.data.providers || [];
    _provFirstLoad = false;
    list.forEach(p => { _provCache[p.id] = p; });
    const box = _('providersList');
    renderProviderSyncBanner(list);
    if (!list.length) { box.innerHTML = listEmptyState(ICON_PROVIDERS, t('暂无自定义 Provider')); return; }
    box.innerHTML = renderProvCooldownBar(list) +
      '<div class="prov-cards">' + list.map(provCardHTML).join('') + '</div>';
    refreshProvDetailIfOpen();
  } catch (e) {
    _provFirstLoad = false;
    const box = _('providersList');
    if (box) box.innerHTML = '<div class="empty">' + t('加载失败') + ': ' + esc(e.message) +
      '<div class="mt-10"><button class="btn btn-sm" onclick="loadProviders()">' + uiIcon('reset') + t('重试') + '</button></div></div>';
  }
}

// ========== §模型列：紧凑芯片 + 可展开（与日志详情行的渐进披露一致） ==========
const PROV_CHIP_PREVIEW = 3;
function provModelsCellHTML(id, modelIds) {
  const total = (modelIds || []).length;
  if (!total) return '<span class="hint-sm">—</span>';
  const chip = m => '<span class="model-tag chip-clip" title="' + escAttr(m) + '">' + esc(m) + '</span>';
  const shown = modelIds.slice(0, PROV_CHIP_PREVIEW);
  const rest = total - shown.length;
  let html = '<div class="model-chips">' + shown.map(chip).join('');
  if (rest > 0) {
    html += '<button type="button" class="model-tag chip-more" data-count="' + rest + '" data-prov-id="' + escAttr(id) + '" onclick="event.stopPropagation();toggleProvModelChips(this)" aria-expanded="false" title="' + escAttr(t('展开全部') + ' ' + total + t(' 个模型')) + '">+' + rest + '</button>';
  }
  html += '</div>';
  if (rest > 0) {
    html += '<div class="model-chips model-chips-extra">' + modelIds.slice(PROV_CHIP_PREVIEW).map(chip).join('') + '</div>';
  }
  return html;
}

// ========== §卡片视图（master–detail 主侧）：每个卡片区域是独立的流式块，禁止绝对定位/固定高，
// 「0/2,000,000」这类最宽数据在各自块内 ellipsis，天然无重叠（旧表 table-layout:fixed 忽略
// min-width 导致 nowrap 数字横向绘制进邻列，即此前两次返工的重叠根因）。 ==========
function provCardHTML(p) {
  const id = escAttrJS(p.id);
  const n = (p.connectionIds || []).length || (p.connectionId ? 1 : 0);
  const h = _provHealth[p.id] || {};
  // 卡片健康行只放「无样本」徽章；热度块全尺寸进详情
  const heatNone = (h.recent || []).length ? '' : '<span class="heat-none">' + t('暂无样本') + '</span>';
  return '<article class="prov-card" data-prov="' + id + '">' +
    '<div class="prov-card-hit" onclick="openProvDetail(\'' + id + '\')" title="' + t('打开详情') + '">' +
      '<div class="prov-card-head">' +
        '<div class="prov-card-title">' +
          '<button type="button" class="prov-card-name" onclick="event.stopPropagation();openProvDetail(\'' + id + '\')" title="' + t('打开详情') + '" aria-haspopup="dialog">' + esc(p.name) + '</button>' +
          (p.free ? '<span class="green-xs" title="' + escAttr(t('整源免费标记，不代表池内每个模型都免费；逐模型计费以分级预览为准')) + '">free</span>' : '') +
          (n ? '<span class="acc-xs" title="' + t('连接池轮转派发') + '">token×' + n + '</span>' : '') +
        '</div>' +
        '<span class="prov-card-status">' + (p.enabled
          ? '<span class="status active"><span class="status-dot active"></span>' + t('已启用') + '</span>'
          : '<span class="status disabled"><span class="status-dot disabled"></span>' + t('已停用') + '</span>') + '</span>' +
      '</div>' +
      '<div class="prov-models-cell">' + provModelsCellHTML(p.id, p.modelIds || []) + '</div>' +
      '<div class="prov-card-health">' + provMiniHealthHTML(p) + heatNone + '</div>' +
      '<div class="prov-card-url mono" title="' + escAttr(p.baseURL) + '">' + esc(p.baseURL) + '</div>' +
      '<div class="prov-card-meta">' +
        '<span class="pm-k">' + t('优先级') + '</span><span class="pm-v mono">' + esc(String(p.priority)) + '</span>' +
        (p.enabled ? '<span class="pm-sync">' + provSyncCellHTML(p.id) + '</span>' : '<span class="hint-xs">—</span>') +
      '</div>' +
    '</div>' +
    '<div class="prov-card-foot">' +
      '<button type="button" class="btn btn-sm btn-primary" onclick="openProvDetail(\'' + id + '\')">' + t('打开详情') + '</button>' +
      '<button type="button" class="btn btn-sm btn-icon" onclick="editProvider(\'' + p.id + '\')" title="' + t('编辑') + '" aria-label="' + t('编辑') + '">' + uiIcon('edit') + '</button>' +
      '<button type="button" class="btn btn-sm btn-icon" onclick=\'testProviderById("' + p.id + '", this)\' title="' + t('测试') + '" aria-label="' + t('测试') + '">' + uiIcon('bolt') + '</button>' +
      '<button type="button" class="btn btn-sm btn-icon" onclick=\'recheckProvider("' + p.id + '", this)\' title="' + t('重新检测') + '" aria-label="' + t('重新检测') + '">' + uiIcon('reset') + '</button>' +
      '<button type="button" class="btn btn-sm btn-icon" onclick=\'toggleProvider("' + p.id + '", ' + !p.enabled + ', this)\' title="' + t(p.enabled ? '停用' : '启用') + '" aria-label="' + t(p.enabled ? '停用' : '启用') + '">' + uiIcon(p.enabled ? 'pause' : 'play') + '</button>' +
      '<button type="button" class="btn btn-sm btn-icon btn-danger" onclick=\'deleteProvider("' + p.id + '")\' title="' + t('删除') + '" aria-label="' + t('删除') + '">' + uiIcon('close') + '</button>' +
    '</div>' +
    '<div class="prov-card-result">' +
      '<span class="prov-row-test-out">' + provRowResultHTML('test', p.id) + '</span>' +
      '<span class="prov-row-recheck-out">' + provRowResultHTML('recheck', p.id) + '</span>' +
    '</div>' +
  '</article>';
}

// 展开态行高易抖：追加芯片而不是整体换面板，行本身仍受 chips 最大两行约束
function toggleProvModelChips(btn) {
  const cell = btn && btn.closest ? btn.closest('.prov-models-cell') : null;
  const extra = cell ? cell.querySelector('.model-chips-extra') : null;
  if (!extra) return;
  const open = extra.classList.toggle('open');
  btn.classList.toggle('open', open);
  btn.setAttribute('aria-expanded', open ? 'true' : 'false');
  btn.textContent = open ? t('收起') : '+' + btn.dataset.count;
}

// ========== §健康：配额表 / 冷却恢复 / 近期成败热度 ==========
let _provHealth = {};

function provQuotaHtml(p) {
  const h = _provHealth[p.id] || {};
  const lim = h.limits || {};
  const daily = Number(lim.daily) || 0, rpm = Number(lim.rpm) || 0;
  let out = '';
  if (daily > 0) {
    const used = Number(h.daily) || 0;
    const ratio = Math.min(1, used / daily);
    const left = 1 - ratio;
    const tone = left >= 0.7 ? 'ok' : (left >= 0.3 ? 'warn' : 'bad');
    out += '<div class="quota-row" title="' + escAttr(t('每日已用 ') + used + '/' + daily) + '">' +
      '<span class="quota-label">' + t('每日') + '</span>' +
      '<span class="quota-meter"><span class="' + tone + '" style="width:' + (ratio * 100).toFixed(1) + '%"></span></span>' +
      '<span class="quota-num">' + formatNumber(used) + '/' + formatNumber(daily) + '</span></div>';
  }
  if (rpm > 0) {
    const used = Number(h.rpm) || 0;
    const left = 1 - Math.min(1, used / rpm);
    const tone = left >= 0.7 ? 'ok' : (left >= 0.3 ? 'warn' : 'bad');
    out += '<div class="quota-row rpm" title="' + escAttr(t('每分钟已用 ') + used + '/' + rpm) + '">' +
      '<span class="quota-label">RPM</span>' +
      '<span class="quota-meter narrow"><span class="' + tone + '" style="width:' + (Math.min(1, used / rpm) * 100).toFixed(1) + '%"></span></span>' +
      '<span class="quota-num">' + used + '/' + rpm + '</span></div>';
  }
  // Token 配额：health.Tokens = 当前窗口（UTC 月/日）用量，limits.tokens = 窗口上限（0=未设）；
  // limits.tokenWindow = "monthly"|"daily"（缺省 monthly，与后端默认一致）
  const tokWin = lim.tokenWindow === 'daily' ? 'daily' : 'monthly';
  const tokLim = Number(lim.tokens) || 0;
  const tokUsed = Number.isFinite(Number(h.tokens)) ? Math.max(0, Number(h.tokens)) : 0;
  if (tokLim > 0) {
    const ratio = tokUsed / tokLim;
    // 已打满/超出 → 直接坏色调，剩余不再按梯度降级；≥70% 即告警
    const tone = ratio >= 1 ? 'bad' : (ratio >= 0.7 ? 'warn' : 'ok');
    const pct = Math.round(ratio * 100);
    out += '<div class="quota-row tokens" title="' + escAttr(t(tokWin === 'daily' ? '今日 Token 已用 ' : '本月 Token 已用 ') + formatNumber(tokUsed) + '/' + formatNumber(tokLim)) + '">' +
      '<span class="quota-label">Token</span>' +
      '<span class="quota-meter"><span class="' + tone + '" style="width:' + (Math.min(1, ratio) * 100).toFixed(1) + '%"></span></span>' +
      '<span class="quota-num ' + (ratio >= 1 ? 'over' : '') + '">' + formatNumber(tokUsed) + '/' + formatNumber(tokLim) + (pct >= 70 ? '（' + pct + '%）' : '') + '</span></div>';
  } else {
    out += '<div class="quota-row tokens" title="' + escAttr(t(tokWin === 'daily' ? '未设 Token 日配额' : '未设 Token 月配额')) + '">' +
      '<span class="quota-label">Token</span>' +
      '<span class="quota-num mut">—</span></div>';
  }
  return out;
}

function provHeatCellHtml(p) {
  const h = _provHealth[p.id] || {};
  const recent = h.recent || [];
  if (!recent.length) return '<span class="heat-none">' + t('暂无样本') + '</span>';
  const blocks = recent.slice(-12);
  const rate = Number(h.rate) || 0;
  const title = t('成功率') + ' ' + (rate * 100).toFixed(0) + '% · ' + t('样本') + ' ' + (h.samples || 0) +
    (h.avgLatencyMs ? ' · ' + Math.round(h.avgLatencyMs) + 'ms' : '');
  return '<span class="heat-blocks" title="' + escAttr(title) + '">' +
    blocks.map(v => '<i class="' + (v ? 'ok' : 'bad') + '"></i>').join('') + '</span>';
}

function fmtLeft(untilRFC) {
  const ms = new Date(untilRFC).getTime() - Date.now();
  if (isNaN(ms) || ms <= 0) return t('即将恢复');
  const s = Math.floor(ms / 1000);
  if (s < 60) return t('剩 ') + s + 's';
  if (s < 3600) return t('剩 ') + Math.floor(s / 60) + 'm';
  return t('剩 ') + Math.floor(s / 3600) + 'h' + Math.floor((s % 3600) / 60) + 'm';
}

// 列表健康行：单个紧凑微型状态（色点 + 一行短语 + RPM 计数），计量表/热度进详情
function provMiniHealthHTML(p) {
  const h = _provHealth[p.id] || {};
  const lim = h.limits || {};
  const st = _provSync[p.id] || {};
  const daily = Number(lim.daily) || 0, rpm = Number(lim.rpm) || 0;
  const cool = (h.cooldowns || []).length;
  const dailyUsed = Number(h.daily) || 0, rpmUsed = Number(h.rpm) || 0;
  const tok = Number(lim.tokens) || 0, tokUsed = Number(h.tokens) || 0;
  let tone = 'ok', label = '正常';
  if (cool) { tone = 'bad'; label = '冷却中'; }
  else if (!st.lastCheckedMs && st.lastError) { tone = 'bad'; label = '检测出错'; }
  else if (daily > 0 && dailyUsed / daily >= 0.7 || rpm > 0 && rpmUsed / rpm >= 0.7 || tok > 0 && tokUsed / tok >= 0.7 || h.rate !== undefined && h.rate < 0.5) { tone = 'warn'; label = '接近上限'; }
  let html = '<span class="pmini tone-' + tone + '"><span class="pmini-dot" aria-hidden="true"></span>' + t(label) + '</span>';
  if (rpm > 0) html += ' <span class="pmini-rpm mono" title="' + escAttr(t('每分钟已用 ') + rpmUsed + '/' + rpm) + '">RPM ' + rpmUsed + '/' + rpm + '</span>';
  if (cool) html += ' <button type="button" class="chip warn cooldown-chip" onclick="event.stopPropagation();resetProviderCooldown(\'' + escAttr(p.id) + '\', \'\', this)" title="' + escAttr(t('点击清除该 Provider 全部冷却')) + '">' + uiIcon('hourglass') + ' ' + cool + ' · ' + fmtLeft(h.recoveryAt) + '</button>';
  return html;
}

// ========== §供应商详情（master–detail）：列表紧凑，全量数据进只读抽屉 ==========
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
    kv(t('优先级'), esc(String(p.priority))) +
    kv(t('权重'), esc(String(p.weight))) +
    kv(t('代理'), p.proxyUrl ? '<span class="mono detail-clip">' + esc(p.proxyUrl) + '</span>' : '<span class="mut">' + t('未设置') + '</span>') +
    kv(t('创建时间'), p.createdAt ? esc(new Date(p.createdAt).toLocaleString(LC())) : '<span class="mut">—</span>');

  // §2 模型：全量芯片 + 待审计数；已下架模型就地标记并提供一键移除
  const totalModes = (p.modelIds || []).length;
  const delistedSet = {};
  ((_provDelisted && _provDelisted[p.name || '']) || []).forEach(r => { delistedSet[r.id] = true; });
  const models = '<div class="model-chips model-chips-all">' +
      (p.modelIds || []).map(m => delistedSet[m]
        ? '<span class="model-tag chip-clip delisted-chip" title="' + escAttr(t('已从上游官方列表移除，实测可能仍可用；同步只标记不删除，上游报模型不存在时自动清理')) + '">' +
            esc(m) + ' <span class="delisted-badge">' + t('已下架') + '</span>' +
            '<a href="javascript:void(0)" class="delist-inline-x" onclick="removeProvDelistedModel(\'' + escAttrJS(m) + '\')" title="' + escAttr(t('移除')) + '" aria-label="' + escAttr(t('移除') + ' ' + m) + '">' + uiIcon('close') + '</a>' +
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

  // §4 连接：勾选的登录连接（名称/状态）
  const connList = conns.length
    ? conns.map(c =>
        '<div class="detail-conn-row">' +
          '<span class="status ' + (c.status === 'active' ? 'active' : 'disabled') + '"><span class="status-dot ' + (c.status === 'active' ? 'active' : 'disabled') + '"></span>' + t(c.status === 'active' ? '已登录' : '需重新登录') + '</span>' +
          '<span class="mono detail-clip">' + esc(c.email || c.id) + '</span>' + connExpiryHtml(c) +
        '</div>').join('')
    : '<div class="hint-sm">' + t('未链接任何连接') + '</div>';

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
      '<div class="form-group-label">' + t('模型') + ' <span class="grp-count">' + ((p.modelIds || []).length || '') + '</span></div>' + sec.models + provDelistedHTML(p) +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('限额与健康') + '</div>' + sec.health +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('联动连接') + ' <span class="grp-count">' + (sec.hasConns ? sec.hasConns : '') + '</span></div>' + sec.connList +
    '</div>' +
    '<div class="drawer-group">' +
      '<div class="form-group-label">' + t('同步') + '</div>' + sec.sync +
      '<div id="provDetailRecheckOut" class="mt-6"></div>' +
    '</div>';
}

// escAttrJS: id 用于 JS 字符串字面量拼进 onclick —— 复用 escAttr 的转义即可（id 无引号风险）
function escAttrJS(s) { return escAttr(s); }

// ========== §已下架模型清理（详情抽屉）：数据取模型池（GET /models），按 provider 名过滤 =====
// 后端只允许删除 custom 或 delisted 条目（06_models deleteModel 语义），这里仅复用同一端点：
// POST /models/delete {id}，不改 06_models.js。
let _provDelisted = null; // providerName -> [{id, cost}]（每次打开详情时刷新一次）

async function ensureProvDelisted() {
  try {
    const d = await api('GET', '/models');
    const list = (d.data.models || []).filter(m => m.source === 'provider' && m.delisted);
    _provDelisted = {};
    list.forEach(m => {
      const k = m.provider || '';
      (_provDelisted[k] = _provDelisted[k] || []).push({ id: m.id, cost: m.cost || '' });
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
  const rows = _provDelisted[p.name || ''] || [];
  if (!rows.length) return '';
  const list =
    '<div class="delist-rows">' +
    rows.map(m =>
      '<div class="detail-conn-row delist-row">' +
        '<span class="mono detail-clip">' + esc(m.id) + '</span>' +
        '<span class="delisted-badge" title="' + escAttr(t('已从上游官方列表移除，实测可能仍可用；同步只标记不删除，上游报模型不存在时自动清理')) + '">' + t('已下架') + '</span>' +
        (m.cost === 'free' ? '<span class="model-tag free">' + t('免费') + '</span>' : '') +
        '<button type="button" class="btn btn-sm btn-xs btn-danger delist-remove" onclick="removeProvDelistedModel(\'' + escAttrJS(m.id) + '\')" title="' + t('移除') + '" aria-label="' + t('移除') + ' ' + escAttr(m.id) + '">' + t('移除') + '</button>' +
      '</div>').join('') +
    '</div>' +
    '<div class="form-actions mt-6"><button type="button" class="btn btn-sm" onclick="removeAllProvDelisted(this)">' + t('移除全部已下架') + '</button></div>';
  return '<div class="delist-sub"><div class="form-group-label">' + t('已下架') + ' <span class="grp-count">' + rows.length + '</span>' +
    '<span class="hint-xs ml-8">' + t('已从上游官方列表移除，可在此手动清理') + '</span></div>' + list + '</div>';
}

// 单个移除：与模型页 deleteModel 同端点同文案（仅复用，不改 06_models.js）
async function removeProvDelistedModel(id) {
  if (!(await showConfirm(t('确认删除模型 ') + id + ' ?'))) return;
  try {
    await api('POST', '/models/delete', { id });
    toast(t('模型已删除: ') + id, 'success');
  } catch (e) {
    toast(t('删除失败: ') + e.message, 'error');
  }
  _provDelisted = null;
  loadProviders();              // 列表卡片（pending/delisted pill）
  ensureProvDelisted();         // 拉新的已下架清单 → 完成后经 refreshProvDetailIfOpen 重绘详情
}

// 一键移除该 provider 全部已下架模型（逐个走同一删除端点，服务端逐条校验 custom/delisted）
async function removeAllProvDelisted(btn) {
  const id = _provDetailOpenId;
  const p = id ? _provCache[id] : null;
  const rows = (p && _provDelisted && _provDelisted[p.name || '']) || [];
  if (!rows.length) { toast(t('没有已下架模型'), 'info'); return; }
  if (!(await showConfirm(t('确定移除 ') + rows.length + t(' 个已下架模型？')))) return;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>'; }
  let okN = 0, errMsg = '';
  for (const m of rows) {
    try { await api('POST', '/models/delete', { id: m.id }); okN++; }
    catch (e) { errMsg = e.message; }
  }
  if (okN) toast(t('已移除 ') + okN + t(' 个已下架模型'), 'success');
  else if (errMsg) toast(t('删除失败: ') + errMsg, 'error');
  if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  _provDelisted = null;
  loadProviders();
  ensureProvDelisted();
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
    const text = r.ok ? (r.durationMs + 'ms') : (r.error || t('失败'));
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
    loadProviders(); // 刷新列表状态列（同时经 refreshProvDetailIfOpen 重绘详情）
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
        '<button class="btn btn-sm" onclick="resetProviderCooldown(\'' + escAttr(r.id) + '\', \'' + escAttr(r.model) + '\', this)">' + t('重置') + '</button>' +
      '</div>').join('') + '</div>';
}

async function resetProviderCooldown(id, model, btn) {
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>'; }
  try {
    const d = await api('POST', '/providers/cooldown-reset', { id: id, model: model || undefined });
    const n = (d.data && d.data.removed) || 0;
    toast(t('已清除 ') + n + t(' 条冷却'), 'success');
    loadProviders();
  } catch (e) { toast(t('重置失败: ') + e.message, 'error'); if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}


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
    loadProviders();
  } catch (e) {
    toast(t('检测失败') + ': ' + e.message, 'error');
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
  } catch (e) { toast(t('保存失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

function showProviderEditor(p) {
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
    if (out) out.innerHTML = renderTestResultHTML(false, friendlyTestError(e));
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
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
    (p.loginFlow ? '<span class="model-source-badge zen" title="' + t('设备码连接：在「账号与连接」添加，编辑器「账号连接池」勾选即可；无需 API Key') + '">' + t('需登录') + '</span>' : (g.badge ? '<span class="' + g.cls + '">' + t(g.badge) + '</span>' : '')) +
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
    toast(t('已填充登录类预设：请到「账号与连接」完成设备码登录，再在编辑器「账号连接池」勾选连接'), 'info');
  } else if (p.kind === 'free' || p.kind === 'local') {
    // 免密钥/本地源：直接走上游探测，弹出模型多选（免费/付费分组）
    toast(t('免密钥预设：正在自动拉取模型…'), 'info');
    fetchProviderModels(_('provFetchModelsBtn'));
  } else {
    toast(t('已填充预设，请补 API Key 和模型 ID'), 'info');
  }
}

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
  const basis = why ? t(why) : (it.reason ? t(it.reason) : '');
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
    const r = d.data || {};
    if (r.error) { toast(t('上游拉取失败') + ': ' + r.error, 'error'); return; }
    _importBatch = r.batch;
    importPreviewCacheSet(key, _importBatch);
    renderImportPreviewList();
    _('importPreviewModal').style.display = 'flex';
  } catch (e) {
    toast(t('加载失败') + ': ' + esc(e.message), 'error');
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
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

function renderImportPreviewList() {
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
        ? '<span class="import-basis" title="' + escAttr(why) + '">' + t('依据: ') + esc(t(_tierSignals[it.signal] || it.reason || it.signal)) + '</span>'
        : '';
      let action;
      if (it.action === 'add') action = '<span class="model-source-badge zen">' + t('新增') + '</span>';
      else if (it.action === 'tier_change') action = '<span class="model-source-badge provider">' + t('层级变化') + ': ' + esc(it.oldTier === 'free' ? t('免费') : t('付费')) + ' → ' + esc(it.tier === 'free' ? t('免费') : t('付费')) + '</span>';
      else if (it.action === 'protected') action = '<span class="model-source-badge" title="' + escAttr(it.reason || '') + '">' + t('受保护') + '</span>';
      else action = '<span class="model-source-badge dim-op">' + t('无变化') + '</span>';
      return '<label class="combo-pick-row prov-flow-row" data-action="' + escAttr(it.action) + '" data-tier="' + (it.tier === 'free' ? 'free' : 'paid') + '">' +
        '<input type="checkbox" class="import-approve" onchange="updateImportCount()" value="' + esc(it.modelId) + '" ' + (it.action === 'add' ? 'checked' : '') + (selectable ? '' : ' disabled') + '>' +
        '<span class="combo-pick-id">' + esc(it.modelId) + '</span>' + tierBadge(it.tier) + sig + action + '</label>';
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

// ========== §8.3 登录连接：设备码登录 / 粘贴 token / 列表·测试·删除 ==========
let _connFlows = [];
let _connList = [];
let _loginSessionId = '';
let _loginPollTimer = null;

// 流程 key → 用户可读名（未知 key 回落原样显示）
const _flowLabels = {
  kilocode: 'Kilo Code', github: 'GitHub Copilot', qwen: 'Qwen Chat',
  'kimi-coding': 'Kimi for Coding', 'codebuddy-cn': 'CodeBuddy CN（腾讯）',
  'codebuddy-intl': 'CodeBuddy Intl', workbuddy: 'WorkBuddy',
  qoder: 'Qoder', 'qoder-cn': 'Qoder CN',
};
function flowLabel(p) { return t(_flowLabels[p] || p); }

async function loadConnections() {
  try {
    const d = await api('GET', '/connections');
    const r = d.data || {};
    _connFlows = r.flows || [];
    _connList = r.connections || [];
  } catch (e) {
    _connFlows = []; _connList = [];
  }
  renderConnectionProviders();
  renderConnectionsList();
  // 导入弹窗的 Provider 下拉随连接列表刷新（流程 / 已有连接分组可能变化）
  const imp = _('connImportModal');
  if (imp && imp.style.display === 'flex') connImportBuildSelect();
  const ed = _('providerEditor');
  if (ed && ed.classList.contains('open')) renderConnPick();
  refreshProvDetailIfOpen();
}

// Wave-5: OAuth providers render as tiles grouped by connection state, so
// login starts from the provider itself instead of a dropdown. Connected
// providers keep an "Add Account" action for multi-account round-robin.
function connsForProvider(provider) {
  return _connList.filter(c => c.provider === provider);
}

function flowIconText(provider) {
  return flowLabel(provider).trim().slice(0, 2).toUpperCase();
}

function renderConnectionProviders() {
  const box = _('connProviderGroups');
  if (!box) return;
  if (!_connFlows.length) { box.innerHTML = ''; return; }

  const byProv = {};
  _connList.forEach(c => { const k = c.provider || ''; (byProv[k] = byProv[k] || []).push(c); });
  const connected = [], unconnected = [];
  _connFlows.slice().sort((a, b) => flowLabel(a).localeCompare(flowLabel(b))).forEach(p => {
    (byProv[p] && byProv[p].length ? connected : unconnected).push(p);
  });
  connected.sort((a, b) =>
    (byProv[b] || []).length - (byProv[a] || []).length || flowLabel(a).localeCompare(flowLabel(b)));

  const tile = (p) => {
    const list = byProv[p] || [];
    const active = list.filter(c => c.status === 'active').length;
    const reauth = list.length - active;
    const has = list.length > 0;
    let state;
    if (!has) {
      state = '<span class="conn-tile-state"><span class="status-dot disabled"></span>' + t('未连接') + '</span>';
    } else {
      state = '<span class="conn-tile-state"><span class="status-dot active"></span>' + active + ' ' + t('个账号');
      if (reauth) state += ' · <span class="warn-txt">' + reauth + ' ' + t('需重新登录') + '</span>';
      state += '</span>';
    }
    const action = '<button class="btn btn-sm' + (has ? '' : ' btn-primary') + '" onclick="startProviderLogin(\'' + escAttr(p) + '\', this)">' +
      t(has ? '添加账号' : '连接') + '</button>';
    return '<div class="conn-provider-tile' + (has ? ' connected' : '') + '">' +
      '<div class="conn-tile-main">' +
        '<div class="conn-tile-icon">' + esc(flowIconText(p)) + '</div>' +
        '<div class="conn-tile-body">' +
          '<div class="conn-tile-name">' + esc(flowLabel(p)) + '</div>' +
          '<div class="conn-tile-sub mono">' + esc(p) + '</div>' +
        '</div>' + state +
      '</div>' +
      '<div class="conn-tile-foot">' +
        '<span class="conn-tile-meta">' + (has ? list.length + ' ' + t('个连接') : t('设备码登录')) + '</span>' +
        '<div class="conn-tile-actions">' + action + '</div>' +
      '</div>' +
    '</div>';
  };
  const group = (title, list) => !list.length ? '' :
    '<div class="conn-provider-group">' +
      '<div class="conn-provider-group-title">' + title + ' <span class="conn-provider-count">' + list.length + '</span></div>' +
      '<div class="conn-provider-grid">' + list.map(tile).join('') + '</div>' +
    '</div>';
  box.innerHTML = group(t('已连接'), connected) + group(t('未连接'), unconnected);
}

// Token 到期倒计时：<1h 红（紧急）、<24h 琥珀、其余灰
function connExpiryHtml(c) {
  const ms = Number(c.expiresAt) || 0;
  if (!ms) return '';
  const left = ms - Date.now();
  if (left <= 0) return '<div class="conn-expiry bad">' + t('Token 已过期') + '</div>';
  const s = Math.floor(left / 1000);
  let text;
  if (s < 3600) text = t('剩 ') + Math.floor(s / 60) + 'm';
  else if (s < 86400) text = t('剩 ') + Math.floor(s / 3600) + 'h' + Math.floor((s % 3600) / 60) + 'm';
  else text = t('剩 ') + Math.floor(s / 86400) + 'd';
  const tone = s < 3600 ? 'bad' : (s < 86400 ? 'warn' : '');
  return '<div class="conn-expiry ' + tone + '" title="' + escAttr(new Date(ms).toLocaleString(LC())) + '">' + t('Token 到期 ') + text + '</div>';
}

// Wave-4: connection rows live on the merged Accounts page (账号与连接),
// grouped by provider service, each with its own usage + per-model stats.
function renderConnectionsList() {
  const box = _('connectionsList');
  if (!box) return;
  if (!_connList.length) { box.innerHTML = listEmptyState(ICON_LOGINS, t('暂无登录连接') + ' · ' + t('用设备码登录或导入已有 Token')); return; }
  const sorted = _connList.slice().sort((a, b) =>
    String(a.provider).localeCompare(String(b.provider)) || String(a.email || '').localeCompare(String(b.email || '')));
  let lastProv = null;
  let altIdx = 0;
  const rows = sorted.map(c => {
    let groupRow = '';
    if (c.provider !== lastProv) {
      lastProv = c.provider;
      groupRow = '<tr class="conn-group-row"><td colspan="6">' + esc(flowLabel(c.provider)) +
        ' <span class="mono grp-count">' + esc(c.provider) + '</span></td></tr>';
    }
    const stats = Object.values(c.modelStats || {}).sort((x, y) => y.totalTokens - x.totalTokens);
    const statRows = stats.length
      ? '<tr class="row-alt"><td colspan="6" class="pl-32 note-sm txt-strong">' + t('按模型统计') + ' · ' + stats.length + ' ' + t('个模型') + '</td></tr>' +
        stats.map(st =>
          '<tr class="row-alt">' +
            '<td class="mono pl-32">' + esc(st.modelId) +
              ' <span class="model-tag tag-xs' + (st.cost === 'free' ? ' free' : '') + '">' + esc(st.cost || 'paid') + '</span></td>' +
            '<td></td>' +
            '<td>' + formatNumber(st.usageCount) + '</td>' +
            '<td>' + formatTokenCount(st.promptTokens) + '</td>' +
            '<td>' + formatTokenCount(st.totalTokens) + '</td>' +
            '<td>' + formatTokenCount(st.cachedTokens) + '</td>' +
          '</tr>').join('')
      : '<tr class="row-alt"><td colspan="6" class="pl-32 hint-sm">' + t('暂无数据') + '</td></tr>';
    return groupRow + '<tr' + (altIdx++ % 2 ? ' class="alt"' : '') + '>' +
      '<td><button class="btn btn-sm btn-icon" onclick="toggleConnStats(\'' + escAttr(c.id) + '\', this)" title="' + t('展开') + '" aria-label="' + t('展开') + '">'+uiIcon('caretRight')+'</button></td>' +
      '<td class="hint-xs">' + esc(c.authType === 'device_code' ? t('设备码登录') : c.authType === 'import_token' ? t('导入 Token') : (c.authType || '')) + '</td>' +
      '<td class="note-sm">' + esc(c.email || (c.providerSpecificData && c.providerSpecificData.orgId ? 'org ' + c.providerSpecificData.orgId : '—')) + '</td>' +
      '<td>' + (c.status === 'active'
        ? '<span class="status active"><span class="status-dot active"></span>' + t('已登录') + '</span>'
        : '<span class="status disabled"><span class="status-dot disabled"></span>' + t('需重新登录') + '</span>') + connExpiryHtml(c) + '</td>' +
      '<td class="note-sm">' + (c.usageCount
        ? formatNumber(c.usageCount) + ' ' + t('请求') + ' · ' + formatTokenCount(c.totalTokens || 0) + ' token'
        : '<span class="mut">—</span>') + '</td>' +
      '<td><button class="btn btn-sm" onclick=\'testConnection("' + c.id + '", this)\'>' + t('测试') + '</button> ' +
          '<span class="conn-row-test-out"></span> ' +
          '<button class="btn btn-sm btn-danger" onclick=\'deleteConnection("' + c.id + '")\' title="' + t('删除') + '" aria-label="' + t('删除') + '">'+uiIcon('close')+'</button></td>' +
      '</tr>' +
      '<tr id="connStatsRow-' + escAttr(c.id) + '" style="display:none"><td colspan="6" class="stats-embed">' +
        '<table class="model-subtable">' +
          '<tr><th class="pl-32 th-left">' + t('模型') + '</th><th></th><th class="th-left">' + t('请求') + '</th><th class="th-left">' + t('输入') + '</th><th class="th-left">' + t('总量') + '</th><th class="th-left">' + t('缓存') + '</th></tr>' +
          statRows +
        '</table></td></tr>';
  }).join('');
  box.innerHTML = '<table class="action-table"><thead><tr><th></th><th>' + t('方式') + '</th><th>' + t('账号') + '</th><th>' + t('状态') + '</th><th>' + t('用量') + '</th><th>' + t('操作') + '</th></tr></thead><tbody>' + rows + '</tbody></table>';
}

function toggleConnStats(id, btn) {
  const row = _('connStatsRow-' + id);
  if (!row) return;
  const hidden = row.style.display === 'none';
  row.style.display = hidden ? '' : 'none';
  btn.innerHTML = hidden ? uiIcon('caretDown') : uiIcon('caretRight');
  btn.title = hidden ? t('收起') : t('展开');
}

function startProviderLogin(provider, btn) {
  if (!provider) return;
  const title = _('connLoginTitle');
  if (title) title.textContent = t('设备码登录') + ' · ' + flowLabel(provider);
  _('connLoginCode').textContent = '';
  const link = _('connLoginUrl');
  link.textContent = ''; link.removeAttribute('href');
  _('connLoginCode').parentElement.style.display = '';
  _('connLoginStatus').innerHTML = '<span class="loading"></span> ' + t('发起中...');
  _('connLoginModal').style.display = 'flex';
  beginDeviceLogin(btn, provider);
}

function closeDeviceLogin() {
  if (_loginPollTimer) { clearInterval(_loginPollTimer); _loginPollTimer = null; }
  _loginSessionId = '';
  const m = _('connLoginModal');
  if (m) m.style.display = 'none';
}

async function beginDeviceLogin(btn, provider) {
  provider = provider || 'kilocode';
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('发起中...'); }
  try {
    const d = await api('POST', '/connections/login/start', { provider: provider });
    const r = d.data || {};
    if (r.error) {
      _('connLoginStatus').innerHTML = renderTestResultHTML(false, r.error);
      return;
    }
    _loginSessionId = r.sessionId;
    _('connLoginCode').textContent = r.userCode || '';
    // Qoder identifies the device by nonce+verifier: the user just opens the
    // link and approves in the browser, there is no code to type.
    const browserOnly = provider === 'qoder' || provider === 'qoder-cn';
    const hint = _('connLoginHint');
    if (hint) {
      hint.textContent = browserOnly
        ? t('在浏览器中打开下面的链接并完成授权，然后保持本窗口打开。')
        : t('在浏览器中打开下面的地址并输入此代码，然后保持本窗口打开。');
    }
    const codeRow = _('connLoginCode') ? _('connLoginCode').parentElement : null;
    if (codeRow) codeRow.style.display = browserOnly ? 'none' : '';
    const a = _('connLoginUrl');
    a.href = r.authUrl || '#'; a.textContent = r.authUrl || '';
    _('connLoginStatus').innerHTML = '<span class="loading"></span> ' + t('等待授权...');
    if (_loginPollTimer) clearInterval(_loginPollTimer);
    _loginPollTimer = setInterval(pollDeviceLogin, 2000);
  } catch (e) {
    _('connLoginStatus').innerHTML = renderTestResultHTML(false, e.message);
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

async function pollDeviceLogin() {
  if (!_loginSessionId) return;
  try {
    const d = await api('GET', '/connections/login/status?sessionId=' + encodeURIComponent(_loginSessionId));
    const r = d.data || {};
    if (!r.done) return;
    clearInterval(_loginPollTimer); _loginPollTimer = null;
    if (r.success) {
      const email = r.email || (r.connection && r.connection.email) || '';
      toast(t('登录成功') + (email ? ': ' + email : ''), 'success');
      closeDeviceLogin();
      loadConnections();
    } else {
      _('connLoginStatus').innerHTML = renderTestResultHTML(false, r.error || t('登录失败'));
    }
  } catch (e) { /* 网络抖动下轮询继续 */ }
}

function copyDeviceCode() {
  const code = (_('connLoginCode') && _('connLoginCode').textContent) || '';
  if (!code) return;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(code); }
    else {
      const ta = document.createElement('textarea'); ta.value = code; document.body.appendChild(ta);
      ta.select(); document.execCommand('copy'); document.body.removeChild(ta);
    }
    toast(t('代码已复制'), 'info');
  } catch (e) {}
}

// ===== 导入 Token 弹窗：Provider 下拉（流程 / 已有连接 / Provider 列表）+ 手动 / 批量 =====
let _connImpProvFetch = null;
let _connBatchBusy = false;
let _connBatchGen = 0;

// 分组去重：同一个 key 只出现在第一个命中的分组里（case-sensitive）
function connImpProviderGroups() {
  const seen = new Set();
  const take = keys => {
    const out = [];
    keys.forEach(k => { if (!k || seen.has(k)) return; seen.add(k); out.push(k); });
    return out;
  };
  const byLabel = (a, b) => flowLabel(a).localeCompare(flowLabel(b));
  const flowKeys = take(_connFlows.filter(Boolean).slice().sort(byLabel));
  const connKeys = take(Array.from(new Set(_connList.map(c => c.provider).filter(Boolean))).sort(byLabel));
  const provKeys = take(Object.keys(_provCache)
    .map(id => (_provCache[id] && _provCache[id].name) || '')
    .filter(Boolean)
    .sort((a, b) => a.localeCompare(b)));
  return [
    { label: t('已登录流程'), keys: flowKeys, labelOf: flowLabel },
    { label: t('已有连接'), keys: connKeys, labelOf: flowLabel },
    { label: t('Provider 列表'), keys: provKeys, labelOf: k => k },
  ].filter(g => g.keys.length);
}

function connImpOptionsHTML(groups, selected) {
  let html = '';
  groups.forEach(g => {
    html += '<optgroup label="' + escAttr(g.label) + '">';
    g.keys.forEach(k => {
      html += '<option value="' + escAttr(k) + '"' + (k === selected ? ' selected' : '') + '>' +
        esc(g.labelOf(k)) + '</option>';
    });
    html += '</optgroup>';
  });
  html += '<option value="__custom__"' + (selected === '__custom__' ? ' selected' : '') + '>' + esc(t('自定义…')) + '</option>';
  return html;
}

// 每次打开弹窗都重建，避免下拉为空或停留在过期数据上
function connImportBuildSelect() {
  const selM = _('connImpProvider');
  if (!selM) return;
  const groups = connImpProviderGroups();
  const valid = v => v === '__custom__' || (!!v && groups.some(g => g.keys.indexOf(v) >= 0));
  const selB = _('connBatchProvider');
  const curM = selM.value || '';
  const mSel = valid(curM) ? curM : (groups.length ? '' : '__custom__');
  const curB = selB ? (selB.value || '') : '';
  const bSel = valid(curB) ? curB : mSel;
  selM.innerHTML = connImpOptionsHTML(groups, mSel);
  if (selB) selB.innerHTML = connImpOptionsHTML(groups, bSel);
  connImportProviderChange();
  connBatchProviderChange(true);
  const panel = _('connImpPanelBatch');
  if (panel && panel.classList.contains('active')) connBatchPreview();
}

// 手动 / 批量两个下拉保持同一个 provider key；自定义输入各自独立
function connImportProviderChange() {
  const sel = _('connImpProvider'), box = _('connImpProviderCustom');
  if (!sel || !box) return;
  const custom = sel.value === '__custom__';
  box.hidden = !custom;
  const other = _('connBatchProvider');
  if (other && other.value !== sel.value) {
    other.value = sel.value;
    const ob = _('connBatchProviderCustom');
    if (ob) ob.hidden = !custom;
  }
  if (custom && document.activeElement === sel) { try { box.focus(); } catch (e) {} }
}

function connBatchProviderChange(fromBuild) {
  const sel = _('connBatchProvider'), box = _('connBatchProviderCustom');
  if (!sel || !box) return;
  const custom = sel.value === '__custom__';
  box.hidden = !custom;
  const other = _('connImpProvider');
  if (other && other.value !== sel.value) {
    other.value = sel.value;
    const ob = _('connImpProviderCustom');
    if (ob) ob.hidden = !custom;
  }
  if (custom && !fromBuild && document.activeElement === sel) { try { box.focus(); } catch (e) {} }
  if (!fromBuild) connBatchPreview();
}

// 弹窗内的 tab 切换（不复用页面级 importTabs 的事件绑定）
function connImportTab(name) {
  const modal = _('connImportModal');
  if (!modal) return;
  name = name === 'batch' ? 'batch' : 'manual';
  modal.querySelectorAll('.conn-imp-tabs .tab').forEach(el => {
    const on = el.dataset.connTab === name;
    el.classList.toggle('active', on);
    el.setAttribute('aria-selected', on ? 'true' : 'false');
    el.tabIndex = on ? 0 : -1;
  });
  modal.querySelectorAll('[data-conn-panel]').forEach(el => {
    el.classList.toggle('active', el.dataset.connPanel === name);
  });
  const mf = _('connImpManualFoot'), bf = _('connImpBatchFoot');
  if (mf) mf.hidden = name !== 'manual';
  if (bf) bf.hidden = name !== 'batch';
  if (name === 'batch') { connBatchPreview(); return; }
  if (modal.style.display === 'flex') { try { _('connImpToken').focus(); } catch (e) {} }
}

function connImportTabKey(e) {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
  e.preventDefault();
  const to = e.key === 'ArrowLeft' ? 'manual' : 'batch';
  connImportTab(to);
  const next = _(to === 'manual' ? 'connImpTabManual' : 'connImpTabBatch');
  if (next) next.focus();
}

function connImportToggleToken(btn) {
  const inp = _('connImpToken');
  if (!inp) return;
  const show = inp.type === 'password';
  inp.type = show ? 'text' : 'password';
  if (btn) {
    btn.textContent = show ? t('隐藏') : t('显示');
    btn.setAttribute('aria-pressed', show ? 'true' : 'false');
  }
}

// 解析 select / 自定义输入得到最终 provider key
function connImpResolvedProvider(selId, customId) {
  const sel = _(selId), box = _(customId);
  const v = sel ? (sel.value || '') : '';
  if (v === '__custom__') return box ? (box.value || '').trim() : '';
  return v.trim();
}

function connImpShowResult(ok, msg) {
  const el = _('connImpResult');
  if (!el) return;
  el.innerHTML = '<div class="save-result ' + (ok ? 'ok' : 'err') + '">' +
    uiIcon(ok ? 'check' : 'close') + '<span>' + esc(msg) + '</span></div>';
}

async function showConnectionImport() {
  const modal = _('connImportModal');
  if (!modal) return;
  modal.style.display = 'flex';
  connImportTab('manual');
  connImportBuildSelect(); // 先用现有缓存填充，避免下拉空白
  // 流程 / 连接可能还没加载过（例如从 Provider 抽屉直接打开）
  if (!_connFlows.length) { await loadConnections(); }
  // Provider 下拉数据源：仅在缓存为空时补一次请求
  if (!Object.keys(_provCache).length) {
    if (!_connImpProvFetch) {
      _connImpProvFetch = api('GET', '/providers')
        .then(d => { ((d.data && d.data.providers) || []).forEach(p => { if (p && p.id) _provCache[p.id] = p; }); })
        .catch(() => {})
        .then(() => { _connImpProvFetch = null; });
    }
    await _connImpProvFetch;
  }
  connImportBuildSelect();
}

function hideConnectionImport() {
  const modal = _('connImportModal');
  if (modal) modal.style.display = 'none';
  _connBatchBusy = false;
  _connBatchGen++;
  ['connImpToken', 'connImpEmail', 'connImpOrg', 'connBatchInput', 'connImpProviderCustom', 'connBatchProviderCustom']
    .forEach(id => { const el = _(id); if (el) el.value = ''; });
  ['connImpResult', 'connBatchResult', 'connBatchPreview', 'connBatchFileName']
    .forEach(id => { const el = _(id); if (el) el.innerHTML = ''; });
  const selM = _('connImpProvider'), selB = _('connBatchProvider');
  if (selM) selM.value = '';
  if (selB) selB.value = '';
  const file = _('connBatchFile'); if (file) file.value = '';
  const tok = _('connImpToken');
  if (tok) tok.type = 'password';
  const reveal = _('connImpReveal');
  if (reveal) { reveal.textContent = t('显示'); reveal.setAttribute('aria-pressed', 'false'); }
  connImportTab('manual');
}

async function submitConnectionImport(btn) {
  const provider = connImpResolvedProvider('connImpProvider', 'connImpProviderCustom');
  const token = ((_('connImpToken') || {}).value || '').trim();
  if (!provider) {
    connImpShowResult(false, t('请选择服务'));
    toast(t('请选择服务'), 'error');
    try { _('connImpProvider').focus(); } catch (e) {}
    return;
  }
  if (!token) {
    connImpShowResult(false, t('请填写 Access Token'));
    toast(t('请填写 Access Token'), 'error');
    try { _('connImpToken').focus(); } catch (e) {}
    return;
  }
  const body = {
    provider: provider,
    accessToken: token,
    email: ((_('connImpEmail') || {}).value || '').trim(),
    orgId: ((_('connImpOrg') || {}).value || '').trim(),
  };
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/connections/import', body);
    connImpShowResult(true, t('连接已保存') + ' · ' + provider);
    toast(t('连接已保存'), 'success');
    _('connImpToken').value = '';
    loadConnections();
  } catch (e) {
    connImpShowResult(false, t('保存失败: ') + e.message);
    toast(t('保存失败: ') + e.message, 'error');
  }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

// ===== 批量导入：宽松解析 → 预览 → 逐项结果 =====
function connImpNormalizeItem(raw, fallback) {
  if (raw == null) return { provider: fallback, accessToken: '', email: '', orgId: '' };
  if (typeof raw === 'string' || typeof raw === 'number') {
    return { provider: fallback, accessToken: String(raw).trim(), email: '', orgId: '' };
  }
  if (typeof raw !== 'object') return { provider: fallback, accessToken: '', email: '', orgId: '' };
  return {
    provider: String(raw.provider || '').trim() || fallback,
    accessToken: String(raw.accessToken || raw.access_token || raw.token || '').trim(),
    email: String(raw.email || '').trim(),
    orgId: String(raw.orgId || raw.org_id || '').trim(),
  };
}

// 纯文本：每行一条；`provider,token[,email]`（逗号或制表符分隔），空行与 # 注释跳过
function connImpParseText(text, fallback) {
  const out = [];
  String(text || '').split(/\r?\n/).forEach(line => {
    const s = line.trim();
    if (!s || s.charAt(0) === '#') return;
    const parts = (s.indexOf('\t') >= 0 ? s.split('\t') : s.split(',')).map(x => x.trim());
    if (parts.length <= 1) out.push({ provider: fallback, accessToken: parts[0] || '', email: '', orgId: '' });
    else out.push({ provider: parts[0], accessToken: parts[1], email: parts[2] || '', orgId: '' });
  });
  return out;
}

// 解析顺序：JSON 数组 → {items|tokens} → 单对象 → 纯文本
function parseConnBatch(text, fallback) {
  const raw = String(text || '').trim();
  const result = { items: [], total: 0, duplicates: 0, invalid: [] };
  if (!raw) return result;
  let list;
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) list = parsed;
    else if (parsed && typeof parsed === 'object') {
      if (Array.isArray(parsed.items)) list = parsed.items;
      else if (Array.isArray(parsed.tokens)) list = parsed.tokens;
      else list = [parsed];
    } else if (typeof parsed === 'string' || typeof parsed === 'number') list = [parsed];
    else list = [];
  } catch (e) {
    list = connImpParseText(raw, fallback);
  }
  const seen = new Set();
  list.forEach((entry, i) => {
    const item = connImpNormalizeItem(entry, fallback);
    result.total++;
    if (!item.accessToken) {
      result.invalid.push({ row: i + 1, provider: item.provider, reason: t('缺少 Token') });
      return;
    }
    if (!item.provider) {
      result.invalid.push({ row: i + 1, provider: '', reason: t('缺少服务') });
      return;
    }
    const key = item.provider + '\n' + item.accessToken;
    if (seen.has(key)) { result.duplicates++; return; }
    seen.add(key);
    result.items.push(item);
  });
  return result;
}

function connBatchPreview() {
  const box = _('connBatchPreview');
  if (!box) return;
  const ta = _('connBatchInput');
  const fallback = connImpResolvedProvider('connBatchProvider', 'connBatchProviderCustom');
  const parsed = parseConnBatch(ta ? ta.value : '', fallback);
  box.classList.remove('err');
  if (!parsed.total) {
    box.innerHTML = esc(t('粘贴或选择文件后自动解析'));
    return;
  }
  let html = t('解析到 ') + parsed.total + t(' · 去重后 ') + parsed.items.length;
  if (parsed.duplicates) html += t(' · 跳过重复 ') + parsed.duplicates;
  if (parsed.invalid.length) {
    html += t(' · 无效 ') + parsed.invalid.length;
    html += '<span class="conn-imp-invalid">' +
      parsed.invalid.slice(0, 4).map(x => esc(t('行 ') + x.row + ' · ' + x.reason)).join(' · ') +
      (parsed.invalid.length > 4 ? ' …' : '') + '</span>';
  }
  box.innerHTML = html;
}

function connBatchPickFile() {
  const f = _('connBatchFile');
  if (f) f.click();
}

async function connBatchFileChosen(event) {
  const input = event && event.target;
  const file = input && input.files && input.files[0];
  if (!file) return;
  const box = _('connBatchPreview');
  const nameEl = _('connBatchFileName');
  if (file.size > 1024 * 1024) {
    if (input) input.value = '';
    if (nameEl) nameEl.textContent = '';
    if (box) { box.classList.add('err'); box.innerHTML = esc(t('文件不能超过 1 MB')); }
    toast(t('文件不能超过 1 MB'), 'error');
    return;
  }
  try {
    const text = await file.text();
    const ta = _('connBatchInput');
    if (ta) ta.value = text; // 只回填，交给用户确认后再导入
    if (nameEl) nameEl.textContent = t('已选择 ') + file.name;
    connBatchPreview();
  } catch (e) {
    if (box) { box.classList.add('err'); box.innerHTML = esc(t('读取文件失败: ') + e.message); }
  } finally {
    if (input) input.value = ''; // 允许重复选择同一个文件
  }
}

function connBatchRenderResult(r) {
  const box = _('connBatchResult');
  if (!box) return;
  const imported = Number(r.imported) || 0;
  const failed = Number(r.failed) || 0;
  const fails = (r.results || []).filter(x => x && x.ok === false);
  let html = '<div class="conn-imp-sum ' + (failed ? (imported ? 'warn' : 'bad') : 'ok') + '">' +
    uiIcon(failed ? 'close' : 'check') +
    '<span>' + t('成功 ') + imported + t(' · 失败 ') + failed + '</span></div>';
  if (fails.length) {
    html += '<ul class="conn-imp-fails">' + fails.map(x => {
      const n = Number(x.index);
      return '<li><span class="mono conn-imp-idx">#' + (Number.isFinite(n) ? n + 1 : '?') + '</span>' +
        (x.provider ? '<span class="mono">' + esc(x.provider) + '</span>' : '') +
        '<span class="conn-imp-err">' + esc(x.error || '') + '</span></li>';
    }).join('') + '</ul>';
  }
  box.innerHTML = html;
}

async function submitConnBatch(btn) {
  if (_connBatchBusy) return; // 防重复提交
  const ta = _('connBatchInput');
  const resBox = _('connBatchResult');
  const raw = ta ? ta.value.trim() : '';
  if (!raw) {
    if (resBox) resBox.innerHTML = '<div class="save-result err">' + uiIcon('close') +
      '<span>' + esc(t('请先粘贴数据或选择文件')) + '</span></div>';
    toast(t('请先粘贴数据或选择文件'), 'error');
    return;
  }
  const fallback = connImpResolvedProvider('connBatchProvider', 'connBatchProviderCustom');
  const parsed = parseConnBatch(raw, fallback);
  if (!parsed.items.length) {
    connBatchPreview();
    if (resBox) resBox.innerHTML = '<div class="save-result err">' + uiIcon('close') +
      '<span>' + esc(t('没有可导入的条目：请检查数据格式')) + '</span></div>';
    toast(t('没有可导入的条目：请检查数据格式'), 'error');
    return;
  }
  _connBatchBusy = true;
  const gen = _connBatchGen;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('导入中...'); }
  if (resBox) resBox.innerHTML = '';
  try {
    const d = await api('POST', '/connections/import-batch', { items: parsed.items });
    if (gen !== _connBatchGen) return; // 弹窗已关闭，丢弃迟到的响应
    const r = (d && d.data) || {};
    const imported = Number(r.imported) || 0;
    const failed = Number(r.failed) || 0;
    connBatchRenderResult(r);
    toast(t('导入完成') + ' · ' + t('成功 ') + imported + t(' · 失败 ') + failed,
      failed ? (imported ? 'info' : 'error') : 'success');
    if (imported > 0) loadConnections();
    if (!failed) {
      if (ta) ta.value = '';
      const nameEl = _('connBatchFileName'); if (nameEl) nameEl.textContent = '';
      const file = _('connBatchFile'); if (file) file.value = '';
      connBatchPreview();
    }
  } catch (e) {
    if (gen === _connBatchGen && resBox) resBox.innerHTML = '<div class="save-result err">' + uiIcon('close') +
      '<span>' + esc(t('导入失败: ') + e.message) + '</span></div>';
    if (gen === _connBatchGen) toast(t('导入失败: ') + e.message, 'error');
  }
  finally {
    _connBatchBusy = false;
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

async function testConnection(id, cell) {
  const out = cell && cell.closest ? cell.closest('td').querySelector('.conn-row-test-out') : null;
  const orig = cell ? cell.innerHTML : '';
  if (cell) { cell.disabled = true; cell.innerHTML = '<span class="loading"></span> ' + t('测试中...'); }
  if (out) out.innerHTML = '';
  try {
    const d = await api('POST', '/connections/test', { id });
    const r = d.data || {};
    if (out) out.innerHTML = renderTestResultHTML(r.ok, r.ok ? (t('可用') + ' · HTTP ' + r.status) : (r.error || t('失败')));
    loadConnections();
  } catch (e) {
    if (out) out.innerHTML = renderTestResultHTML(false, friendlyTestError(e));
  } finally {
    if (cell) { cell.disabled = false; cell.innerHTML = orig; }
  }
}

async function deleteConnection(id) {
  if (!(await showConfirm(t('确定删除该连接？绑定它的 Provider 将不再注入 token。')))) return;
  try {
    await api('POST', '/connections/delete', { id });
    toast(t('已删除'), 'success');
    loadConnections();
  } catch (e) { toast(t('删除失败: ') + e.message, 'error'); }
}

// Wave-4: connection pool picker in the provider drawer (checkbox list grouped
// by provider service). Replaces the old single-select provConnection dropdown.
let _provConnSel = [];

function initConnPick(ids) { _provConnSel = (ids || []).slice(); renderConnPick(); }

function toggleConnPick(id, on) {
  const i = _provConnSel.indexOf(id);
  if (on && i < 0) _provConnSel.push(id);
  if (!on && i >= 0) _provConnSel.splice(i, 1);
  const cnt = _('provConnCount');
  if (cnt) cnt.textContent = ' · ' + (_provConnSel.length ? _provConnSel.length + t('已选') : t('未选择'));
  refreshSaveBar(); // 委托监听先于本函数执行，需在此补一次脏检查
}

function renderConnPick() {
  const box = _('provConnPick');
  if (!box) return;
  const cnt = _('provConnCount');
  if (cnt) cnt.textContent = ' · ' + (_provConnSel.length ? _provConnSel.length + t('已选') : t('未选择'));
  if (!_connList.length) {
    box.innerHTML = '<span class="conn-pick-empty">' + t('暂无登录连接') + ' · <a href="javascript:void(0)" onclick="showConnectionImport()" class="lnk-a">' + t('导入 Token') + '</a></span>';
    return;
  }
  const byProv = {};
  _connList.forEach(c => { (byProv[c.provider] = byProv[c.provider] || []).push(c); });
  box.innerHTML = Object.keys(byProv).sort().map(prov =>
    '<div class="conn-pick-group">' + esc(flowLabel(prov)) + '</div>' +
    byProv[prov].map(c => {
      const on = _provConnSel.includes(c.id);
      const who = c.email || (c.providerSpecificData && c.providerSpecificData.orgId ? 'org ' + c.providerSpecificData.orgId : c.id);
      return '<label class="conn-pick-item' + (on ? ' on' : '') + '">' +
        '<input type="checkbox"' + (on ? ' checked' : '') + ' onchange="toggleConnPick(\'' + escAttr(c.id) + '\', this.checked)"> ' +
        '<span class="mono note-sm">' + esc(who) + '</span>' +
        (c.status !== 'active' ? ' <span class="conn-pick-warn">' + t('需重新登录') + '</span>' : '') +
        (c.usageCount ? ' <span class="conn-pick-usage">' + formatNumber(c.usageCount) + ' ' + t('请求') + '</span>' : '') +
        '</label>';
    }).join('')).join('');
}

async function saveHeaders(btn) {
  const tbody = _('headersTableBody');
  const rows = tbody.querySelectorAll('tr');
  const headers = {};
  let hasEmpty = false;
  rows.forEach(tr => {
    const keyInput = tr.querySelector('.header-key');
    const valInput = tr.querySelector('.header-val');
    if (keyInput && valInput) {
      const k = keyInput.value.trim();
      const v = valInput.value.trim();
      if (k) { headers[k] = v; }
      else if (v) { hasEmpty = true; }
    }
  });
  if (hasEmpty) { toast(t('存在有值无键的行，已忽略'), 'info'); }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    const d = await api('POST', '/config/update', { headers });
    toast(t('请求头已保存'), 'success');
    showSaveResult('headerSaveResult', true, t('已保存 ') + Object.keys(d.data.headers).length + t(' 个请求头'));
    loadConfig();
  } catch (e) { toast(t('保存失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}


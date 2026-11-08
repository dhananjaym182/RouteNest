// ========== 自定义 Provider ==========
let _editingProviderId = '';
let _provEditorRevision = 0;
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
    observeProviderSyncDelistings(list, _provSync);
    if (list.some(p => (_provSync[p.id] || {}).delistedMarked)) await checkModelDelistings();
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
  if (cool) html += ' <button type="button" class="chip warn cooldown-chip" onclick="event.stopPropagation();providersResetCooldown(\'' + escAttr(p.id) + '\', \'\', this)" title="' + escAttr(t('点击清除该 Provider 全部冷却')) + '">' + uiIcon('hourglass') + ' ' + cool + ' · ' + fmtLeft(h.recoveryAt) + '</button>';
  return html;
}

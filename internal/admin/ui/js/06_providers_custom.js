// ========== 自定义 Provider ==========
let _editingProviderId = '';
let _provEditorRevision = 0;
// 从免费类预设新建并保存后，自动触发一次分级导入预览（用户勾选后才写入）
let _previewAfterSave = false;

let _provLoadGeneration = 0;
// 行内测试/检测结果跨 re-render 保留（loadProviders 重绘后回填到结果位）
const _provRowResults = {};
function provRowResultHTML(kind, id) {
  const r = _provRowResults[kind + ':' + id];
  return r ? renderTestResultHTML(r.ok, r.text) : '';
}

// Loads the managed-provider data (health, sync state, cooldowns) and hands it to the one
// provider table (renderGlobalInstances), which merges it with the instance/offering data.
async function loadProviders() {
  const generation = ++_provLoadGeneration;
  try {
    const [d, sd] = await Promise.all([
      api('GET', '/providers'),
      api('GET', '/providers/sync-status').catch(() => ({ data: {} })),
    ]);
    if (generation !== _provLoadGeneration) return;
    _provSync = (sd && sd.data && sd.data.providers) || {};
    _provHealth = (d.data && d.data.health) || {};
    const list = d.data.providers || [];
    const ids = new Set(list.map(p => p.id));
    Object.keys(_provCache).forEach(id => { if (!ids.has(id)) delete _provCache[id]; });
    list.forEach(p => { _provCache[p.id] = p; });
    renderProviderSyncBanner(list);
    renderProviderCooldowns(list);
    refreshProvDetailIfOpen();
    if (typeof refreshProviderInventory === 'function') refreshProviderInventory();
    observeProviderSyncDelistings(list, _provSync);
    if (list.some(p => (_provSync[p.id] || {}).delistedMarked)) await checkModelDelistings();
  } catch (e) {
    if (generation !== _provLoadGeneration) return;
    const box = _('providerCooldowns');
    if (box) box.innerHTML = '<div class="sync-banner on sync-banner-warn"><span class="hint-mut">' + t('加载失败') + ': ' + esc(e.message) + '</span>' +
      '<span class="row-bulk"><button type="button" class="btn btn-sm" onclick="loadProviders()">' + uiIcon('reset') + t('重试') + '</button></span></div>';
  }
}

function renderProviderCooldowns(list) {
  const box = _('providerCooldowns');
  if (box) box.innerHTML = renderProvCooldownBar(list);
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
  // The server reports rate 0 for a provider with no traffic, so a rate only counts once samples exist.
  const samples = Number(h.samples) || 0;
  let tone = 'ok', label = '正常';
  if (cool) { tone = 'bad'; label = '冷却中'; }
  else if (!st.lastCheckedMs && st.lastError) { tone = 'bad'; label = '检测出错'; }
  else if (daily > 0 && dailyUsed / daily >= 0.7 || rpm > 0 && rpmUsed / rpm >= 0.7 || tok > 0 && tokUsed / tok >= 0.7) { tone = 'warn'; label = '接近上限'; }
  else if (samples > 0 && Number(h.rate) < 0.5) { tone = 'warn'; label = '成功率偏低'; }
  else if (samples === 0) { tone = 'idle'; label = '暂无样本'; }
  let html = '<span class="pmini tone-' + tone + '"><span class="pmini-dot" aria-hidden="true"></span>' + t(label) + '</span>';
  if (rpm > 0) html += ' <span class="pmini-rpm mono" title="' + escAttr(t('每分钟已用 ') + rpmUsed + '/' + rpm) + '">RPM ' + rpmUsed + '/' + rpm + '</span>';
  if (cool) html += ' <button type="button" class="chip warn cooldown-chip" onclick="event.stopPropagation();providersResetCooldown(\'' + escAttr(p.id) + '\', \'\', this)" title="' + escAttr(t('点击清除该 Provider 全部冷却')) + '">' + uiIcon('hourglass') + ' ' + cool + ' · ' + fmtLeft(h.recoveryAt) + '</button>';
  return html;
}

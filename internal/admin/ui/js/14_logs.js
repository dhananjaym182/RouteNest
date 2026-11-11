// ========== 请求日志 ==========
let logCursor = '';
let logHasMore = false;
let _logRowSeq = 0;
let _logItems = [];       // 已加载日志（新→旧），筛选/渲染都基于它
let _logIds = new Set();  // 已知 ID 集合，跟随轮询去重用
let _logFilter = 'all';   // all|ok|fail|stream|combo|fallback
let _logSearch = '';
let _logTimeHours = 0;
let _logProvider = '';
let _logModel = '';
let _logPending = [];     // 暂停跟随时缓冲的新日志
// 跟随新日志为客户端实时视图开关：默认开，用户手动停用后持久化（跨标签页/刷新保留）
const LOG_FOLLOW_KEY = 'cline_admin_log_follow';
let _logFollow = false;
try { _logFollow = localStorage.getItem(LOG_FOLLOW_KEY) !== '0'; } catch (e) { _logFollow = true; }
let _logPollTimer = 0;
let _logDetailIdx = -1;
let _logSearchT = 0;
let _logDetailMap = {};   // log id -> 展开行 DOM id（避免把 UI 状态写进日志对象）
let _logOpenIds = new Set(); // 已展开详情的日志 id（跨轮询重渲染保持展开状态）

const formatDuration = ms => {
  if (!ms || ms <= 0) return '-';
  if (ms < 1000) return ms + 'ms';
  return (ms / 1000).toFixed(1) + 's';
};
const formatTPS = v => (!v || v <= 0) ? '-' : v.toFixed(1);

// 尝试链路结果标签（成功/失败/跳过）
function attemptOutcomeLabel(o) {
  if (o === 'success') return t('成功');
  if (o === 'skipped') return t('跳过');
  return t('失败');
}

// 上游展示名：自定义 provider 上游时用后端回传的 providerName 显示真实名称，
// 其次回落到 attempt.provider，最后才是通用 upstream 文案。旧日志条目没有
// providerName/provider，仍显示 cline/opencode 等原值。
function attemptUpstreamLabel(a) {
  if (a.providerName) return a.upstream === 'provider' ? 'Provider › ' + a.providerName : a.providerName;
  if (a.provider) return a.provider;
  return a.upstream || '';
}

// 表格「账号」列 / 卡片标题：账号邮箱优先；无邮箱时展示上游（provider 上游带真实名称）
function logUpstreamValue(l) {
  if (l.accountEmail) return l.accountEmail;
  return attemptUpstreamLabel(l) || '-';
}

// 尝试链路渲染：顺序 / 模型 / Provider 或上游 / HTTP 状态 / 耗时 / 结果 / 原因。
// 只展示 AttemptLog 实际存在的字段；没有 account 邮箱与 TTFT，绝不臆造。
function renderAttempts(attempts) {
  return '<ol class="log-attempts">' + attempts.map((a, i) => {
    const outcome = a.outcome || 'failed';
    const oc = (outcome === 'success' || outcome === 'failed' || outcome === 'skipped') ? outcome : 'failed';
    const order = (typeof a.order === 'number' && a.order > 0) ? a.order : (i + 1);
    const meta = [];
    const upLabel = attemptUpstreamLabel(a);
    if (upLabel) meta.push(esc(upLabel));
    if (a.status) meta.push('HTTP ' + esc(String(a.status)));
    if (typeof a.durationMs === 'number' && a.durationMs > 0) meta.push(formatDuration(a.durationMs));
    return '<li>' +
      '<span class="combo-entry-idx">' + order + '</span>' +
      '<span class="attempt-model">' + esc(a.model || '-') + '</span>' +
      '<span class="attempt-outcome ' + oc + '">' + esc(attemptOutcomeLabel(outcome)) + '</span>' +
      (meta.length ? '<span class="attempt-meta">' + meta.join(' · ') + '</span>' : '') +
      (a.reason ? '<span class="attempt-reason">' + esc(a.reason) + '</span>' : '') +
    '</li>';
  }).join('') + '</ol>';
}

// 详情弹窗分区：语义 section + 标题，便于桌面右侧抽屉纵向阅读。
function logDetailSection(title, body) {
  return '<section class="log-detail-section">' +
    '<h4 class="log-detail-section-title">' + esc(title) + '</h4>' + body +
  '</section>';
}

// 详情键值网格：沿用 .log-detail-grid 的既有阅读密度。
function logDetailGrid(rows) {
  return '<div class="log-detail-grid">' + rows.map(r =>
    '<span class="k">' + esc(r[0]) + '</span><span class="v">' + esc(String(r[1])) + '</span>').join('') + '</div>';
}

// 移动端行内 HTTP 状态：只取 attempt 上真实存在的 status（优先最后一次尝试）。
// 没有 attempt 状态时由调用方回落为完成/失败，不伪造状态码。
function logAttemptStatus(attempts) {
  for (let i = attempts.length - 1; i >= 0; i--) {
    if (attempts[i].status) return attempts[i].status;
  }
  return 0;
}

// 日志模型单元格：服务模型 + 组合徽章 + 回退计数（requestedModel 与服务模型不同时一并显示）
function logModelCell(l) {
  let html = '<span class="mono hint-xs">' + esc(l.model || '-') + '</span>';
  if (l.requestedModel && l.requestedModel !== l.model) {
    html += '<span class="log-req-model">' + t('请求: ') + esc(l.requestedModel) + '</span>';
  }
  if (l.comboId) {
    html += '<span class="log-combo-badge">' + t('组合') + ': ' + esc(l.comboName || l.comboId) + '</span>';
  }
  if (l.fallbackUsed) {
    const n = Number(l.fallbackCount) || 0;
    html += '<span class="log-fallback-badge">' + t('回退') + (n > 0 ? ' ' + n : '') + '</span>';
  }
  return '<div class="log-model-cell">' + html + '</div>';
}

// 表格行展开/收起尝试链路详情行
// 用 class 驱动显隐（.log-detail.open），而不是翻转内联 style.display：
// 旧实现 `style.display === 'none' || !style.display` 在展开态（display=''，空串为
// falsy）会被误判回「未设置」分支再次置 ''，导致行一旦展开就无法收起。
// 状态以 _logOpenIds 为准，重渲染（跟随轮询/筛选）也能恢复展开态与 chevron。
function setLogDetailOpen(l, open) {
  if (!l) return;
  const el = _( _logDetailMap[l.id] );
  if (!el) return;
  el.classList.toggle('open', open);
  if (l.id) { open ? _logOpenIds.add(l.id) : _logOpenIds.delete(l.id); }
  const row = el.previousElementSibling;
  if (row && row.classList.contains('log-row')) {
    row.classList.toggle('expanded', open);
    if (open) row.setAttribute('aria-expanded', 'true');
    else row.setAttribute('aria-expanded', 'false');
  }
}

function toggleLogDetailByItem(l) {
  if (!l || !(Array.isArray(l.attempts) && l.attempts.length)) return;
  const el = _( _logDetailMap[l.id] );
  setLogDetailOpen(l, !(el && el.classList.contains('open')));
}

// ---------- 筛选 ----------
// 快捷筛选 chip：与全局 .chip-btn 体系统一，dot 提供成败/流式等色调提示
const LOG_FILTERS = [
  { key: 'all', label: '全部', tone: '', fn: () => true },
  { key: 'ok', label: '成功', tone: 'green', fn: l => l.completed && !l.error },
  { key: 'fail', label: '失败', tone: 'red', fn: l => !l.completed || !!l.error },
  { key: 'stream', label: '流式', tone: 'cyan', fn: l => !!l.stream },
  { key: 'combo', label: '组合', tone: 'purple', fn: l => !!l.comboId },
  { key: 'fallback', label: '回退', tone: 'yellow', fn: l => !!l.fallbackUsed }
];

function logMatches(l) {
  const f = LOG_FILTERS.find(x => x.key === _logFilter) || LOG_FILTERS[0];
  if (!f.fn(l)) return false;
  const sel = _('logFilterProtocol');
  if (sel && sel.value && (l.protocol || '') !== sel.value) return false;
  if (_logProvider && logProviderKey(l) !== _logProvider) return false;
  if (_logModel && (l.model || '') !== _logModel && (l.requestedModel || '') !== _logModel) return false;
  if (_logTimeHours > 0) {
    const ts = new Date(l.startedAt).getTime();
    if (!ts || ts < Date.now() - _logTimeHours * 3600000) return false;
  }
  if (_logSearch) {
    const q = _logSearch.toLowerCase();
    const hay = [l.model, l.requestedModel, l.accountEmail, l.connectionEmail, l.providerName, l.id,
      (l.comboName || ''), (l.error || '')].filter(Boolean).join(' ').toLowerCase();
    if (hay.indexOf(q) === -1) return false;
  }
  return true;
}

function logProviderKey(l) {
  return l.providerName || l.upstream || (l.accountEmail ? 'cline' : 'unknown');
}

function updateLogChips() {
  const box = _('logFilterChips');
  if (!box) return;
  box.innerHTML = LOG_FILTERS.map(f => {
    const n = _logItems.filter(f.fn).length;
  // 筛选 chip：确定要性/计数由 .chip-btn 统一样式提供；dot 提示色调
    return '<button type="button" class="chip chip-btn log-chip' + (_logFilter === f.key ? ' active' : '') + '" data-filter="' + f.key + '" onclick="setLogFilter(\'' + f.key + '\')">' +
      (f.tone ? '<i class="dot ' + f.tone + '"></i>' : '') + t(f.label) + ' <b>' + n + '</b></button>';
  }).join('');
  // 协议下拉：用已加载数据补充选项，保留当前选择
  const sel = _('logFilterProtocol');
  if (sel) {
    const protos = [...new Set(_logItems.map(l => l.protocol).filter(Boolean))].sort();
    const cur = sel.value;
    sel.innerHTML = '<option value="">' + t('全部协议') + '</option>' +
      protos.map(p => '<option value="' + escAttr(p) + '"' + (p === cur ? ' selected' : '') + '>' + esc(p) + '</option>').join('');
  }
  const providerSel = _('logFilterProvider');
  if (providerSel) {
    const providers = [...new Set(_logItems.map(logProviderKey).filter(Boolean))].sort();
    providerSel.innerHTML = '<option value="">' + t('全部 Provider') + '</option>' + providers.map(p => '<option value="' + escAttr(p) + '"' + (p === _logProvider ? ' selected' : '') + '>' + esc(p) + '</option>').join('');
  }
  const modelSel = _('logFilterModel');
  if (modelSel) {
    const models = [...new Set(_logItems.flatMap(l => [l.model, l.requestedModel]).filter(Boolean))].sort();
    modelSel.innerHTML = '<option value="">' + t('全部模型') + '</option>' + models.map(m => '<option value="' + escAttr(m) + '"' + (m === _logModel ? ' selected' : '') + '>' + esc(m) + '</option>').join('');
  }
}

function setLogFilter(key) {
  _logFilter = key;
  updateLogChips();
  renderLogList();
}

function onLogProtoFilterChange() { renderLogList(); }

function onLogAdvancedFilterChange() {
  _logTimeHours = Number((_('logFilterTime') && _('logFilterTime').value) || 0) || 0;
  _logProvider = (_('logFilterProvider') && _('logFilterProvider').value) || '';
  _logModel = (_('logFilterModel') && _('logFilterModel').value) || '';
  renderLogList();
}

function onLogSearchInput() {
  clearTimeout(_logSearchT);
  _logSearchT = setTimeout(() => {
    _logSearch = (_('logSearch').value || '').trim();
    renderLogList();
  }, 200);
}

// ---------- 渲染 ----------
function renderLogList() {
  const tbody = _('logTableBody');
  const cards = _('logCards');
  if (!tbody || !cards) return;
  const shown = _logItems.filter(logMatches);
  if (!shown.length) {
    const msg = _logItems.length ? t('暂无匹配日志') : t('暂无请求日志');
    tbody.innerHTML = '<tr><td colspan="13" class="empty">' + msg + '</td></tr>';
    cards.innerHTML = _logItems.length ? '' : '<div class="empty">' + t('暂无请求日志') + '</div>';
    _('logLoadMore').style.display = (_logItems.length && logHasMore) ? 'block' : 'none';
    return;
  }

  const renderRow = (l, idx) => {
    const st = l.completed
      ? '<span class="log-status ok">' + t('完成') + '</span>'
      : '<span class="log-status fail" title="' + escAttr(tx(l.error || '')) + '">' + t('失败') + '</span>';
    const tk = l.usageAvailable
      ? formatTokenCount(l.inputTokens) + '</td><td>' + formatTokenCount(l.outputTokens) + '</td><td>' + formatTokenCount(l.cachedTokens) + '</td><td>' + formatTokenCount(l.totalTokens)
      : '-</td><td>-</td><td>-</td><td>-';
    const attempts = Array.isArray(l.attempts) ? l.attempts : [];
    const hasAttempts = attempts.length > 0;
    const detailId = 'logDetail' + (_logRowSeq++);
    _logDetailMap[l.id] = detailId;
    const open = hasAttempts && _logOpenIds.has(l.id);
    const rowCls = 'log-row' + (hasAttempts ? ' has-attempts' : '') + (open ? ' expanded' : '') + (idx % 2 ? ' alt' : '');
    const acct = esc(logUpstreamValue(l));
    const rowTitle = hasAttempts ? ' title="' + escAttr(t('点击查看尝试链路')) + ' · ' + t('双击复制 JSON') + '"' : ' title="' + escAttr(t('双击复制 JSON')) + '"';
    return '<tr class="' + rowCls + '" data-idx="' + idx + '"' + rowTitle + (hasAttempts ? ' aria-expanded="' + (open ? 'true' : 'false') + '"' : '') + '>' +
      '<td>' + logTimestampHtml(l.startedAt) + '</td>' +
      '<td title="' + escAttr(logUpstreamValue(l)) + '">' + acct + '</td>' +
      '<td>' + esc(l.protocol || '-') + '</td>' +
      '<td>' + logModelCell(l) + '</td>' +
      '<td>' + tk + '</td>' +
      '<td>' + formatDuration(l.durationMs) + '</td>' +
      '<td>' + (l.ttftMs ? formatDuration(l.ttftMs) : '-') + '</td>' +
      '<td>' + formatTPS(l.outputTokensPerSecond) + '</td>' +
      '<td>' + st + '</td>' +
      '<td>' + (hasAttempts ? '<i class="log-chevron" aria-hidden="true"></i>' : '') + '<button type="button" class="btn btn-sm log-detail-btn" data-idx="' + idx + '">' + t('详情') + '</button></td></tr>' +
      (hasAttempts ? '<tr id="' + detailId + '" class="log-detail' + (open ? ' open' : '') + '"><td colspan="13" class="log-detail-cell">' + renderAttempts(attempts) + '</td></tr>' : '');
  };

  // 移动端 ≤760px：每条请求压成两行（模型 + 状态 / 账号·上游 + HTTP或结果 + 耗时 + 时间）。
  // 完整信息与尝试链路统一进入全屏详情抽屉，不再渲染高卡片与 2x2 指标网格。
  const renderCard = (l, idx) => {
    const attempts = Array.isArray(l.attempts) ? l.attempts : [];
    const st = l.completed
      ? '<span class="log-status ok">' + t('完成') + '</span>'
      : '<span class="log-status fail">' + t('失败') + '</span>';
    const model = l.requestedModel || l.model || '-';
    const status = logAttemptStatus(attempts);
    const result = status ? 'HTTP ' + esc(String(status)) : (l.completed ? t('完成') : t('失败'));
    const when = l.startedAt ? new Date(l.startedAt).toLocaleTimeString(LC()) : '-';
    const titleBits = [l.model, l.requestedModel, logUpstreamValue(l), when].filter(Boolean).join(' · ');
    return '<article class="log-card log-row-mobile" data-idx="' + idx + '" title="' + escAttr(titleBits) + '">' +
      '<div class="log-row-mobile-top">' +
        '<span class="log-row-mobile-model mono">' + esc(model) + '</span>' +
        st +
      '</div>' +
      '<div class="log-row-mobile-meta">' +
        '<span class="log-row-mobile-provider">' + esc(logUpstreamValue(l)) + '</span>' +
        '<span class="log-row-mobile-sep" aria-hidden="true">·</span>' +
        '<span class="log-row-mobile-result">' + result + '</span>' +
        '<span class="log-row-mobile-sep" aria-hidden="true">·</span>' +
        '<span class="log-row-mobile-latency">' + formatDuration(l.durationMs) + '</span>' +
        '<span class="log-row-mobile-sep" aria-hidden="true">·</span>' +
        '<span class="log-row-mobile-time">' + esc(when) + '</span>' +
      '</div>' +
    '</article>';
  };

  const pairs = [];
  for (let i = 0; i < _logItems.length; i++) { if (logMatches(_logItems[i])) pairs.push([_logItems[i], i]); }
  tbody.innerHTML = pairs.map(p => renderRow(p[0], p[1])).join('');
  cards.innerHTML = pairs.map(p => renderCard(p[0], p[1])).join('');
  _('logLoadMore').style.display = logHasMore ? 'block' : 'none';
}

function ingestLogItems(items, reset) {
  if (reset) {
    _logItems = items.slice();
    _logIds = new Set(_logItems.map(l => l.id));
    _logRowSeq = 0;
  } else {
    items.forEach(l => { if (!_logIds.has(l.id)) { _logItems.push(l); _logIds.add(l.id); } });
  }
}

async function loadRequestLogs(reset) {
  if (reset) { logCursor = ''; _logRowSeq = 0; _logPending = []; hideLogResumeBar(); }
  const cursor = logCursor;
  try {
    const path = '/request-logs?limit=50' + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '');
    const d = await api('GET', path);
    const page = d.data;
    const items = page.items || [];
    logHasMore = !!page.hasMore;
    logCursor = page.nextCursor || '';
    ingestLogItems(items, reset);
    updateLogChips();
    renderLogList();
  } catch (e) { toast(t('加载日志失败: ') + e.message, 'error'); }
}

// ---------- 跟随新日志 ----------
// 页面滚动发生在 .main（overflow-y:auto），不是 documentElement；实时端判定
// 必须相对视口内最新一行的位置：最新一行被滚出视口 280px 以上才算「翻阅历史」，
// 否则（含刚点过「恢复跟随」回到实时端时）都在实时端，resume 条不出现。
function newestLogEl() {
  const row = document.querySelector('#logTableBody tr.log-row');
  if (row && row.getClientRects().length) return row;
  const card = document.querySelector('#logCards .log-card');
  if (card && card.getClientRects().length) return card;
  return row || card || null;
}
// 280px 缓冲最高容纳 sticky 表头 + 工具栏高度，轻微滚动不会被误判为离端
function logAtLiveEdge() {
  const first = newestLogEl();
  if (!first) return true;
  const rect = first.getBoundingClientRect();
  if (!rect.height) return true;
  return rect.top >= -280;
}

function hideLogResumeBar() {
  const bar = _('logResumeBar');
  if (bar) bar.hidden = true;
}

function startLogPolling() {
  refreshLogFollowBtn();
  if (_logPollTimer) return;
  _logPollTimer = setInterval(logPollTick, 8000);
}

function stopLogPolling() {
  if (_logPollTimer) { clearInterval(_logPollTimer); _logPollTimer = 0; }
}

// 跟随按钮视觉与 _logFollow 持久态对齐（进入日志页时刷新，默认开/手动停后保持）
function refreshLogFollowBtn() {
  const btn = _('logFollowBtn');
  if (!btn) return;
  btn.classList.toggle('btn-primary', _logFollow);
  btn.textContent = _logFollow ? t('跟随新日志') : t('已暂停跟随');
  btn.title = _logFollow ? t('自动跟随新日志') : t('点击恢复自动跟随');
}

// 恢复跟随 = 把最新一行带回视口顶部（不是跳页面顶）：回到实时端后下一次轮询
// 才会自动置顶新日志，而不是再次弹出 resume 条
function scrollLogToLiveEdge() {
  const first = newestLogEl();
  if (first && first.scrollIntoView) { first.scrollIntoView({ block: 'start' }); return; }
  window.scrollTo(0, 0);
}

async function logPollTick() {
  if (document.visibilityState !== 'visible' || !_logFollow) return;
  if (typeof _activeTabName === 'undefined' || _activeTabName !== 'logs') return;
  try {
    const d = await api('GET', '/request-logs?limit=50');
    const fresh = (d.data.items || []).filter(l => l.id && !_logIds.has(l.id));
    if (!fresh.length) return;
    if (!logAtLiveEdge()) {
      // 用户向下翻阅历史：新日志缓冲不打断，显示"恢复跟随 (N 新)"
      fresh.forEach(l => { if (!_logPending.some(p => p.id === l.id)) _logPending.unshift(l); });
      _logPending.sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt));
      const bar = _('logResumeBar'), txt = _('logResumeText');
      if (bar) bar.hidden = false;
      if (txt) txt.textContent = t('已暂停跟随 · ') + _logPending.length + t(' 条新日志');
      return;
    }
    fresh.forEach(l => { _logItems.unshift(l); _logIds.add(l.id); });
    updateLogChips();
    renderLogList();
  } catch (e) { /* 静默：轮询失败不打扰 */ }
}

function resumeLogFollow() {
  if (_logPending.length) {
    // _logPending 为新→旧排序；unshift 会把先处理的推到更前，所以必须从最旧
    // 的一端开始合并，否则缓冲的新日志在列表里是新旧行交错反序。
    for (let i = _logPending.length - 1; i >= 0; i--) {
      const l = _logPending[i];
      if (!_logIds.has(l.id)) { _logItems.unshift(l); _logIds.add(l.id); }
    }
    _logPending = [];
  }
  hideLogResumeBar();
  updateLogChips();
  renderLogList();
  scrollLogToLiveEdge(); // 回到顶部实时端，避免下一次轮询再次弹出 resume 条
}

function toggleLogFollow(btn) {
  _logFollow = !_logFollow;
  try { localStorage.setItem(LOG_FOLLOW_KEY, _logFollow ? '1' : '0'); } catch (e) {}
  refreshLogFollowBtn();
  if (_logFollow && _logPending.length) resumeLogFollow();
}

// ---------- 复制 / 详情弹窗 ----------
function copyLogJson(idx) {
  const l = _logItems[idx];
  if (!l) return;
  copyText(JSON.stringify(l, null, 2));
}

function openLogDetail(idx) {
  const l = _logItems[idx];
  if (!l) return;
  _logDetailIdx = idx;
  const modal = _('logDetailModal');
  if (!modal) return;
  _('logDetailId').textContent = l.id || '';
  const bits = [];
  if (l.startedAt) bits.push(new Date(l.startedAt).toLocaleString(LC()));
  if (l.protocol) bits.push(l.protocol);
  if (l.stream) bits.push(t('流式'));
  if (l.model) bits.push(l.model);
  _('logDetailSub').textContent = bits.join(' · ');

  // 1) 摘要：请求身份 / 路由归属 / 结果，全部来自已加载记录。
  const summaryRows = [
    ['ID', l.id || '-'],
    [t('时间'), l.startedAt ? new Date(l.startedAt).toLocaleString(LC()) : '-'],
    [t('协议'), l.protocol || '-'],
    [t('流式'), l.stream ? t('开启') : t('关闭')],
    [t('模型'), l.model || '-']
  ];
  if (l.requestedModel) summaryRows.push([t('请求模型'), l.requestedModel]);
  if (l.comboId) summaryRows.push([t('组合'), l.comboName || l.comboId]);
  const fallbackCount = Number(l.fallbackCount) || 0;
  summaryRows.push([t('回退'), l.fallbackUsed
    ? t('已使用') + (fallbackCount > 0 ? ' × ' + fallbackCount : '')
    : t('关闭')]);
  let accountProvider = logUpstreamValue(l);
  if (l.accountEmail && l.providerName) accountProvider += ' · ' + l.providerName;
  summaryRows.push([t('账号'), accountProvider]);
  summaryRows.push([t('状态'), l.completed && !l.error ? t('完成') : t('失败')]);

  let html = logDetailSection(t('摘要'), logDetailGrid(summaryRows));

  // 2) 路由尝试：仅 attempts 存在时渲染，字段来自 AttemptLog。
  const attempts = Array.isArray(l.attempts) ? l.attempts : [];
  if (attempts.length) {
    html += logDetailSection(t('尝试链路'), renderAttempts(attempts));
  }

  // 3) 错误 / 响应：有 error 就展示；完成且无错误时明确说明未记录错误。
  const errorBody = l.error
    ? '<div class="log-detail-error">' + esc(tx(l.error)) + '</div>'
    : '<div class="log-detail-note">' + t('未记录错误') + '</div>';
  html += logDetailSection(t('错误 / 响应'), errorBody);

  // 4) 用量：usageAvailable 为假时 token 显示未知；TTFT/耗时等仍按实际值展示。
  const usageRows = [
    [t('输入'), l.usageAvailable ? formatTokenCount(l.inputTokens) : t('未知')],
    [t('输出'), l.usageAvailable ? formatTokenCount(l.outputTokens) : t('未知')],
    [t('缓存'), l.usageAvailable ? formatTokenCount(l.cachedTokens) : t('未知')],
    [t('总 Token'), l.usageAvailable ? formatTokenCount(l.totalTokens) : t('未知')],
    ['TTFT', l.ttftMs ? formatDuration(l.ttftMs) : '-'],
    ['tok/s', formatTPS(l.outputTokensPerSecond)],
    [t('耗时'), formatDuration(l.durationMs)]
  ];
  html += logDetailSection(t('用量'), logDetailGrid(usageRows));

  // 5) 原始 JSON：保持既有 pre 块与复制 JSON 按钮。
  html += logDetailSection(t('原始 JSON'), '<pre class="log-detail-json">' + esc(JSON.stringify(l, null, 2)) + '</pre>');

  _('logDetailBody').innerHTML = html;
  modal.style.display = 'flex';
}

function closeLogDetail() {
  const modal = _('logDetailModal');
  if (modal) modal.style.display = 'none';
  _logDetailIdx = -1;
}

function copyLogDetailJson(btn) {
  if (_logDetailIdx < 0) return;
  copyLogJson(_logDetailIdx);
}

// ---------- 下载 / 清空 / 全屏 ----------
function downloadLogs(btn) {
  const shown = _logItems.filter(logMatches);
  if (!shown.length) { toast(t('当前无可下载日志'), 'error'); return; }
  if (btn) btn.disabled = true;
  try {
    downloadLogsTSV(shown, 'routenest-logs.txt', btn);
  } finally { if (btn) btn.disabled = false; }
}

// 仅失败请求（与行内「失败」判定一致），不受当前筛选影响
function downloadErrorLogs(btn) {
  const failed = _logItems.filter(l => !(l.completed && !l.error));
  if (!failed.length) { toast(t('当前无失败日志'), 'success'); return; }
  if (btn) btn.disabled = true;
  try {
    downloadLogsTSV(failed, 'routenest-logs-errors.txt', btn);
  } finally { if (btn) btn.disabled = false; }
}

function downloadLogsTSV(list, filename, btn) {
  const orig = btn ? btn.innerHTML : '';
  try {
    const lines = [
      'time\tstatus\tprotocol\tmodel\taccount\tinput\toutput\tcached\ttotal\tdurationMs\tttftMs\ttok/s\terror\tid'
    ];
    list.forEach(l => {
      lines.push([
        l.startedAt, (l.completed && !l.error) ? 'ok' : 'fail', l.protocol || '', l.model || '', logUpstreamValue(l),
        l.usageAvailable ? l.inputTokens : '-', l.usageAvailable ? l.outputTokens : '-',
        l.usageAvailable ? l.cachedTokens : '-', l.usageAvailable ? l.totalTokens : '-',
        l.durationMs || 0, l.ttftMs || 0, l.outputTokensPerSecond || 0,
        (l.error || '').replace(/[\t\n\r]/g, ' '), l.id || ''
      ].join('\t'));
    });
    const blob = new Blob([lines.join('\n')], { type: 'text/plain;charset=utf-8' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    document.body.appendChild(a); a.click(); document.body.removeChild(a);
    setTimeout(() => URL.revokeObjectURL(a.href), 4000);
    toast(t('已下载 ') + list.length + t(' 条日志'), 'success');
  } catch (e) { toast(t('下载失败: ') + e.message, 'error'); }
  if (btn) btn.innerHTML = orig;
}

async function clearRequestLogs(btn) {
  if (!(await showConfirm(t('确定清空所有请求日志？此操作不可恢复。')))) return;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('清空中...'); }
  try {
    await api('POST', '/request-logs/clear');
    toast(t('日志已清空'), 'success');
    loadRequestLogs(true);
  } catch (e) { toast(t('清空失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

function toggleLogFullscreen(btn) {
  const tab = document.getElementById('tab-logs');
  if (!tab) return;
  const on = tab.classList.toggle('log-fullscreen');
  if (btn) btn.classList.toggle('btn-primary', on);
}

// ---------- 事件委托（避免行内拼接动态字符串） ----------
(function wireLogEvents() {
  // 表格行：单击展开尝试链路，双击复制 JSON
  const tbody = _('logTableBody');
  if (tbody) {
    tbody.addEventListener('click', e => {
      if (e.target.closest('.log-detail-btn')) return; // 详情按钮走 capture 监听开弹窗
      const tr = e.target.closest('tr.log-row');
      if (!tr || !tr.classList.contains('has-attempts')) return;
      const l = _logItems[Number(tr.dataset.idx)];
      if (l) toggleLogDetailByItem(l);
    });
    tbody.addEventListener('dblclick', e => {
      const tr = e.target.closest('tr.log-row');
      if (tr) copyLogJson(Number(tr.dataset.idx));
    });
  }
  const cards = _('logCards');
  if (cards) {
    cards.addEventListener('dblclick', e => {
      const art = e.target.closest('.log-card');
      if (art) copyLogJson(Number(art.dataset.idx));
    });
    // 移动端两行列表：整行点击打开全屏详情抽屉（保留旧 .log-detail-btn 兜底）
    cards.addEventListener('click', e => {
      const row = e.target.closest('.log-row-mobile');
      if (row) { openLogDetail(Number(row.dataset.idx)); return; }
      const btn = e.target.closest('.log-detail-btn');
      if (btn) openLogDetail(Number(btn.dataset.idx));
    });
  }
  // 表格视图：行点击只做「展开尝试链路」，详情按钮单独开弹窗
  if (tbody) {
    tbody.addEventListener('click', e => {
      const btn = e.target.closest('.log-detail-btn');
      if (btn) openLogDetail(Number(btn.dataset.idx));
    }, true);
  }
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') closeLogDetail();
  });
})();

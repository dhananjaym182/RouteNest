// ========== 全局 Provider 视图（只读） ==========
// 数据来自 /admin/api/provider-instances、/credentials、/model-offerings、
// /provider-state、/egress-profiles。旧页面与接口保持不变，这里只做跨上游汇总：
// 能力状态、访问类别、冷却截止与配额重置分别呈现，未知项明确标注，不猜测。

let _globalData = null;
let _globalFilter = '';

const _globalCapabilities = [
  ['modelDiscovery', '模型发现'],
  ['freeModelDetection', '免费识别'],
  ['removedModelDetection', '下架识别'],
  ['reactiveQuota', '限流响应'],
  ['proactiveQuota', '主动配额'],
  ['quotaResetDetection', '配额重置'],
  ['credentialRotation', '凭据轮换'],
  ['egressPool', '出口池'],
  ['contextCompaction', '上下文压缩'],
];
const _globalCapabilityStates = { supported: '支持', unsupported: '不支持', unknown: '未知' };
const _globalAccess = { free: '免费', free_tier: '免费额度', subscription: '订阅', paid_api: '付费 API', trial: '试用', unknown: '未知' };
const _globalCredentialKinds = { api_key: 'API 密钥', oauth_account: 'OAuth 账号', refresh_token: '刷新令牌', imported_token: '导入令牌', session: '会话', public: '公共身份' };
const _globalStatuses = { active: '活跃', expired: '已过期', cooldown: '冷却' };
const _globalObservations = { rate_limited: '限流', quota_exhausted: '配额耗尽', quota_reset: '配额恢复', auth_expired: '认证过期', model_gone: '模型下架', transient_failure: '瞬时故障', recovered: '已恢复' };
const _globalConfidence = { explicit: '明确', inferred: '推断', fallback: '回退' };
const _globalStrategies = { round_robin: '轮询', random: '随机', fill: '填充' };

function globalSkeleton() {
  return '<div class="sk-stack p-16"><span class="skeleton"></span><span class="skeleton w70"></span></div>';
}

function globalEmpty() {
  return '<div class="empty">' + esc(t('暂无数据')) + '</div>';
}

// Go marshals a zero time.Time as 0001-01-01 rather than omitting it, so an
// absent timestamp must be rejected instead of rendered as a real date.
function globalTime(value) {
  if (!value) return null;
  const at = new Date(value);
  if (isNaN(at.getTime()) || at.getFullYear() < 2000) return null;
  return at;
}

function globalDate(value) {
  const at = globalTime(value);
  return at ? at.toLocaleString(LC()) : '—';
}

function globalCountdown(value) {
  const at = globalTime(value);
  if (!at) return '—';
  return '<span title="' + escAttr(at.toLocaleString(LC())) + '">' + esc(formatCooldown(value)) + '</span>';
}

function globalTable(headers, rows) {
  if (!rows.length) return globalEmpty();
  const head = headers.map(h => '<th>' + esc(t(h)) + '</th>').join('');
  return '<table style="table-layout:auto"><thead><tr>' + head + '</tr></thead><tbody>' + rows.join('') + '</tbody></table>';
}

function globalCell(value) {
  return '<td>' + (value === '' || value === undefined || value === null ? '—' : value) + '</td>';
}

function globalMatches(providerID) {
  return !_globalFilter || providerID === _globalFilter;
}

function renderGlobalSummary(data) {
  const el = _('globalSummary');
  if (!el) return;
  const instances = data.instances || [];
  const stat = (value, label) => '<div class="quota-stat"><div class="quota-stat-n">' + formatNumber(value) + '</div><div class="quota-stat-l">' + esc(t(label)) + '</div></div>';
  el.innerHTML =
    stat(instances.length, 'Provider 实例') +
    stat(instances.filter(i => i.enabled).length, '启用') +
    stat((data.credentials || []).length, '凭据') +
    stat((data.offerings || []).length, '模型授权') +
    stat((data.state || []).length, '运行状态') +
    stat((data.profiles || []).length, '出口配置');
}

function renderGlobalFilter(data) {
  const el = _('globalProviderFilter');
  if (!el) return;
  const options = ['<option value="">' + esc(t('全部 Provider')) + '</option>'];
  const seen = {};
  (data.instances || []).forEach(i => {
    if (!i || !i.id || seen[i.id]) return;
    seen[i.id] = true;
    options.push('<option value="' + escAttr(i.id) + '">' + esc(i.name || i.id) + '</option>');
  });
  el.innerHTML = options.join('');
  el.value = _globalFilter;
}

function renderGlobalInstances(data) {
  const el = _('globalInstances');
  if (!el) return;
  const rows = (data.instances || []).filter(i => globalMatches(i.id)).map(i => {
    const caps = i.capabilities || {};
    const chips = _globalCapabilities.map(([key, label]) => {
      const state = caps[key] || 'unknown';
      return '<span class="model-tag"><span>' + esc(t(label)) + '</span> <span>' + esc(t(_globalCapabilityStates[state] || '未知')) + '</span></span>';
    }).join(' ');
    return '<tr>' +
      '<td><div>' + esc(i.name || i.id) + '</div><div class="mono">' + esc(i.id) + '</div></td>' +
      globalCell('<span class="mono">' + esc(i.adapter || '—') + '</span>') +
      globalCell(i.enabled ? esc(t('启用')) : esc(t('已停用'))) +
      globalCell(i.freeTier ? esc(t('免费')) : '—') +
      '<td>' + chips + '</td>' +
      '</tr>';
  });
  el.innerHTML = globalTable(['名称', '适配器', '状态', '免费层', '能力'], rows);
}

function renderGlobalCredentials(data) {
  const el = _('globalCredentials');
  if (!el) return;
  const rows = (data.credentials || []).filter(c => (c.providerIds || []).some(globalMatches)).map(c => {
    const cooldown = globalCountdown(c.cooldownUntil);
    return '<tr>' +
      '<td><div>' + esc(c.subject || c.id) + '</div><div class="mono">' + esc(c.id) + '</div></td>' +
      globalCell(esc(t(_globalCredentialKinds[c.kind] || c.kind || '未知'))) +
      globalCell(esc((c.providerIds || []).join(' · '))) +
      globalCell(esc(t(_globalStatuses[c.status] || c.status || '未知'))) +
      globalCell(globalDate(c.expiresAt)) +
      globalCell(cooldown) +
      '</tr>';
  });
  el.innerHTML = globalTable(['主体', '类型', 'Provider', '状态', '到期', '冷却至'], rows);
}

function renderGlobalOfferings(data) {
  const el = _('globalOfferings');
  if (!el) return;
  const rows = (data.offerings || []).filter(o => globalMatches(o.providerId)).map(o => {
    const tags = [];
    if (o.delisted) tags.push('<span class="model-tag">' + esc(t('模型下架')) + '</span>');
    if (o.disabled) tags.push('<span class="model-tag">' + esc(t('已停用')) + '</span>');
    const signal = o.tierSignal ? (_tierSignals[o.tierSignal] || o.tierSignal) : '';
    return '<tr>' +
      '<td class="mono">' + esc(o.providerId) + '</td>' +
      '<td class="mono">' + esc(o.modelId) + '</td>' +
      globalCell(esc(t(_globalAccess[o.access] || '未知'))) +
      globalCell(esc(t(_globalStatuses[o.status] || o.status || '—'))) +
      '<td>' + (signal ? esc(signal) : '—') + ' ' + tags.join(' ') + '</td>' +
      '</tr>';
  });
  el.innerHTML = globalTable(['Provider', '模型', '访问类别', '状态', '来源'], rows);
}

function globalScope(scope) {
  if (!scope) return '—';
  return [scope.providerId, scope.credentialId, scope.modelId, scope.egressId].filter(Boolean).join(' · ') || '—';
}

function renderGlobalState(data) {
  const el = _('globalState');
  if (!el) return;
  const rows = (data.state || []).filter(o => globalMatches(o.scope && o.scope.providerId)).map(o => {
    const until = globalCountdown(o.until);
    const reset = o.resetAt ? globalCountdown(o.resetAt) : esc(t('未知'));
    return '<tr>' +
      globalCell(esc(t(_globalObservations[o.kind] || o.kind || '—'))) +
      globalCell('<span class="mono">' + esc(globalScope(o.scope)) + '</span>') +
      globalCell(until) +
      globalCell(reset) +
      globalCell('<span class="mono">' + esc(o.source || '—') + '</span>') +
      globalCell(esc(t(_globalConfidence[o.confidence] || o.confidence || '未知'))) +
      '</tr>';
  });
  el.innerHTML = globalTable(['类型', '范围', '冷却至', '重置时间', '证据', '置信度'], rows);
}

function renderGlobalEgress(data) {
  const el = _('globalEgress');
  if (!el) return;
  const globalID = data.globalProfileId || '';
  const rows = (data.profiles || []).map(p => {
    const endpoints = (p.proxies || []);
    const cell = endpoints.length
      ? endpoints.map(e => '<div class="mono">' + esc(e) + '</div>').join('')
      : '—';
    const isGlobal = p.id === globalID;
    return '<tr>' +
      '<td><div>' + esc(p.name || p.id) + '</div><div class="mono">' + esc(p.id) + '</div></td>' +
      globalCell(isGlobal ? '<span class="model-tag">' + esc(t('全局')) + '</span>' : '—') +
      globalCell(esc(t(_globalStrategies[p.strategy] || p.strategy || '未知'))) +
      globalCell(String(endpoints.length)) +
      '<td>' + cell + '</td>' +
      '</tr>';
  });
  el.innerHTML = globalTable(['名称', '默认', '策略', '端点', '地址'], rows);
}

function renderGlobal() {
  const data = _globalData;
  if (!data) return;
  renderGlobalSummary(data);
  renderGlobalFilter(data);
  renderGlobalInstances(data);
  renderGlobalCredentials(data);
  renderGlobalOfferings(data);
  renderGlobalState(data);
  renderGlobalEgress(data);
}

function setGlobalFilter(value) {
  _globalFilter = value || '';
  renderGlobal();
}

async function loadGlobal(showSkeleton) {
  if (showSkeleton !== false) {
    ['globalInstances', 'globalCredentials', 'globalOfferings', 'globalState', 'globalEgress'].forEach(id => {
      const el = _(id);
      if (el) el.innerHTML = globalSkeleton();
    });
  }
  try {
    const [instances, credentials, offerings, state, egress] = await Promise.all([
      api('GET', '/provider-instances'),
      api('GET', '/credentials'),
      api('GET', '/model-offerings'),
      api('GET', '/provider-state'),
      api('GET', '/egress-profiles'),
    ]);
    _globalData = {
      instances: (instances.data && instances.data.instances) || [],
      credentials: (credentials.data && credentials.data.credentials) || [],
      offerings: (offerings.data && offerings.data.offerings) || [],
      state: (state.data && state.data.observations) || [],
      profiles: (egress.data && egress.data.profiles) || [],
      globalProfileId: (egress.data && egress.data.globalProfileId) || '',
    };
    renderGlobal();
  } catch (e) {
    _globalData = null;
    const message = '<div class="empty">' + esc(t('加载失败')) + ': ' + esc(e.message) + '</div>';
    ['globalInstances', 'globalCredentials', 'globalOfferings', 'globalState', 'globalEgress'].forEach(id => {
      const el = _(id);
      if (el) el.innerHTML = message;
    });
    toast(t('加载失败') + ': ' + e.message, 'error');
  }
}

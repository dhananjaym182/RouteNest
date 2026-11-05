// ========== 全局 Provider 系统视图（只读投影 + 兼容工作流入口） ==========
// 五个全局端点只提供脱敏读模型。写操作继续走现有账号、Provider、模型与网络接口。

let _globalData = null;
let _globalFilter = '';
let _globalProviderSearch = '';
let _globalProviderKind = '';
let _globalOfferingSearch = '';
let _globalOfferingAccess = '';
let _globalOfferingSort = 'model';
let _globalOfferingsExpanded = false;
const _globalOfferingsPreviewCount = 12;

const _globalFilterIDs = [
  'globalProviderFilter',
  'globalCredentialProviderFilter',
  'globalModelProviderFilter',
  'globalHealthProviderFilter',
];
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
const _globalStatuses = { active: '活跃', expired: '已过期', cooldown: '冷却', disabled: '已停用' };
const _globalObservations = { rate_limited: '限流', quota_exhausted: '配额耗尽', quota_reset: '配额恢复', auth_expired: '认证过期', model_gone: '模型下架', transient_failure: '瞬时故障', recovered: '已恢复' };
const _globalConfidence = { explicit: '明确', inferred: '推断', fallback: '回退' };
const _globalStrategies = { round_robin: '轮询', random: '随机', fill: '填充' };

function globalSkeleton() {
  return '<div class="sk-stack p-16"><span class="skeleton"></span><span class="skeleton w70"></span><span class="skeleton w45"></span></div>';
}

function globalEmpty(message) {
  return '<div class="system-empty"><span class="system-empty-mark" aria-hidden="true">◇</span><span>' + esc(t(message || '暂无数据')) + '</span></div>';
}

// Go marshals a zero time.Time as year 1. Treat it as absent rather than showing
// a plausible-looking date in the UI.
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
  return '<span class="time-value" title="' + escAttr(at.toLocaleString(LC())) + '">' + esc(formatCooldown(value)) + '</span>';
}

function globalJS(value) {
  return escAttr(JSON.stringify(String(value == null ? '' : value)));
}

function globalMatches(providerID) {
  return !_globalFilter || String(providerID || '') === _globalFilter;
}

function globalProviderMatches(instance) {
  if (!instance || !globalMatches(instance.id)) return false;
  if (_globalProviderKind && String(instance.kind || '') !== _globalProviderKind) return false;
  if (!_globalProviderSearch) return true;
  const q = _globalProviderSearch.toLowerCase();
  return [instance.name, instance.id, instance.adapter, instance.kind, instance.baseURL]
    .filter(Boolean).join(' ').toLowerCase().includes(q);
}

function globalProviderName(providerID) {
  const id = String(providerID || '');
  const item = ((_globalData && _globalData.instances) || []).find(p => String(p.id) === id);
  return item ? (item.name || id) : id;
}

function globalBadge(label, tone, extra) {
  return '<span class="system-badge ' + escAttr(tone || 'neutral') + (extra ? ' ' + escAttr(extra) : '') + '">' + esc(t(label)) + '</span>';
}

function globalMeta(label, value, extra) {
  return '<div class="system-meta' + (extra ? ' ' + escAttr(extra) : '') + '"><span>' + esc(t(label)) + '</span><strong>' + (value || '—') + '</strong></div>';
}

function globalStatusTone(status) {
  if (status === 'active' || status === 'recovered') return 'positive';
  if (status === 'expired' || status === 'disabled') return 'negative';
  if (status === 'cooldown') return 'warning';
  return 'neutral';
}

function filteredGlobalData(data) {
  return {
    instances: (data.instances || []).filter(i => globalMatches(i.id)),
    credentials: (data.credentials || []).filter(c => (c.providerIds || []).some(globalMatches)),
    offerings: (data.offerings || []).filter(o => globalMatches(o.providerId)),
    state: (data.state || []).filter(o => globalMatches(o.scope && o.scope.providerId)),
  };
}

function renderGlobalSummary(data) {
  const el = _('globalSummary');
  if (!el) return;
  const view = filteredGlobalData(data);
  const stat = (value, label) => '<div class="system-stat"><strong>' + formatNumber(value) + '</strong><span>' + esc(t(label)) + '</span></div>';
  el.innerHTML =
    stat(view.instances.length, 'Providers') +
    stat(view.instances.filter(i => i.enabled).length, '已启用') +
    stat(view.credentials.length, '凭据') +
    stat(view.offerings.length, '模型授权') +
    stat(view.state.length, '可用性观察') +
    stat((data.profiles || []).length, '出口配置');
}

function renderGlobalFilters(data) {
  const options = ['<option value="">' + esc(t('全部 Provider')) + '</option>'];
  const seen = {};
  (data.instances || []).forEach(i => {
    if (!i || !i.id || seen[i.id]) return;
    seen[i.id] = true;
    options.push('<option value="' + escAttr(i.id) + '">' + esc(i.name || i.id) + '</option>');
  });
  if (_globalFilter && !seen[_globalFilter]) {
    options.push('<option value="' + escAttr(_globalFilter) + '">' + esc(_globalFilter) + '</option>');
  }
  _globalFilterIDs.forEach(id => {
    const el = _(id);
    if (!el) return;
    el.innerHTML = options.join('');
    el.value = _globalFilter;
  });
  const kind = _('globalProviderKindFilter');
  if (kind) {
    const kinds = [...new Set((data.instances || []).map(i => i.kind).filter(Boolean))].sort();
    kind.innerHTML = '<option value="">' + esc(t('全部类型')) + '</option>' + kinds.map(k => '<option value="' + escAttr(k) + '">' + esc(t(k)) + '</option>').join('');
    kind.value = _globalProviderKind;
  }
  const access = _('globalOfferingAccessFilter');
  if (access) access.value = _globalOfferingAccess;
  const sort = _('globalOfferingSort');
  if (sort) sort.value = _globalOfferingSort;
}

function capabilityHTML(capabilities) {
  const caps = capabilities || {};
  return _globalCapabilities.map(([key, label]) => {
    const raw = caps[key];
    const state = raw === 'supported' || raw === 'unsupported' ? raw : 'unknown';
    return '<div class="capability-row" data-capability="' + escAttr(key) + '">' +
      '<span>' + esc(t(label)) + '</span>' +
      '<strong class="capability-state capability-' + state + '" data-capability-state="' + state + '">' + esc(t(_globalCapabilityStates[state])) + '</strong>' +
      '</div>';
  }).join('');
}

function renderGlobalInstances(data) {
  const el = _('globalInstances');
  if (!el) return;
  const instances = (data.instances || []).filter(globalProviderMatches);
  if (!instances.length) { el.innerHTML = globalEmpty(_globalProviderSearch || _globalProviderKind ? '暂无匹配 Provider' : '暂无 Provider'); return; }
  el.innerHTML = '<div class="provider-inventory-grid">' + instances.map(i => {
    const name = i.name || i.id;
    const initial = Array.from(name || '?')[0] || '?';
    const kind = i.runtimeBuiltin ? '内置' : (i.kind || '自定义');
    const credentials = (data.credentials || []).filter(c => (c.providerIds || []).some(id => String(id) === String(i.id)));
    const offerings = (data.offerings || []).filter(o => String(o.providerId || '') === String(i.id));
    const authMethods = [...new Set(credentials.map(c => c.authFlow || t(_globalCredentialKinds[c.kind] || c.kind)).filter(Boolean))];
    return '<article class="provider-inventory-card">' +
      '<div class="provider-card-head"><span class="provider-monogram" aria-hidden="true">' + esc(initial.toUpperCase()) + '</span>' +
        '<div class="provider-card-title"><h3>' + esc(name) + '</h3><code>' + esc(i.id) + '</code></div>' +
        globalBadge(i.enabled ? '已启用' : '已停用', i.enabled ? 'positive' : 'negative') +
      '</div>' +
      '<div class="provider-card-meta">' +
        globalMeta('适配器', '<code>' + esc(i.adapter || '—') + '</code>') +
        globalMeta('类型', esc(t(kind))) +
        globalMeta('认证方式', esc(authMethods.length ? authMethods.join(' · ') : t('未配置'))) +
        globalMeta('凭据', formatNumber(credentials.length)) +
        globalMeta('模型授权', formatNumber(offerings.length)) +
        globalMeta('Base URL', i.baseURL ? '<code title="' + escAttr(i.baseURL) + '">' + esc(i.baseURL) + '</code>' : '—') +
      '</div>' +
      '<div class="capability-grid">' + capabilityHTML(i.capabilities) + '</div>' +
      '<div class="provider-card-actions" aria-label="' + escAttr(t('关联视图')) + '">' +
        '<button type="button" class="text-action" onclick="openGlobalProviderView(' + globalJS('credentials') + ',' + globalJS(i.id) + ')">' + esc(t('凭据')) + ' →</button>' +
        '<button type="button" class="text-action" onclick="openGlobalProviderView(' + globalJS('models') + ',' + globalJS(i.id) + ')">' + esc(t('模型')) + ' →</button>' +
        '<button type="button" class="text-action" onclick="openGlobalProviderView(' + globalJS('health') + ',' + globalJS(i.id) + ')">' + esc(t('使用与健康')) + ' →</button>' +
        '<button type="button" class="text-action" onclick="openGlobalProviderView(' + globalJS('network') + ',' + globalJS(i.id) + ')">' + esc(t('网络')) + ' →</button>' +
      '</div>' +
      '</article>';
  }).join('') + '</div>';
}

function renderGlobalCredentials(data) {
  const el = _('globalCredentials');
  if (!el) return;
  const credentials = (data.credentials || []).filter(c => (c.providerIds || []).some(globalMatches));
  if (!credentials.length) { el.innerHTML = globalEmpty('暂无匹配凭据'); return; }
  el.innerHTML = '<div class="system-card-grid">' + credentials.map(c => {
    const providers = (c.providerIds || []).map(id => globalProviderName(id) || id).join(' · ');
    const status = c.status || 'unknown';
    const cooldown = globalTime(c.cooldownUntil);
    return '<article class="system-card credential-card">' +
      '<div class="system-card-head"><div><h3>' + esc(c.subject || c.id) + '</h3><code>' + esc(c.id) + '</code></div>' + globalBadge(_globalStatuses[status] || '未知', globalStatusTone(status)) + '</div>' +
      '<div class="system-card-tags">' + globalBadge(_globalCredentialKinds[c.kind] || c.kind || '未知', 'accent') + (c.refreshable ? globalBadge('可刷新', 'positive') : '') + '</div>' +
      '<div class="system-meta-grid">' +
        globalMeta('Provider', esc(providers || '—')) +
        globalMeta('认证方式', esc(c.authFlow || '—')) +
        globalMeta('到期', esc(globalDate(c.expiresAt))) +
        globalMeta('冷却至', cooldown ? globalCountdown(c.cooldownUntil) : '—', cooldown ? 'warning' : '') +
        globalMeta('请求', formatNumber(c.usageCount || 0)) +
        globalMeta('Token', formatTokenCount(c.totalTokens || 0)) +
      '</div>' +
      '</article>';
  }).join('') + '</div>';
}

function accessTone(access) {
  if (access === 'free' || access === 'free_tier') return 'positive';
  if (access === 'unknown') return 'unknown';
  if (access === 'subscription') return 'accent';
  return 'warning';
}

function renderGlobalOfferings(data) {
  const el = _('globalOfferings');
  if (!el) return;
  const q = _globalOfferingSearch.toLowerCase();
  const offerings = (data.offerings || []).filter(o => {
    if (!globalMatches(o.providerId)) return false;
    if (_globalOfferingAccess && String(o.access || 'unknown') !== _globalOfferingAccess) return false;
    if (q && ![o.modelId, o.upstreamModelId, o.providerId, globalProviderName(o.providerId)].filter(Boolean).join(' ').toLowerCase().includes(q)) return false;
    return true;
  }).sort((a, b) => {
    if (_globalOfferingSort === 'provider') return String(globalProviderName(a.providerId)).localeCompare(String(globalProviderName(b.providerId))) || String(a.modelId).localeCompare(String(b.modelId));
    if (_globalOfferingSort === 'access') return String(a.access || 'unknown').localeCompare(String(b.access || 'unknown')) || String(a.modelId).localeCompare(String(b.modelId));
    return String(a.modelId).localeCompare(String(b.modelId));
  });
  if (!offerings.length) { el.innerHTML = globalEmpty('暂无匹配模型授权'); return; }
  const shown = _globalOfferingsExpanded ? offerings : offerings.slice(0, _globalOfferingsPreviewCount);
  const hiddenCount = Math.max(0, offerings.length - shown.length);
  const previewNote = hiddenCount
    ? '<div class="system-note offering-preview-note">' + esc(t('仅预览前 12 条，共 N 条').replace('N', offerings.length)) + '</div>'
    : '';
  el.innerHTML = previewNote + '<div class="offerings-table-wrap"><table class="offerings-table"><thead><tr>' +
    '<th>' + esc(t('逻辑模型')) + '</th><th>Provider</th><th>' + esc(t('上游模型')) + '</th><th>' + esc(t('访问类别')) + '</th><th>' + esc(t('授权')) + '</th><th>' + esc(t('上下文')) + '</th><th>' + esc(t('状态')) + '</th></tr></thead><tbody>' + shown.map(o => {
    const signal = o.tierSignal ? (_tierSignals[o.tierSignal] || o.tierSignal) : '';
    const rawStatus = o.status || 'active';
    const state = o.disabled ? '已停用' : (o.delisted ? '已下架' : (_globalStatuses[rawStatus] || rawStatus));
    const stateTone = o.disabled || o.delisted ? 'negative' : globalStatusTone(rawStatus);
    return '<tr><td class="mono-heading" title="' + escAttr(o.modelId) + '">' + esc(o.modelId) + '</td>' +
      '<td><span class="provider-byline">' + esc(globalProviderName(o.providerId) || o.providerId) + '</span><code>' + esc(o.providerId) + '</code></td>' +
      '<td title="' + escAttr(o.upstreamModelId || o.modelId) + '">' + esc(o.upstreamModelId || o.modelId) + '</td>' +
      '<td>' + globalBadge(_globalAccess[o.access] || '未知', accessTone(o.access || 'unknown')) + (signal ? '<div class="hint-xs mt-4">' + esc(t(signal)) + '</div>' : '') + '</td>' +
      '<td>' + globalBadge(o.allowanceVerified ? '授权已验证' : '授权未验证', o.allowanceVerified ? 'positive' : 'unknown') + '</td>' +
      '<td>' + (o.context ? formatNumber(o.context) : '—') + '</td><td class="offering-status-cell">' + globalBadge(state, stateTone) + '</td></tr>';
  }).join('') + '</tbody></table></div>';
  const toggle = _('globalOfferingsToggle');
  if (toggle) {
    toggle.textContent = t(offerings.length > _globalOfferingsPreviewCount && !_globalOfferingsExpanded ? '展开全部' : '收起');
    toggle.setAttribute('aria-expanded', _globalOfferingsExpanded ? 'true' : 'false');
  }
}

function renderGlobalHealthSummary(data) {
  const el = _('globalHealthSummary');
  if (!el) return;
  const view = filteredGlobalData(data);
  const activeCreds = view.credentials.filter(c => c.status === 'active').length;
  const attentionCreds = view.credentials.length - activeCreds;
  const attentionObservations = view.state.filter(o => o.kind !== 'recovered' && o.kind !== 'quota_reset').length;
  const stat = (value, label, tone) => '<div class="health-summary-card' + (tone ? ' ' + tone : '') + '"><strong>' + formatNumber(value) + '</strong><span>' + esc(t(label)) + '</span></div>';
  el.innerHTML = stat(view.instances.filter(i => i.enabled).length, '已启用 Providers') +
    stat(activeCreds, '活跃凭据') + stat(attentionCreds, '需关注凭据', attentionCreds ? 'has-warning' : '') +
    stat(view.state.length, '可用性观察', attentionObservations ? 'has-warning' : '');
}

function toggleGlobalOfferings() {
  _globalOfferingsExpanded = !_globalOfferingsExpanded;
  if (_globalData) renderGlobalOfferings(_globalData);
}

function globalScopeParts(scope) {
  if (!scope) return [];
  return [
    ['Provider', scope.providerId],
    ['凭据', scope.credentialId],
    ['模型', scope.modelId],
    ['出口', scope.egressId],
  ].filter(([, value]) => value);
}

function renderObservationTimes(observation) {
  const until = globalTime(observation.until);
  const reset = globalTime(observation.resetAt);
  const fallback = observation.confidence === 'fallback' && !!until && !reset;
  let resetLabel = '未确认重置';
  let resetTone = 'unknown';
  if (reset && observation.confidence === 'explicit') { resetLabel = '上游确认重置'; resetTone = 'confirmed'; }
  else if (reset && observation.confidence === 'inferred') { resetLabel = '推断重置'; resetTone = 'inferred'; }
  else if (reset) { resetLabel = '已知重置'; resetTone = 'confirmed'; }
  const cooldownLabel = fallback ? '回退冷却截止' : '可用性冷却截止';
  return '<div class="observation-times">' +
    '<div class="observation-time ' + (fallback ? 'fallback' : 'cooldown') + '"><span>' + esc(t(cooldownLabel)) + '</span><strong>' + (until ? globalCountdown(observation.until) : '—') + '</strong>' + (until ? '<small>' + esc(globalDate(observation.until)) + '</small>' : '') + '</div>' +
    '<div class="observation-time reset ' + resetTone + '"><span>' + esc(t(resetLabel)) + '</span><strong>' + (reset ? globalCountdown(observation.resetAt) : esc(t('未知'))) + '</strong>' + (reset ? '<small>' + esc(globalDate(observation.resetAt)) + '</small>' : '<small>' + esc(t('没有上游重置证据')) + '</small>') + '</div>' +
    '</div>';
}

function renderGlobalState(data) {
  const el = _('globalState');
  if (!el) return;
  const observations = (data.state || []).filter(o => globalMatches(o.scope && o.scope.providerId));
  if (!observations.length) { el.innerHTML = globalEmpty('暂无可用性观察'); return; }
  el.innerHTML = '<div class="observation-list">' + observations.map(o => {
    const scope = globalScopeParts(o.scope);
    return '<article class="observation-card' + (o.confidence === 'fallback' && !globalTime(o.resetAt) ? ' is-fallback' : '') + '">' +
      '<div class="observation-head"><div><span class="provider-byline">' + esc(globalProviderName(o.scope && o.scope.providerId)) + '</span><h3>' + esc(t(_globalObservations[o.kind] || o.kind || '未知')) + '</h3></div>' + globalBadge(_globalConfidence[o.confidence] || '未知', o.confidence === 'fallback' ? 'warning' : 'accent') + '</div>' +
      '<div class="scope-row">' + scope.map(([label, value]) => '<span><b>' + esc(t(label)) + '</b><code>' + esc(value) + '</code></span>').join('') + '</div>' +
      renderObservationTimes(o) +
      '<div class="evidence-row"><span>' + esc(t('证据来源')) + '</span><code>' + esc(o.source || '—') + '</code></div>' +
      '</article>';
  }).join('') + '</div>';
}

function renderGlobalEgress(data) {
  const el = _('globalEgress');
  if (el) {
    const profiles = data.profiles || [];
    if (!profiles.length) el.innerHTML = globalEmpty('暂无出口配置');
    else el.innerHTML = '<div class="system-card-grid egress-grid">' + profiles.map(p => {
      const endpoints = p.proxies || [];
      const endpointList = endpoints.length
        ? '<div class="endpoint-list">' + endpoints.map(address => '<code>' + esc(address) + '</code>').join('') + '</div>'
        : '<span class="mut">' + esc(t('直连或未配置')) + '</span>';
      return '<article class="system-card egress-card">' +
        '<div class="system-card-head"><div><h3>' + esc(p.name || p.id) + '</h3><code>' + esc(p.id) + '</code></div>' + (p.id === data.globalProfileId ? globalBadge('全局默认', 'accent') : '') + '</div>' +
        '<div class="system-card-tags">' + globalBadge(_globalStrategies[p.strategy] || p.strategy || '未知', 'neutral') + globalBadge(String(endpoints.length) + ' ' + t('个端点'), 'neutral') + '</div>' +
        endpointList +
        '</article>';
    }).join('') + '</div>';
  }
  const context = _('globalNetworkContext');
  if (context) {
    context.innerHTML = _globalFilter
      ? '<strong>' + esc(t('当前 Provider')) + '</strong><span>' + esc(globalProviderName(_globalFilter) || _globalFilter) + '</span><small>' + esc(t('当前接口未提供 Provider 到出口配置的绑定关系；下方仍显示全局清单。')) + '</small>'
      : '<span>' + esc(t('出口配置按全局范围展示。')) + '</span>';
  }
}

function renderGlobal() {
  const data = _globalData;
  if (!data) return;
  renderGlobalFilters(data);
  renderGlobalSummary(data);
  renderGlobalInstances(data);
  renderGlobalCredentials(data);
  renderGlobalOfferings(data);
  renderGlobalState(data);
  renderGlobalEgress(data);
  renderGlobalHealthSummary(data);
}

function setGlobalFilter(value) {
  _globalFilter = value || '';
  renderGlobal();
}

function setGlobalProviderSearch(value) {
  _globalProviderSearch = String(value || '').trim();
  if (_globalData) renderGlobalInstances(_globalData);
}

function setGlobalProviderKind(value) {
  _globalProviderKind = value || '';
  if (_globalData) renderGlobalInstances(_globalData);
}

function setGlobalOfferingSearch(value) {
  _globalOfferingSearch = String(value || '').trim();
  _globalOfferingsExpanded = false;
  if (_globalData) renderGlobalOfferings(_globalData);
}

function setGlobalOfferingAccess(value) {
  _globalOfferingAccess = value || '';
  _globalOfferingsExpanded = false;
  if (_globalData) renderGlobalOfferings(_globalData);
}

function setGlobalOfferingSort(value) {
  _globalOfferingSort = value || 'model';
  if (_globalData) renderGlobalOfferings(_globalData);
}

function openGlobalProviderView(tab, providerID) {
  _globalFilter = String(providerID || '');
  applyTab(tab);
  if (tab === 'credentials') switchPageSubTab('accounts', 'inventory', true);
  if (_globalData) renderGlobal();
}

async function loadGlobal(showSkeleton) {
  const ids = ['globalInstances', 'globalCredentials', 'globalOfferings', 'globalState', 'globalEgress', 'globalHealthSummary'];
  if (showSkeleton !== false) {
    ids.forEach(id => {
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
    const message = '<div class="system-empty"><span>' + esc(t('加载失败')) + ': ' + esc(e.message) + '</span></div>';
    ids.forEach(id => {
      const el = _(id);
      if (el) el.innerHTML = message;
    });
    toast(t('加载失败') + ': ' + e.message, 'error');
  }
}

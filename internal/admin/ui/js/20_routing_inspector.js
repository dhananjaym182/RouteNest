// Selected-model route inspector.
// Reads existing provider/credential/offering/state projections plus stored
// request logs. It never computes eligibility, cooldowns or routing priority.

let _routeInspectorData = null;
let _routeInspectorModel = '';
let _routeInspectorLoadGen = 0;
let _routeInspectorLoadedAt = 0;
let _routeInspectorFilter = '';
const ROUTE_INSPECTOR_FRESH_MS = 30000; // 切回「路由检查」时，30 秒内不重复读取

// ===== 权限：没有 provider.write 的账号只读 =====
// GET /config 与保存都需要 provider.write；没有它时表单整体禁用并给出说明，
// 流程图不再停在「加载中」，检查器照常工作（只是没有全局配置部分）。
function routingCanWrite() {
  return typeof adminHasPermission !== 'function' || adminHasPermission('provider.write');
}

function applyRoutingAccess() {
  const canWrite = routingCanWrite();
  const notice = _('routeReadOnlyNotice');
  if (notice) notice.hidden = canWrite;
  document.querySelectorAll('#tab-routing [data-route-write]').forEach(page => {
    page.querySelectorAll('input, select, textarea, button').forEach(el => {
      if (!canWrite) {
        if (!('routeWasDisabled' in el.dataset)) el.dataset.routeWasDisabled = el.disabled ? '1' : '0';
        el.disabled = true;
      } else if ('routeWasDisabled' in el.dataset) {
        el.disabled = el.dataset.routeWasDisabled === '1';
        delete el.dataset.routeWasDisabled;
      }
    });
  });
  if (!canWrite) {
    ['routeFlowModel', 'routeFlowStrategy', 'routeFlowFallback'].forEach(id => {
      const el = _(id);
      if (el) { el.textContent = t('无权限查看'); el.title = t('需要 provider.write 权限'); }
    });
  }
}

// ===== 惰性加载：检查器是独立子页，只有看得到时才读取 =====
function routeInspectorVisible() {
  const tab = document.querySelector('.page-subtabs[data-page="routing"] .subtab.active');
  return !!tab && tab.dataset.sub === 'inspect';
}

function routeInspectorShown() {
  const fresh = _routeInspectorData && Date.now() - _routeInspectorLoadedAt < ROUTE_INSPECTOR_FRESH_MS;
  return fresh ? Promise.resolve() : loadRouteInspector(false);
}

// 进入路由页时调用：上次停留在「路由检查」才立即加载，否则等用户切过去
function maybeLoadRouteInspector() {
  return routeInspectorVisible() ? routeInspectorShown() : Promise.resolve();
}

// 保存配置后只更新检查器里的配置部分（默认模型 / 回退链），不重读其余六个接口
function refreshRouteInspectorConfig(config) {
  if (!_routeInspectorData) return;
  _routeInspectorData.config = config || {};
  _routeInspectorData.configAvailable = true;
  renderRouteInspector();
}

// 保存失败：下拉框回到服务端当前值（回退链文本是草稿，不动）
function restoreRouteSelects() {
  const c = typeof _lastAdminConfig !== 'undefined' ? _lastAdminConfig : null;
  if (!c) return;
  if (c.strategy && _('settingStrategy')) _('settingStrategy').value = c.strategy;
  if (c.defaultModel !== undefined && _('settingDefModel')) _('settingDefModel').value = c.defaultModel;
  if (c.onlyFree !== undefined && _('settingOnlyFree')) _('settingOnlyFree').value = String(c.onlyFree);
}

// 「回退模型链」旁的选择器：从目录里挑一个模型追加到链尾并保存
function addModelToChain() {
  const input = _('settingChainAdd'), chain = _('settingModelChain');
  if (!input || !chain) return;
  const id = input.value.trim();
  if (!id) return;
  if (id !== 'free' && !(_cachedModels || []).some(m => m.id === id)) {
    toast(t('目录里没有这个模型；要填列表外的 ID 请直接在上方输入框输入'), 'info');
    return;
  }
  const ids = chain.value.split(',').map(x => x.trim()).filter(Boolean);
  input.value = '';
  if (ids.indexOf(id) !== -1) { toast(t('已在回退链中'), 'info'); return; }
  ids.push(id);
  chain.value = ids.join(', ');
  chain.dataset.dirty = '1';
  return updateConfig();
}

// Same guard as globalTime(): a Go zero time is not a real timestamp.
function routeInspectorTime(value) {
  if (!value) return null;
  const at = new Date(value);
  if (isNaN(at.getTime()) || at.getFullYear() < 2000) return null;
  return at;
}

function routeInspectorDate(value) {
  const at = routeInspectorTime(value);
  return at ? at.toLocaleString(LC()) : t('未知');
}

function routeInspectorMono(value) {
  return '<code class="ri-mono">' + esc(value == null ? '' : String(value)) + '</code>';
}

function routeInspectorChip(label, tone) {
  return '<span class="ri-chip' + (tone ? ' ' + escAttr(tone) : '') + '">' + esc(t(label)) + '</span>';
}

function routeInspectorFact(label, valueHTML) {
  return '<div class="ri-fact"><span>' + esc(t(label)) + '</span><div class="ri-fact-value">' + valueHTML + '</div></div>';
}

function routeInspectorSkeleton() {
  return '<div class="ri-skeleton"><span class="skeleton"></span><span class="skeleton w70"></span><span class="skeleton w45"></span></div>';
}

function routeInspectorSetLoading() {
  const err = _('routeInspectorError');
  if (err) { err.hidden = true; err.innerHTML = ''; }
  ['routeInspectorSummary', 'routeInspectorCandidates', 'routeInspectorFallback', 'routeInspectorRuntime'].forEach(id => {
    const el = _(id);
    if (el) el.innerHTML = routeInspectorSkeleton();
  });
}

function routeInspectorShowError(message) {
  const err = _('routeInspectorError');
  if (err) {
    err.hidden = false;
    err.innerHTML = '<div class="ri-error-head">' + esc(t('路由检查加载失败')) + '</div>' +
      '<div class="ri-error-msg">' + esc(message || t('未知')) + '</div>' +
      '<button type="button" class="btn btn-sm" onclick="loadRouteInspector(true)">' + esc(t('重试')) + '</button>';
  }
  ['routeInspectorSummary', 'routeInspectorCandidates', 'routeInspectorFallback', 'routeInspectorRuntime'].forEach(id => {
    const el = _(id);
    if (el) el.innerHTML = '';
  });
}

async function loadRouteInspector(force, quiet) {
  if (!_('routeInspectorSection')) return;
  const generation = ++_routeInspectorLoadGen;
  if (!quiet && (!_routeInspectorData || force)) routeInspectorSetLoading();
  try {
    const canReadCredentials = typeof adminHasPermission !== 'function' || adminHasPermission('credential.write');
    // 凭据与全局配置对只读账号是不可见的：跳过请求，或把权限拒绝当作“没有这部分”
    const optional = (allowed, request) => allowed ? request().catch(e => {
      if (e && e.message === 'permission_denied') return null;
      throw e;
    }) : Promise.resolve(null);
    const [instances, credentials, offerings, state, config, combos, logs] = await Promise.all([
      api('GET', '/provider-instances'),
      optional(canReadCredentials, () => api('GET', '/credentials')),
      api('GET', '/model-offerings'),
      api('GET', '/provider-state'),
      optional(routingCanWrite(), () => fetchAdminConfig()),
      api('GET', '/combos'),
      api('GET', '/request-logs?limit=100'),
    ]);
    if (generation !== _routeInspectorLoadGen) return;
    _routeInspectorData = {
      instances: (instances.data && instances.data.instances) || [],
      credentials: (credentials && credentials.data && credentials.data.credentials) || [],
      credentialsAvailable: credentials !== null,
      offerings: (offerings.data && offerings.data.offerings) || [],
      observations: (state.data && state.data.observations) || [],
      config: (config && config.data) || {},
      configAvailable: config !== null,
      combos: (combos.data && combos.data.combos) || [],
      logs: (logs.data && logs.data.items) || [],
    };
    _routeInspectorLoadedAt = Date.now();
    const err = _('routeInspectorError');
    if (err) { err.hidden = true; err.innerHTML = ''; }
    renderRouteInspector();
  } catch (e) {
    if (generation !== _routeInspectorLoadGen) return;
    _routeInspectorData = null;
    routeInspectorShowError(e && e.message);
  }
}

function selectRouteInspectorModel(value) {
  _routeInspectorModel = String(value || '');
  if (_routeInspectorData) renderRouteInspector();
}

// Union of known model ids. The default model and the global fallback chain come
// first (in that order); everything else is sorted by name. No routing order is
// implied by this picker.
function routeInspectorModelIDs(data) {
  const seen = {};
  const clean = value => String(value == null ? '' : value).trim();
  const pinned = [];
  const rest = [];
  const add = (list, value) => {
    const id = clean(value);
    if (!id || seen[id]) return;
    seen[id] = true;
    list.push(id);
  };
  add(pinned, data.config && data.config.defaultModel);
  ((data.config && data.config.modelChain) || []).forEach(id => add(pinned, id));
  (data.offerings || []).forEach(o => add(rest, o.modelId));
  (_cachedModels || []).forEach(m => add(rest, m && m.id));
  rest.sort((a, b) => a.localeCompare(b));
  const ids = pinned.concat(rest);
  ids.pinnedCount = pinned.length;
  return ids;
}

function filterRouteInspectorModels(value) {
  _routeInspectorFilter = String(value || '').trim().toLowerCase();
  if (_routeInspectorData) routeInspectorSyncModelSelect(_routeInspectorData);
}

// Enter in the filter box: choose the first model that matches
function pickFirstRouteInspectorModel() {
  if (!_routeInspectorData) return;
  const first = routeInspectorModelIDs(_routeInspectorData).find(id => !_routeInspectorFilter || id.toLowerCase().indexOf(_routeInspectorFilter) !== -1);
  if (!first) return;
  _routeInspectorModel = first;
  renderRouteInspector();
}

function routeInspectorSyncModelSelect(data) {
  const sel = _('routeInspectorModel');
  if (!sel) return;
  const ids = routeInspectorModelIDs(data);
  const count = _('routeInspectorModelCount');
  if (!ids.length) {
    _routeInspectorModel = '';
    sel.disabled = true;
    sel.innerHTML = '<option value="">' + esc(t('没有可用模型')) + '</option>';
    if (count) count.textContent = '';
    return;
  }
  if (!_routeInspectorModel || ids.indexOf(_routeInspectorModel) === -1) {
    // 默认模型优先；读不到配置（只读账号）或没有默认时，选第一个确实有授权的模型，
    // 这样一打开就有内容可看，而不是一个没有候选的空面板
    const preferred = String((data.config && data.config.defaultModel) || '');
    const offered = {};
    (data.offerings || []).forEach(o => { offered[String(o.modelId || '')] = true; });
    _routeInspectorModel = ids.indexOf(preferred) !== -1 ? preferred : (ids.find(id => offered[id]) || ids[0]);
  }
  const matches = id => !_routeInspectorFilter || id === _routeInspectorModel || id.toLowerCase().indexOf(_routeInspectorFilter) !== -1;
  const option = id => '<option value="' + escAttr(id) + '"' + (id === _routeInspectorModel ? ' selected' : '') + '>' + esc(id) + '</option>';
  const pinned = ids.slice(0, ids.pinnedCount || 0).filter(matches);
  const rest = ids.slice(ids.pinnedCount || 0).filter(matches);
  sel.disabled = false;
  sel.innerHTML = (pinned.length ? '<optgroup label="' + escAttr(t('默认与回退链')) + '">' + pinned.map(option).join('') + '</optgroup>' : '') +
    (pinned.length ? '<optgroup label="' + escAttr(t('全部模型')) + '">' + rest.map(option).join('') + '</optgroup>' : rest.map(option).join(''));
  sel.value = _routeInspectorModel;
  if (count) count.textContent = _routeInspectorFilter ? (pinned.length + rest.length) + ' / ' + ids.length : String(ids.length);
}

function renderRouteInspector() {
  const data = _routeInspectorData;
  if (!data || !_('routeInspectorSection')) return;
  routeInspectorSyncModelSelect(data);
  const model = _routeInspectorModel;
  const candidates = routeInspectorCandidates(data, model);

  const summary = _('routeInspectorSummary');
  if (summary) summary.innerHTML = routeInspectorSummaryHTML(model, candidates, data.credentialsAvailable);
  const cEl = _('routeInspectorCandidates');
  if (cEl) cEl.innerHTML = routeInspectorCandidatesHTML(model, candidates);
  const fEl = _('routeInspectorFallback');
  if (fEl) fEl.innerHTML = routeInspectorFallbackHTML(data, model);
  const rEl = _('routeInspectorRuntime');
  if (rEl) rEl.innerHTML = routeInspectorRuntimeHTML(data, model);
}

function routeInspectorCandidates(data, model) {
  if (!model) return [];
  return (data.offerings || []).filter(o => String(o.modelId || '') === model).map(o => {
    const providerId = String(o.providerId || '');
    const instance = (data.instances || []).find(i => String(i.id || '') === providerId) || null;
    const credentials = (data.credentials || []).filter(c =>
      (c.providerIds || []).some(id => String(id) === providerId));
    const observations = (data.observations || []).filter(obs => {
      const scope = obs.scope || {};
      if (String(scope.providerId || '') !== providerId) return false;
      const scopedModel = String(scope.modelId || '');
      return !scopedModel || scopedModel === model;
    });
    return { offering: o, providerId: providerId, instance: instance, credentials: credentials,
      credentialsAvailable: data.credentialsAvailable, observations: observations };
  });
}

function routeInspectorSummaryHTML(model, candidates, credentialsAvailable) {
  const providers = {};
  const credentials = {};
  let providerCount = 0;
  let credentialCount = 0;
  let observationCount = 0;
  candidates.forEach(c => {
    if (!providers[c.providerId]) { providers[c.providerId] = true; providerCount++; }
    c.credentials.forEach(cred => {
      const id = String(cred.id || '');
      if (!credentials[id]) { credentials[id] = true; credentialCount++; }
    });
    observationCount += c.observations.length;
  });
  return '<div class="ri-fact-grid">' +
    routeInspectorFact('逻辑模型', model ? routeInspectorMono(model) : '<span class="ri-unknown">' + esc(t('没有可用模型')) + '</span>') +
    routeInspectorFact('候选 Provider 数', formatNumber(providerCount)) +
    routeInspectorFact('关联凭据数', credentialsAvailable === false
      ? '<span class="ri-unknown">' + esc(t('未知')) + '</span>' : formatNumber(credentialCount)) +
    routeInspectorFact('已知观察数', formatNumber(observationCount)) +
    '</div>';
}

function routeInspectorProviderCell(c) {
  const name = c.instance ? (c.instance.name || c.providerId) : t('未知');
  return '<div class="ri-provider">' +
    '<strong>' + esc(name) + '</strong>' +
    routeInspectorMono(c.providerId || t('未知')) +
    (c.instance ? '' : '<span class="ri-warn">' + esc(t('未找到 Provider 实例')) + '</span>') +
    '</div>';
}

function routeInspectorCredentialsCell(c, model) {
  if (c.credentialsAvailable === false) return '<span class="ri-unknown">' + esc(t('未知')) + '</span>';
  if (!c.credentials.length) return '<span class="ri-unknown">' + esc(t('没有凭据投影')) + '</span>';
  return '<ul class="ri-cred-list">' + c.credentials.map(cred => {
    const bits = [];
    if (cred.subject) bits.push('<span class="ri-cred-subject">' + esc(cred.subject) + '</span>');
    if (cred.id) bits.push(routeInspectorMono(cred.id));
    if (!cred.subject && !cred.id) bits.push('<span class="ri-unknown">' + esc(t('未知')) + '</span>');
    const cooldown = routeInspectorTime(cred.cooldownUntil);
    if (cooldown) bits.push('<span class="ri-chip warning">' + esc(t('冷却至')) + ' ' + esc(routeInspectorDate(cred.cooldownUntil)) + '</span>');
    const modelCooldown = cred.modelCooldowns && cred.modelCooldowns[model];
    if (routeInspectorTime(modelCooldown)) {
      bits.push('<span class="ri-chip warning">' + esc(t('模型冷却至')) + ' ' + esc(routeInspectorDate(modelCooldown)) + '</span>');
    }
    if (cred.status && cred.status !== 'active') {
      bits.push(routeInspectorChip(_globalStatuses[cred.status] || cred.status, globalStatusTone(cred.status)));
    }
    return '<li>' + bits.join('') + '</li>';
  }).join('') + '</ul>';
}

function routeInspectorUpstreamCell(c) {
  const upstream = c.offering.upstreamModelId;
  return upstream ? routeInspectorMono(upstream) : '<span class="ri-unknown">' + esc(t('未知')) + '</span>';
}

function routeInspectorAccessCell(c) {
  const access = c.offering.access || 'unknown';
  return '<div class="ri-chips">' +
    routeInspectorChip(_globalAccess[access] || '未知', accessTone(access)) +
    routeInspectorChip(c.offering.allowanceVerified ? '授权已验证' : '授权未验证', c.offering.allowanceVerified ? 'positive' : 'unknown') +
    '</div>';
}

function routeInspectorStateCell(c) {
  if (!c.observations.length) {
    return '<span class="ri-unknown">' + esc(t('无观察记录 / 未验证')) + '</span>';
  }
  return '<ul class="ri-state-list">' + c.observations.map(obs => {
    const kind = obs.kind || '';
    const bits = [];
    bits.push(routeInspectorChip(_globalObservations[kind] || kind || '未知', routeInspectorObservationTone(kind)));
    if (obs.confidence) bits.push(routeInspectorChip(_globalConfidence[obs.confidence] || obs.confidence, 'neutral'));
    if (routeInspectorTime(obs.until)) bits.push('<span class="ri-chip warning">' + esc(t('冷却至')) + ' ' + esc(routeInspectorDate(obs.until)) + '</span>');
    if (routeInspectorTime(obs.resetAt)) bits.push('<span class="ri-chip neutral">' + esc(t('重置')) + ' ' + esc(routeInspectorDate(obs.resetAt)) + '</span>');
    if (obs.source) bits.push(routeInspectorMono(obs.source));
    return '<li>' + bits.join('') + '</li>';
  }).join('') + '</ul>';
}

function routeInspectorObservationTone(kind) {
  return (kind === 'recovered' || kind === 'quota_reset') ? 'positive' : 'warning';
}

function routeInspectorPriorityCell(c) {
  if (!c.instance) return '<span class="ri-unknown">' + esc(t('未知')) + '</span>';
  return '<span class="ri-pri">' + esc(t('优先级')) + ' ' + formatNumber(c.instance.priority || 0) +
    ' · ' + esc(t('权重')) + ' ' + formatNumber(c.instance.weight || 0) + '</span>';
}

function routeInspectorFactsCell(c) {
  const facts = [];
  if (c.instance) {
    facts.push(routeInspectorChip(c.instance.enabled ? 'Provider 已启用' : 'Provider 已停用', c.instance.enabled ? 'positive' : 'negative'));
  } else {
    facts.push(routeInspectorChip('未找到 Provider 实例', 'warning'));
  }
  if (c.offering.providerEnabled === false) facts.push(routeInspectorChip('Provider 投影未启用', 'negative'));
  if (c.offering.disabled) facts.push(routeInspectorChip('模型授权已停用', 'negative'));
  if (c.offering.delisted) facts.push(routeInspectorChip('已下架', 'negative'));
  if (c.offering.status) {
    facts.push(routeInspectorChip(_globalStatuses[c.offering.status] || c.offering.status, globalStatusTone(c.offering.status)));
  }
  return '<div class="ri-chips">' + facts.join('') + '</div>';
}

function routeInspectorCandidatesHTML(model, candidates) {
  if (!model) return '<div class="ri-empty">' + esc(t('没有可用模型')) + '</div>';
  if (!candidates.length) return '<div class="ri-empty">' + esc(t('该模型没有模型授权投影')) + '</div>';
  const table = '<div class="ri-table-wrap"><table class="ri-table"><thead><tr>' +
    '<th>Provider</th>' +
    '<th>' + esc(t('账号 / 凭据')) + '</th>' +
    '<th>' + esc(t('上游模型')) + '</th>' +
    '<th>' + esc(t('访问 / 授权')) + '</th>' +
    '<th>' + esc(t('Provider 状态')) + '</th>' +
    '<th>' + esc(t('配置优先级/权重')) + '</th>' +
    '<th>' + esc(t('配置事实')) + '</th>' +
    '</tr></thead><tbody>' + candidates.map(c =>
      '<tr>' +
        '<td>' + routeInspectorProviderCell(c) + '</td>' +
        '<td>' + routeInspectorCredentialsCell(c, model) + '</td>' +
        '<td>' + routeInspectorUpstreamCell(c) + '</td>' +
        '<td>' + routeInspectorAccessCell(c) + '</td>' +
        '<td>' + routeInspectorStateCell(c) + '</td>' +
        '<td>' + routeInspectorPriorityCell(c) + '</td>' +
        '<td>' + routeInspectorFactsCell(c) + '</td>' +
      '</tr>').join('') + '</tbody></table></div>';

  const cards = '<div class="ri-card-list">' + candidates.map(c =>
    '<article class="ri-card">' +
      '<div class="ri-card-head">' + routeInspectorProviderCell(c) + '</div>' +
      '<div class="ri-card-grid">' +
        routeInspectorFact('账号 / 凭据', routeInspectorCredentialsCell(c, model)) +
        routeInspectorFact('上游模型', routeInspectorUpstreamCell(c)) +
        routeInspectorFact('访问 / 授权', routeInspectorAccessCell(c)) +
        routeInspectorFact('Provider 状态', routeInspectorStateCell(c)) +
        routeInspectorFact('配置优先级/权重', routeInspectorPriorityCell(c)) +
        routeInspectorFact('配置事实', routeInspectorFactsCell(c)) +
      '</div>' +
    '</article>').join('') + '</div>';

  return table + cards;
}

function routeInspectorComboHTML(combo, model) {
  const entries = combo.entries || [];
  return '<article class="ri-combo">' +
    '<div class="ri-combo-head">' +
      '<strong>' + esc(combo.name || combo.id || t('未知')) + '</strong>' +
      (combo.id ? routeInspectorMono(combo.id) : '') +
      routeInspectorChip(combo.enabled ? '已启用' : '已停用', combo.enabled ? 'positive' : 'negative') +
      (combo.emergencyFallback ? routeInspectorChip('紧急回退', 'warning') : '') +
    '</div>' +
    '<div class="ri-hint">' + esc(t('按下述配置条目顺序（不是运行时选择结果）')) + '</div>' +
    '<ol class="ri-chain">' + entries.map((entry, index) =>
      '<li' + (String(entry.model || '') === model ? ' class="is-selected"' : '') + '>' +
        '<span class="ri-order">' + (index + 1) + '</span>' +
        routeInspectorMono(entry.model || t('未知')) +
        (entry.provider ? '<span class="ri-hint">' + esc(entry.provider) + '</span>' : '') +
        (entry.source ? '<span class="ri-hint">' + esc(entry.source) + '</span>' : '') +
        (entry.disabled ? routeInspectorChip('已停用', 'negative') : '') +
      '</li>').join('') + '</ol>' +
    '</article>';
}

function routeInspectorFallbackHTML(data, model) {
  const chain = (data.config && Array.isArray(data.config.modelChain))
    ? data.config.modelChain.filter(Boolean) : [];
  let html = '<div class="ri-config-note">' + esc(t('以下为配置，不是运行时决策。')) + '</div>';
  html += '<div class="ri-fallback-row">' +
    '<div class="ri-sub-label">' + esc(t('全局回退链（配置）')) + '</div>';
  if (data.configAvailable === false) {
    html += '<div class="ri-chain-empty"><span class="ri-unknown">' + esc(t('需要 provider.write 权限才能查看全局回退链')) + '</span></div>';
  } else if (chain.length) {
    html += '<ol class="ri-chain">' + chain.map((id, index) =>
      '<li><span class="ri-order">' + (index + 1) + '</span>' + routeInspectorMono(id) + '</li>').join('') + '</ol>';
  } else {
    html += '<div class="ri-chain-empty">' +
      routeInspectorChip('内置免费回退链（配置）', 'neutral') +
      '<span class="ri-hint">' + esc(t('空配置表示使用内置免费回退链，这是配置默认值，不是预测。')) + '</span>' +
      '</div>';
  }
  html += '</div>';

  const combos = (data.combos || []).filter(combo =>
    (combo.entries || []).some(entry => String(entry.model || '') === model));
  html += '<div class="ri-fallback-row">' +
    '<div class="ri-sub-label">' + esc(t('引用此模型的模型组合')) + '</div>';
  if (!combos.length) {
    html += '<div class="ri-chain-empty"><span class="ri-unknown">' + esc(t('没有组合引用此模型')) + '</span></div>';
  } else {
    html += combos.map(combo => routeInspectorComboHTML(combo, model)).join('');
  }
  html += '</div>';
  return html;
}

function routeInspectorOutcomeLabel(outcome) {
  if (outcome === 'success') return t('成功');
  if (outcome === 'skipped') return t('跳过');
  if (outcome === 'failed') return t('失败');
  return outcome ? String(outcome) : t('未知');
}

function routeInspectorAttemptUpstream(attempt) {
  const parts = [];
  if (attempt.providerName) parts.push(esc(attempt.providerName));
  if (attempt.provider) parts.push(routeInspectorMono(attempt.provider));
  if (attempt.upstream) parts.push(routeInspectorMono(attempt.upstream));
  return parts.length ? parts.join(' · ') : '<span class="ri-unknown">' + esc(t('未知')) + '</span>';
}

function routeInspectorRuntimeHTML(data, model) {
  if (!model) return '<div class="ri-empty">' + esc(t('没有可用模型')) + '</div>';
  const items = data.logs || [];
  const matches = items.filter(item =>
    item && (String(item.model || '') === model || String(item.requestedModel || '') === model));
  matches.sort((a, b) => {
    const aTime = routeInspectorTime(a.startedAt);
    const bTime = routeInspectorTime(b.startedAt);
    return (bTime ? bTime.getTime() : 0) - (aTime ? aTime.getTime() : 0);
  });
  const latest = matches[0];
  let html = '<div class="ri-runtime-window">' +
    esc(t('已加载 N 条记录').replace('N', formatNumber(items.length))) + '</div>';

  if (!latest) {
    return html + '<div class="ri-empty">' +
      '<div>' + esc(t('已加载窗口内没有该模型的请求记录')) + '</div>' +
      '<div class="ri-chips">' + routeInspectorChip('未验证 / 未知', 'unknown') + '</div>' +
      '<div class="ri-hint">' + esc(t('运行追踪只来自已存储请求记录，不推断实时路由。')) + '</div>' +
      '</div>';
  }

  const attempts = Array.isArray(latest.attempts) ? latest.attempts : [];
  html += '<div class="ri-request-head">' +
    '<div class="ri-request-id"><span>' + esc(t('请求 ID')) + '</span>' + routeInspectorMono(latest.id || t('未知')) + '</div>' +
    '<div class="ri-request-time"><span>' + esc(t('时间')) + '</span>' + esc(routeInspectorDate(latest.startedAt)) + '</div>' +
    '<div>' + (latest.completed ? routeInspectorChip('完成', 'positive') : routeInspectorChip('失败', 'negative')) + '</div>' +
    '</div>';
  html += '<div class="ri-runtime-warning">' + esc(t('最近一条已存储请求记录；这不是实时路由策略')) + '</div>';

  if (attempts.length) {
    html += '<ol class="ri-attempts">' + attempts.map((attempt, index) => {
      const order = (typeof attempt.order === 'number' && attempt.order > 0) ? attempt.order : (index + 1);
      const duration = (typeof attempt.durationMs === 'number' && attempt.durationMs > 0)
        ? formatDuration(attempt.durationMs) : '';
      return '<li>' +
        '<span class="ri-order">' + order + '</span>' +
        '<div class="ri-attempt-main">' +
          routeInspectorMono(attempt.model || t('未知')) +
          '<span class="ri-attempt-meta">' + routeInspectorAttemptUpstream(attempt) + '</span>' +
        '</div>' +
        '<div class="ri-attempt-side">' +
          (attempt.status ? '<span>' + esc('HTTP ' + attempt.status) + '</span>' : '') +
          (duration ? '<span>' + esc(duration) + '</span>' : '') +
          '<span class="ri-outcome ' + escAttr(attempt.outcome || '') + '">' + esc(routeInspectorOutcomeLabel(attempt.outcome)) + '</span>' +
          (attempt.reason ? '<span class="ri-reason">' + esc(attempt.reason) + '</span>' : '') +
        '</div>' +
      '</li>';
    }).join('') + '</ol>';
  } else {
    html += '<div class="ri-empty">' + esc(t('该请求没有已存储的尝试链路')) + '</div>';
  }

  html += '<div class="ri-runtime-actions">' +
    '<button type="button" class="btn btn-sm" onclick="switchTab(\'logs\')">' + esc(t('查看请求日志')) + '</button>' +
    '</div>';
  return html;
}

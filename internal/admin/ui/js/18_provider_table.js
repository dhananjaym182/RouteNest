// ===== Providers page: the one provider table =====
// A row merges three sources: /provider-instances (identity), /model-offerings (models,
// credentials) and the managed-provider data loadProviders() fetches (health, sync, actions).
// Built-in services (Cline, OpenCode Zen) have no managed record, so their extra cells stay "—".
let _globalExpandedProviders = {};

function globalProviderCache() {
  return typeof _provCache === 'undefined' ? {} : _provCache;
}

function globalProviderRow(instance, data) {
  const id = String(instance.id);
  const name = instance.name || id;
  const initial = Array.from(name || '?')[0] || '?';
  const credentials = (data.credentials || []).filter(c => (c.providerIds || []).some(pid => String(pid) === id));
  const offerings = (data.offerings || []).filter(o => String(o.providerId || '') === id);
  const authMethods = [...new Set(credentials.map(c => c.authFlow || t(_globalCredentialKinds[c.kind] || c.kind)).filter(Boolean))];
  const managed = globalProviderCache()[id] || null;
  const priority = Number(instance.priority);
  return {
    instance: instance,
    id: id,
    name: name,
    initial: initial.toUpperCase(),
    kindLabel: instance.runtimeBuiltin ? '内置' : '自定义',
    isCustom: instance.deletable === true,
    enabled: !!instance.enabled,
    free: !!instance.freeTier,
    managed: managed,
    connections: managed ? ((managed.connectionIds || []).length || (managed.connectionId ? 1 : 0)) : 0,
    priority: Number.isFinite(priority) && priority > 0 ? priority : null,
    authMethods: authMethods,
    authText: authMethods.length ? authMethods.join(' · ') : t('未配置'),
    credentialsCount: credentials.length,
    offerings: offerings.slice().sort((a, b) => String(a.modelId).localeCompare(String(b.modelId), LC(), { numeric: true })),
    offeringsCount: offerings.length,
    baseURL: instance.baseURL || '',
  };
}

function globalProviderSortValue(row, key) {
  if (key === 'status') return row.enabled ? 1 : 0;
  if (key === 'type') return t(row.kindLabel);
  if (key === 'auth') return row.authText;
  if (key === 'credentials') return row.credentialsCount;
  if (key === 'offerings') return row.offeringsCount;
  if (key === 'priority') return row.priority == null ? Number.MAX_SAFE_INTEGER : row.priority;
  if (key === 'baseURL') return row.baseURL;
  return row.name;
}

function globalProviderSortRows(rows) {
  const key = _globalProviderSort || 'name';
  const numeric = key === 'status' || key === 'credentials' || key === 'offerings' || key === 'priority';
  const direction = _globalProviderSortDir === 'desc' ? -1 : 1;
  return rows.slice().sort((a, b) => {
    const av = globalProviderSortValue(a, key);
    const bv = globalProviderSortValue(b, key);
    let cmp;
    if (numeric) cmp = (Number(av) || 0) - (Number(bv) || 0);
    else cmp = String(av == null ? '' : av).localeCompare(String(bv == null ? '' : bv), LC(), { sensitivity: 'base', numeric: true });
    if (cmp === 0) cmp = a.name.localeCompare(b.name, LC(), { sensitivity: 'base' });
    return cmp * direction;
  });
}

function globalProviderSortHeader(key, label) {
  const active = _globalProviderSort === key;
  const dir = active ? _globalProviderSortDir : '';
  const cue = active ? (dir === 'desc' ? ' ▼' : ' ▲') : '';
  return '<th scope="col" aria-sort="' + (active ? (dir === 'desc' ? 'descending' : 'ascending') : 'none') + '">' +
    '<button type="button" class="mgmt-sort" onclick="setGlobalProviderSort(' + globalJS(key) + ')">' +
    esc(t(label)) + '<span class="mgmt-sort-cue" aria-hidden="true">' + cue + '</span></button></th>';
}

// ----- cells -----
function globalProviderNameHTML(row) {
  const tags =
    (row.free ? '<span class="green-xs" title="' + escAttr(t('整源免费标记，不代表池内每个模型都免费；逐模型计费以分级预览为准')) + '">free</span>' : '') +
    (row.connections ? '<span class="acc-xs" title="' + escAttr(t('连接池轮转派发')) + '">token×' + row.connections + '</span>' : '');
  return '<div class="mgmt-provider-cell"><span class="provider-monogram mgmt-monogram" aria-hidden="true">' + esc(row.initial) + '</span>' +
    '<div class="mgmt-primary"><span class="mgmt-name-line"><span class="mgmt-name" title="' + escAttr(row.name) + '">' + esc(row.name) + '</span>' + tags + '</span>' +
    '<code class="mgmt-id" title="' + escAttr(row.id) + '">' + esc(row.id) + '</code></div></div>';
}

function globalProviderStatusHTML(row) {
  const badge = globalBadge(row.enabled ? '已启用' : '已停用', row.enabled ? 'positive' : 'negative');
  if (!row.managed || !row.enabled) return badge;
  return badge + '<div class="prov-health">' + provMiniHealthHTML(row.managed) + '</div>';
}

function globalProviderTypeHTML(row) {
  return '<div class="mgmt-stack"><span>' + esc(t(row.kindLabel)) + '</span><small title="' + escAttr(row.authText) + '">' + esc(row.authText) + '</small></div>';
}

function globalProviderModelsToggleHTML(row) {
  if (!row.offeringsCount) return '<span class="mgmt-muted">0</span>';
  const open = !!_globalExpandedProviders[row.id];
  return '<button type="button" class="prov-models-toggle' + (open ? ' open' : '') + '" data-prov-toggle="' + escAttr(row.id) + '" aria-expanded="' + (open ? 'true' : 'false') + '"' +
    ' title="' + escAttr(t(open ? '收起' : '展开全部')) + '" onclick="toggleGlobalProviderModels(' + globalJS(row.id) + ')">' +
    '<span class="prov-models-chevron" aria-hidden="true">▸</span>' + formatNumber(row.offeringsCount) + '</button>';
}

function globalProviderCheckHTML(row) {
  return row.managed && row.enabled ? '<span class="prov-sync">' + provSyncCellHTML(row.id) + '</span>' : '<span class="hint-xs">—</span>';
}

function globalProviderActionsHTML(row) {
  const id = globalJS(row.id);
  const details = '<button type="button" class="btn btn-sm btn-primary" onclick="openGlobalProviderDetail(' + id + ')">' + esc(t('详情')) + '</button>';
  if (!row.isCustom) {
    return details + '<button type="button" class="btn btn-sm" onclick="setupProviderInstance(' + id + ')">' + esc(t('连接设置')) + '</button>';
  }
  const icon = (handler, label, glyph, extra) => '<button type="button" class="btn btn-sm btn-icon' + (extra ? ' ' + extra : '') + '" onclick="' + handler + '" title="' + escAttr(t(label)) +
    '" aria-label="' + escAttr(t(label)) + '">' + uiIcon(glyph) + '</button>';
  return details +
    icon('editProvider(' + id + ')', '编辑', 'edit') +
    icon('testProviderById(' + id + ',this)', '测试', 'bolt') +
    icon('recheckProvider(' + id + ',this)', '重新检测', 'reset') +
    icon('toggleProvider(' + id + ',' + (!row.enabled) + ',this)', row.enabled ? '停用' : '启用', row.enabled ? 'pause' : 'play') +
    icon('deleteProvider(' + id + ')', '删除', 'close', 'btn-danger');
}

// Inline test / re-check outcomes (written by testProviderById and recheckProvider, kept across re-renders).
function globalProviderResultHTML(row) {
  return '<span class="prov-row-test-out">' + provRowResultHTML('test', row.id) + '</span>' +
    '<span class="prov-row-recheck-out">' + provRowResultHTML('recheck', row.id) + '</span>';
}

// Expanded detail: the full address and every model this provider serves. A model the upstream
// knows under another ID shows "name → upstream ID".
function globalProviderExpandHTML(row) {
  const chips = row.offerings.map(o => {
    const mapped = o.upstreamModelId && o.upstreamModelId !== o.modelId ? o.upstreamModelId : '';
    const off = o.delisted || o.disabled;
    const hint = (o.qualifiedId || o.modelId) + (mapped ? ' → ' + mapped : '') + (o.delisted ? ' · ' + t('已下架') : (o.disabled ? ' · ' + t('已停用') : ''));
    return '<span class="model-tag chip-clip prov-chip' + (off ? ' prov-chip-off' : '') + '" title="' + escAttr(hint) + '">' + esc(o.modelId) +
      (mapped ? '<span class="prov-chip-up"> → ' + esc(mapped) + '</span>' : '') + '</span>';
  }).join('');
  return '<div class="prov-expand">' +
    '<div class="prov-expand-meta">' +
      '<span><b>Base URL</b> <code class="mgmt-mono">' + esc(row.baseURL || '—') + '</code></span>' +
      '<span><b>ID</b> <code class="mgmt-mono">' + esc(row.id) + '</code></span>' +
    '</div>' +
    '<div class="prov-expand-models" aria-label="' + escAttr(t('模型')) + '">' + (chips || '<span class="hint-sm">' + esc(t('暂无模型')) + '</span>') + '</div></div>';
}

// ----- table + compact list -----
const GLOBAL_PROVIDER_COLUMNS = 9;

function globalProviderTableRows(rows) {
  return rows.map(row => {
    const open = !!_globalExpandedProviders[row.id] && row.offeringsCount > 0;
    return '<tbody class="prov-group" data-prov="' + escAttr(row.id) + '">' +
      '<tr class="prov-row">' +
        '<td>' + globalProviderNameHTML(row) + '</td>' +
        '<td class="prov-status">' + globalProviderStatusHTML(row) + '</td>' +
        '<td>' + globalProviderTypeHTML(row) + '</td>' +
        '<td class="mgmt-num">' + formatNumber(row.credentialsCount) + '</td>' +
        '<td class="mgmt-num">' + globalProviderModelsToggleHTML(row) + '</td>' +
        '<td class="mgmt-num">' + (row.priority == null ? '<span class="hint-xs">—</span>' : esc(String(row.priority))) + '</td>' +
        '<td>' + globalProviderCheckHTML(row) + '</td>' +
        '<td><span class="mgmt-url mgmt-mono" title="' + escAttr(row.baseURL) + '">' + esc(row.baseURL || '—') + '</span></td>' +
        '<td><div class="mgmt-actions">' + globalProviderActionsHTML(row) + '</div></td>' +
      '</tr>' +
      '<tr class="prov-result-row"><td colspan="' + GLOBAL_PROVIDER_COLUMNS + '">' + globalProviderResultHTML(row) + '</td></tr>' +
      (open ? '<tr class="prov-expand-row"><td colspan="' + GLOBAL_PROVIDER_COLUMNS + '">' + globalProviderExpandHTML(row) + '</td></tr>' : '') +
    '</tbody>';
  }).join('');
}

function globalProviderCompactRows(rows) {
  return rows.map(row => {
    const open = !!_globalExpandedProviders[row.id] && row.offeringsCount > 0;
    const facts = [];
    if (row.priority != null) facts.push(esc(t('优先级')) + ' ' + esc(String(row.priority)));
    return '<article class="provider-compact-item" data-prov="' + escAttr(row.id) + '">' +
      '<div class="pc-line1"><span class="provider-monogram mgmt-monogram" aria-hidden="true">' + esc(row.initial) + '</span><strong title="' + escAttr(row.name) + '">' + esc(row.name) + '</strong>' +
        (row.free ? '<span class="green-xs">free</span>' : '') +
        globalBadge(row.enabled ? '已启用' : '已停用', row.enabled ? 'positive' : 'negative') + '</div>' +
      '<div class="pc-line2"><span title="' + escAttr(row.authText) + '">' + esc(t(row.kindLabel)) + ' · ' + esc(row.authText) + '</span>' +
        '<span>' + esc(t('凭据')) + ' ' + formatNumber(row.credentialsCount) + (facts.length ? ' · ' + facts.join(' · ') : '') + '</span></div>' +
      (row.managed && row.enabled ? '<div class="pc-line3">' + provMiniHealthHTML(row.managed) + globalProviderCheckHTML(row) + '</div>' : '') +
      '<div class="pc-line3"><span class="hint-xs">' + esc(t('模型授权')) + '</span> ' + globalProviderModelsToggleHTML(row) + '</div>' +
      '<div class="pc-actions"><div class="mgmt-actions">' + globalProviderActionsHTML(row) + '</div></div>' +
      '<div class="pc-result">' + globalProviderResultHTML(row) + '</div>' +
      (open ? globalProviderExpandHTML(row) : '') +
    '</article>';
  }).join('');
}

function renderGlobalInstances(data) {
  const el = _('globalInstances');
  if (!el) return;
  const instances = (data.instances || []).filter(globalProviderMatches);
  if (!instances.length) { el.innerHTML = globalEmpty(_globalProviderSearch || _globalProviderKind || _globalProviderStatus ? '暂无匹配 Provider' : '暂无 Provider'); return; }
  const rows = globalProviderSortRows(instances.map(i => globalProviderRow(i, data)));
  const headers =
    globalProviderSortHeader('name', 'Provider') +
    globalProviderSortHeader('status', '状态') +
    globalProviderSortHeader('type', '类型') +
    globalProviderSortHeader('credentials', '凭据') +
    globalProviderSortHeader('offerings', '模型授权') +
    globalProviderSortHeader('priority', '优先级') +
    '<th scope="col">' + esc(t('最近检测')) + '</th>' +
    globalProviderSortHeader('baseURL', 'Base URL') +
    '<th scope="col">' + esc(t('操作')) + '</th>';
  el.innerHTML = '<div class="mgmt-table-wrap provider-table-wrap"><table class="mgmt-table provider-table">' +
    '<thead><tr>' + headers + '</tr></thead>' + globalProviderTableRows(rows) + '</table></div>' +
    '<div class="provider-compact-list">' + globalProviderCompactRows(rows) + '</div>';
}

// Re-render after the managed-provider data (health, sync) changed.
function refreshProviderInventory() {
  if (_globalData) renderGlobalInstances(_globalData);
}

function toggleGlobalProviderModels(id) {
  id = String(id);
  if (_globalExpandedProviders[id]) delete _globalExpandedProviders[id];
  else _globalExpandedProviders[id] = true;
  refreshProviderInventory();
  // The button was re-created by the render; keep keyboard focus on its replacement.
  const next = Array.from(document.querySelectorAll('[data-prov-toggle]'))
    .find(button => button.dataset.provToggle === id && button.getClientRects().length);
  if (next) next.focus();
}

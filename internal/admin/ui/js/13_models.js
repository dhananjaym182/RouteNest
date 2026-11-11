// ========== 模型列表 ==========
let _cachedModels = [];
let _modelGroupOpen = {}; // 分组折叠状态（localStorage 持久化）
const _modelFilters = { search: '', provider: '', cost: '', status: '' };
const MODEL_GROUP_OPEN_KEY = 'cline_admin_model_groups_open';
const MODEL_FILTER_KEY = 'cline_admin_model_filters';
const MODEL_ROWS_PAGE = 120;       // 每个分组一次最多构建的行数，其余点「显示更多」
let _modelGroupRows = new Map();   // 分组 key -> 上次渲染的已排序行（折叠分组按需再构建）
let _modelGroupShown = {};         // 分组 key -> 已构建行数
let _modelFilterOpen = {};         // 有筛选时各分组的展开状态（仅本次筛选有效，不持久化）
let _modelSearchTimer = 0;
try {
  const _mgo = JSON.parse(localStorage.getItem(MODEL_GROUP_OPEN_KEY) || '{}');
  if (_mgo && typeof _mgo === 'object') _modelGroupOpen = _mgo;
} catch (e) {}

function modelProviderNameIndex() {
  const index = new Map();
  const offerings = typeof _globalData !== 'undefined' && _globalData ? _globalData.offerings || [] : [];
  for (const offering of offerings) {
    if (!offering.qualifiedId) continue;
    if (!index.has(offering.modelId)) index.set(offering.modelId, new Map());
    index.get(offering.modelId).set(offering.qualifiedId, offering);
  }
  return new Map(Array.from(index, ([id, names]) => [id,
    Array.from(names.values()).sort((a, b) => a.qualifiedId.localeCompare(b.qualifiedId)),
  ]));
}

function modelProviderNameButton(offering) {
  const name = offering.qualifiedId;
  const slash = name.indexOf('/');
  const label = t('复制模型名') + ': ' + name;
  // The provider's upstream expects a different ID than the name clients send.
  const mapped = offering.upstreamModelId && offering.upstreamModelId !== offering.modelId ? offering.upstreamModelId : '';
  return '<button type="button" class="model-provider-name" onclick="copyText(' + escAttr(JSON.stringify(name)) + ')"' +
    ' title="' + escAttr(label + (offering.providerName ? ' · ' + offering.providerName : '') + (mapped ? ' · ' + t('上游模型 ID') + ': ' + mapped : '')) + '" aria-label="' + escAttr(label + (mapped ? ' · ' + t('上游模型 ID') + ': ' + mapped : '')) + '">' +
    '<code><span class="model-provider-prefix">' + esc(name.slice(0, slash + 1)) + '</span><span class="model-provider-model">' + esc(name.slice(slash + 1)) + '</span></code>' +
    (mapped ? '<span class="model-provider-upstream" title="' + escAttr(t('上游模型 ID')) + '">→ ' + esc(mapped) + '</span>' : '') +
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg></button>';
}

// ===== 分组：一级 = 真实来源（Cline / Zen / Provider / 自定义），列表只按这一级渲染 =====
// mergedSourceKey：合并行的主来源，优先级 自定义 > Provider > zen > Cline
function mergedSourceKey(m) {
  if (m._customAny) return 'custom';
  if (m._sources.indexOf('provider') !== -1) return 'provider';
  if (m._sources.indexOf('zen') !== -1) return 'zen';
  return 'remote';
}

// 一级分组 key：src:cline / src:zen / src:prov:<name> / src:custom / src:other
function sourceGroupKey(m) {
  const src = mergedSourceKey(m);
  if (src === 'provider') return 'src:prov:' + ((m._provNames && m._provNames[0]) || m.provider || 'Provider');
  if (src === 'zen') return 'src:zen';
  if (src === 'custom') return 'src:custom';
  if (src === 'remote') return 'src:cline';
  return 'src:other';
}

// 模型所属的全部一级来源：一个模型可同时来自 Cline / Zen / 多个 Provider / 自定义。
// 筛选、分组计数与「全部启用/停用」都按这个集合，主来源 sourceGroupKey 只是其中之一。
function modelSourceKeys(m) {
  const keys = [];
  if (m._sources.indexOf('custom') !== -1 || m._customAny) keys.push('src:custom');
  if (m._sources.indexOf('provider') !== -1) {
    if (m._provNames && m._provNames.length) m._provNames.forEach(n => keys.push('src:prov:' + n));
    else keys.push('src:prov:' + (m.provider || 'Provider'));
  }
  if (m._sources.indexOf('zen') !== -1) keys.push('src:zen');
  if (m._sources.indexOf('remote') !== -1) keys.push('src:cline');
  return keys.length ? Array.from(new Set(keys)) : ['src:other'];
}

function sourceGroupLabel(key) {
  if (key === 'src:cline') return t('Cline');
  if (key === 'src:zen') return 'OpenCode Zen';
  if (key.indexOf('src:prov:') === 0) return key.slice(9);
  if (key === 'src:custom') return t('用户自定义');
  return t('其他');
}

function sourceSortKey(a, b) {
  const ord = k => k === 'src:cline' ? 0 : (k === 'src:zen' ? 1 : (k.indexOf('src:prov:') === 0 ? 2 : (k === 'src:custom' ? 3 : 4)));
  const oa = ord(a), ob = ord(b);
  if (oa !== ob) return oa - ob;
  return a.localeCompare(b);
}

// 模型 ID 前缀（旧二级厂商 key）：渲染已不再使用，仅保留给 mergedRowsForGroupKey 兼容
// 历史 localStorage 分组 key（形如 src:xxx/vendor），避免旧状态解析失效
function vendorKey(m) {
  const idx = (m.id || '').indexOf('/');
  if (idx > 0) return m.id.slice(0, idx);
  return m.provider || '__other';
}

function vendorLabel(key) { return key === '__other' ? t('其他') : key; }

function vendorSortKey(a, b) {
  if (a === '__other') return 1;
  if (b === '__other') return -1;
  return a.localeCompare(b);
}

function modelSource(m) {
  if (m.custom) return 'custom';
  if (m.source === 'provider') return 'provider';
  if (m.source === 'zen' || m.provider === 'opencode') return 'zen';
  return 'remote';
}
const MODEL_SOURCE_LABEL = { zen: 'OpenCode Zen', remote: 'Cline', provider: 'Provider' };

// mergeModelsById 同一模型 ID 可能同时被 Cline 与 OpenCode Zen 同步（池内多条目）：
// 合并为一行展示 —— 来源/计费取并集；delisted 区分「任一下架/全部下架」
// （任一来源仍在官方列表就不算整体下架）；启停开关按 ID 作用到所有来源条目。
function mergeModelsById(models) {
  const map = new Map();
  const providerNames = modelProviderNameIndex();
  for (const m of models) {
    const prev = map.get(m.id);
    if (!prev) {
      map.set(m.id, {
        id: m.id, provider: m.provider || '', context: m.context || 0, output: m.output || 0,
        // cost and tierSignal describe the ID, so keep the first non-empty value
        // for the inline metadata editor (the full provenance is in _costs).
        cost: m.cost || '', tierSignal: m.tierSignal || '',
        _sources: [modelSource(m)],
        _costs: [m.cost || 'free'],
        _provNames: (modelSource(m) === 'provider' && m.provider) ? [m.provider] : [],
        _delistedAny: !!m.delisted,
        _delistedAll: !!m.delisted,
        _customAny: !!m.custom,
        _disabled: !!m.disabled,
        _providerModelNames: providerNames.get(m.id) || [],
      });
      continue;
    }
    const src = modelSource(m);
    if (!prev.cost && m.cost) prev.cost = m.cost;
    if (!prev.tierSignal && m.tierSignal) prev.tierSignal = m.tierSignal;
    if (prev._sources.indexOf(src) === -1) prev._sources.push(src);
    if (prev._costs.indexOf(m.cost || 'free') === -1) prev._costs.push(m.cost || 'free');
    // 同一模型 ID 可被多个自定义 Provider 同步进来（同名不同源）：
    // 全部来源名字都亮出来，肉眼即可分辨同一模型的两个上游
    if (src === 'provider' && m.provider && prev._provNames.indexOf(m.provider) === -1) prev._provNames.push(m.provider);
    prev._delistedAny = prev._delistedAny || !!m.delisted;
    prev._delistedAll = prev._delistedAll && !!m.delisted;
    prev._customAny = prev._customAny || !!m.custom;
    prev._disabled = prev._disabled || !!m.disabled;
    if (!prev.provider && m.provider) prev.provider = m.provider;
    if ((m.context || 0) > prev.context) prev.context = m.context || 0;
    if ((m.output || 0) > prev.output) prev.output = m.output || 0;
  }
  return Array.from(map.values());
}

function modelFilterActive() {
  return !!(_modelFilters.search || _modelFilters.provider || _modelFilters.cost || _modelFilters.status);
}

function onModelFilterChange(key, value) {
  _modelFilters[key] = value;
  if (key === 'search') {
    // 逐字输入不立刻重绘整张列表
    clearTimeout(_modelSearchTimer);
    _modelSearchTimer = setTimeout(applyModelFilters, 160);
    return;
  }
  applyModelFilters();
}

function applyModelFilters() {
  clearTimeout(_modelSearchTimer);
  _modelGroupShown = {};
  _modelFilterOpen = {};
  persistModelFilters();
  renderModelList();
}

function clearModelFilters() {
  _modelFilters.search = _modelFilters.provider = _modelFilters.cost = _modelFilters.status = '';
  const search = _('modelSearch');
  if (search) search.value = '';
  const cost = _('modelFilterCost');
  if (cost) cost.value = '';
  applyModelFilters();
}

// 统计条上的数字就是快捷筛选：再点一次取消
function setModelStatusFilter(value) {
  _modelFilters.status = _modelFilters.status === value ? '' : value;
  applyModelFilters();
}

// 状态筛选走工具栏下拉（与来源/计费同款式）：''|enabled|disabled|failed|delisted

function modelMatchesFilters(m) {
  const searchable = [m.id].concat((m._providerModelNames || []).flatMap(o => [o.qualifiedId, o.providerName, o.upstreamModelId])).filter(Boolean).join(' ').toLowerCase();
  if (_modelFilters.search && !searchable.includes(_modelFilters.search.toLowerCase())) return false;
  if (_modelFilters.provider && modelSourceKeys(m).indexOf(_modelFilters.provider) === -1) return false;
  if (_modelFilters.cost && m._costs.indexOf(_modelFilters.cost) === -1) return false;
  if (_modelFilters.status === 'disabled' && !m._disabled) return false;
  if (_modelFilters.status === 'enabled' && m._disabled) return false;
  if (_modelFilters.status === 'failed' && !(_modelTestResults[m.id] && _modelTestResults[m.id].ok === false)) return false;
  if (_modelFilters.status === 'delisted' && !m._delistedAll) return false;
  return true;
}

// 工具栏「来源」筛选：按一级来源分组 key 生成选项
function buildProviderOptions(merged) {
  const sel = _('modelFilterProvider');
  if (!sel) return;
  const counts = new Map();
  merged.forEach(m => modelSourceKeys(m).forEach(k => counts.set(k, (counts.get(k) || 0) + 1)));
  const keys = Array.from(counts.keys()).sort(sourceSortKey);
  // 已保存的来源筛选对应的 Provider 被移除后，不能让列表一直为空
  if (_modelFilters.provider && !counts.has(_modelFilters.provider)) { _modelFilters.provider = ''; persistModelFilters(); }
  const cur = _modelFilters.provider;
  sel.innerHTML = '<option value="">' + t('全部来源') + '</option>' +
    keys.map(k => '<option value="' + escAttr(k) + '"' + (k === cur ? ' selected' : '') + '>' + esc(sourceGroupLabel(k)) + ' (' + counts.get(k) + ')</option>').join('');
}

// 「状态」筛选下拉：与来源/计费同款样式，选项带全量口径的计数
const MODEL_STATUS_OPTS = [['enabled', '已启用'], ['disabled', '已停用'], ['failed', '测试失败'], ['delisted', '已下架']];
function modelStatusCount(merged, v) {
  if (v === 'enabled') return merged.filter(m => !m._disabled).length;
  if (v === 'disabled') return merged.filter(m => m._disabled).length;
  if (v === 'failed') return merged.filter(m => _modelTestResults[m.id] && _modelTestResults[m.id].ok === false).length;
  if (v === 'delisted') return merged.filter(m => m._delistedAll).length;
  return merged.length;
}
function buildStatusOptions(merged) {
  const sel = _('modelFilterStatus');
  if (!sel) return;
  const cur = _modelFilters.status;
  sel.innerHTML = '<option value="">' + esc(t('全部状态')) + ' (' + merged.length + ')</option>' +
    MODEL_STATUS_OPTS.map(([v, label]) =>
      '<option value="' + v + '"' + (v === cur ? ' selected' : '') + '>' + esc(t(label)) + ' (' + modelStatusCount(merged, v) + ')</option>').join('');
}

function renderModelStats(merged) {
  buildStatusOptions(merged);
  const bar = _('modelStatsBar');
  if (!merged.length) {
    if (bar) bar.innerHTML = '';
    return;
  }
  const disabled = merged.filter(m => m._disabled).length;
  const delisted = merged.filter(m => m._delistedAll).length;
  const failed = modelStatusCount(merged, 'failed');
  if (!bar) return;
  // 数字即快捷筛选（与「状态」下拉联动）；再点一次取消
  const stat = (value, dot, label, count) =>
    '<button type="button" class="model-stat' + (_modelFilters.status === value ? ' active' : '') + '" aria-pressed="' + (_modelFilters.status === value) + '"' +
      ' onclick="setModelStatusFilter(\'' + value + '\')" title="' + escAttr(t('点击筛选')) + '">' +
      (dot ? '<span class="dot ' + dot + '"></span>' : '') + esc(t(label)) + ' <b>' + count + '</b></button>';
  bar.innerHTML =
    '<span>' + t('共') + ' <b>' + merged.length + '</b> ' + t('个模型') + '</span>' +
    stat('enabled', 'g', '已启用', merged.length - disabled) +
    stat('disabled', 'y', '已停用', disabled) +
    (delisted ? stat('delisted', 'r', '已下架', delisted) : '') +
    (failed ? stat('failed', 'r', '测试失败', failed) : '');
}

// 分组内只显示该分组自己的 Provider 别名（如 nvidia 分组只列 nvidia 的名字）；
// 同一模型在其它来源下的别名，在那个来源的分组里看。解析不出 Provider 时回退为全部。
function modelGroupProviderIDs(key) {
  if (!key || key === 'src:other') return null;
  const ids = modelGroupProviders(key).map(p => p.id);
  return ids.length ? new Set(ids) : null;
}

// scope 省略 = 合并视图，列出全部 Provider 别名
function renderModelRow(m, scope) {
  const argument = value => escAttr(JSON.stringify(value));
  const names = scope ? (m._providerModelNames || []).filter(o => scope.has(o.providerId)) : (m._providerModelNames || []);
  let tags = '';
  const hasFree = m._costs.indexOf('free') !== -1;
  const hasPass = m._costs.indexOf('pass') !== -1;
  if (hasFree && hasPass) {
    tags += '<span class="model-tag free">' + t('免费') + '</span><span class="model-tag pass">' + t('付费') + '</span>';
  } else if (hasFree) {
    tags += '<span class="model-tag free">' + t('免费') + '</span>';
  } else {
    tags += '<span class="model-tag pass">' + t('付费') + '</span>';
  }
  // Per-row tier provenance: surface the classifier signal that decided the
  // cost, matching the provider import preview (manual tiering shows no badge).
  if (m.tierSignal && m.tierSignal !== 'manual' && _tierSignals[m.tierSignal]) {
    const sigLabel = t(_tierSignals[m.tierSignal]);
    tags += '<span class="model-source-badge model-tier-signal" title="' + escAttr(t('自动判定层级') + ': ' + sigLabel) + '" aria-label="' + escAttr(sigLabel) + '"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7v1"/></svg></span>';
  }
  if (m._delistedAll) {
    tags += '<span class="delisted-badge" title="' + t('已从上游官方列表移除，实测可能仍可用；同步只标记不删除，上游报模型不存在时自动清理') + '">' + t('已下架') + '</span>';
  }
  for (const s of m._sources) {
    if (s === 'custom') tags += '<span class="model-source-badge custom">' + t('自定义') + '</span>';
    else if (s === 'provider') {
      // 别名标识： Provider 来源的 badge 显示真实名称（多源同名模型全部列出）
      const n = (m._provNames && m._provNames.length) ? m._provNames.join(' · ') : (m.provider || MODEL_SOURCE_LABEL[s]);
      tags += '<span class="model-source-badge provider" title="' + escAttr(n) + '">' + esc(n) + '</span>';
    }
    else tags += '<span class="model-source-badge ' + s + '">' + MODEL_SOURCE_LABEL[s] + '</span>';
  }
  // 所有模型都开放上下文/最大输出编辑：Cline 模型不在已知硬限制表时，
  // 手动设置的 Output 会作为 modelMaxOutputLimit 的封顶值生效
  const prevTest = _modelTestResults[m.id] || null;
  let actions =
    '<button class="btn btn-sm btn-xs" data-focus="test" onclick="testModelRow(' + argument(m.id) + ', this)" title="' + t('测试') + '" aria-label="' + escAttr(t('测试') + ': ' + m.id) + '">' + t('测试') + '</button>' +
    '<button class="btn btn-sm btn-xs" data-focus="edit" onclick="editModelMeta(' + [m.id, m.context, m.output, m.cost || '', m.tierSignal || ''].map(argument).join(',') + ')" title="' + t('上下文窗口 / 最大输出 (token)，点击⚙修改') + '" aria-label="' + escAttr(t('编辑上下文与输出') + ': ' + m.id) + '">⚙</button>';
  if (m._customAny || m._delistedAny) {
    actions += '<button class="btn btn-sm btn-danger btn-xs" data-focus="delete" onclick="deleteModel(' + argument(m.id) + ')" title="' + (m._delistedAny ? t('移除（已下架模型支持手动移除）') : t('删除')) + '" aria-label="' + escAttr((m._delistedAny ? t('移除（已下架模型支持手动移除）') : t('删除')) + ': ' + m.id) + '">'+uiIcon('close')+'</button>';
  }
  return '<div class="model-row' + (m._disabled ? ' disabled-row' : '') + '" data-model-id="' + escAttr(m.id) + '">' +
    '<label class="switch model-col-switch" title="' + (m._disabled ? t('点击启用该模型') : t('点击停用该模型')) + '"><input type="checkbox" data-focus="switch" aria-label="' + escAttr(t('启用模型') + ': ' + m.id) + '"' + (m._disabled ? '' : ' checked') + ' onchange="toggleModel(' + argument(m.id) + ', !this.checked)"><span class="slider"></span></label>' +
    '<div class="model-row-main' + (names.length > 1 ? ' has-many-provider-names' : '') + '"><div class="model-row-names"><span class="model-row-id mono" title="' + escAttr(m.id) + '">' + esc(m.id) + '</span>' +
      (names.length ? '<div class="model-provider-names" role="group" aria-label="' + escAttr(t('Provider 模型名')) + '">' + names.map(modelProviderNameButton).join('') + '</div>' : '') +
      '</div>' +
      (tags || (m._providerModelNames && m._providerModelNames.length) ? '<span class="model-row-tags">' + tags +
        (m._providerModelNames && m._providerModelNames.length ? '<button type="button" class="model-link-btn" data-focus="offerings" onclick="showModelOfferings(' + argument(m.id) + ')" title="' + escAttr(t('在「Provider 模型授权」中查看')) + '" aria-label="' + escAttr(t('查看 Provider 授权') + ': ' + m.id) + '">' + esc(t('查看授权')) + '</button>' : '') +
        '</span>' : '') + '</div>' +
    '<div class="model-row-cell mono" title="' + t('上下文') + '">' + (m.context ? formatTokenCount(m.context) : '—') + '</div>' +
    '<div class="model-row-cell mono" title="' + t('输出') + '">' + (m.output ? formatTokenCount(m.output) : '—') + '</div>' +
    '<div class="model-row-test"' + (prevTest ? ' data-tested="1"' : '') + '>' + modelTestCellHTML(m.id, prevTest) + '</div>' +
    '<div class="model-row-actions">' + actions + '</div>' +
  '</div>';
}

// ========== 模型行内测试（/models/test：安全预算小探测，服务端按模型串行化） ==========
// 结果按模型 ID 回记并持久化（localStorage），筛选/折叠重渲染与刷新页面后
// 仍可回显上次测试结论，「测试失败」状态筛选也依赖这份数据
const _modelTestResults = {}; // id -> { ok, text }
const MODEL_TEST_STORE_KEY = 'cline_admin_model_tests';
const MODEL_TEST_TTL_MS = 7 * 24 * 3600 * 1000; // 测试结论保留 7 天，过期不再计入「测试失败」
try {
  const _savedTests = JSON.parse(localStorage.getItem(MODEL_TEST_STORE_KEY) || '{}');
  if (_savedTests && typeof _savedTests === 'object') {
    const now = Date.now();
    let pruned = false;
    for (const k in _savedTests) {
      const v = _savedTests[k];
      if (!v || typeof v !== 'object') { pruned = true; continue; }
      if (!v.at) { v.at = now; pruned = true; }              // 旧版本没有时间戳：从现在起算
      if (now - v.at > MODEL_TEST_TTL_MS) { pruned = true; continue; }
      _modelTestResults[k] = v;
    }
    if (pruned) persistModelTests();
  }
} catch (e) {}

// 测试列：失败原因做成按钮，触屏与读屏无需悬停就能读到完整原因
function modelTestCellHTML(id, r) {
  if (!r) return '<span class="model-untested">—</span>';
  const age = r.at && typeof fmtAgo === 'function' ? ' · ' + t('测试于') + ' ' + fmtAgo(r.at) : '';
  if (r.ok) return '<span title="' + escAttr(r.text + age) + '">' + renderTestResultHTML(true, r.text) + '</span>';
  return '<button type="button" class="model-test-fail" data-focus="fail" onclick="showModelTestDetail(' + escAttr(JSON.stringify(id)) + ')" title="' + escAttr(r.text + age) + '" aria-label="' + escAttr(t('测试失败') + ': ' + id + ' · ' + r.text) + '">' + renderTestResultHTML(false, r.text) + '</button>';
}

function showModelTestDetail(id) {
  const r = _modelTestResults[id];
  if (r) toastError(id + ': ' + r.text);
}
function persistModelTests() {
  try { localStorage.setItem(MODEL_TEST_STORE_KEY, JSON.stringify(_modelTestResults)); } catch (e) {}
}

async function testModelRow(id, btn) {
  if (btn && btn.disabled) return; // 行级防重入：等待完成前忽略再次点击
  const row = btn && btn.closest ? btn.closest('.model-row') : null;
  const out = row ? row.querySelector('.model-row-test') : null;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>'; }
  if (out) out.innerHTML = '<span class="mut"><span class="loading"></span></span>';
  try {
    const d = await api('POST', '/models/test', { model: id });
    const r = d.data || {};
    const text = r.ok ? ((r.upstream || '') + ' · ' + (r.durationMs || 0) + 'ms') : (tx(r.error) || t('失败'));
    _modelTestResults[id] = { ok: !!r.ok, text: text, at: Date.now() };
    if (out) out.innerHTML = modelTestCellHTML(id, _modelTestResults[id]);
  } catch (e) {
    const text = friendlyTestError(e);
    _modelTestResults[id] = { ok: false, text: text, at: Date.now() };
    if (out) out.innerHTML = modelTestCellHTML(id, _modelTestResults[id]);
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
    persistModelTests();
    // 重绘以同步分组头的失败数与「测试失败」筛选，并保持焦点在原来的按钮上
    renderModelListKeepFocus();
  }
}

// 列表排序（表头点击）：id/ctx/out/test 四列；同列再点翻转方向
let _modelSort = { key: 'id', dir: 1 };
function setModelSort(key) {
  if (_modelSort.key === key) _modelSort.dir = -_modelSort.dir;
  else _modelSort = { key: key, dir: 1 };
  _modelGroupShown = {};
  persistModelFilters();
  renderModelListKeepFocus();
}

// 来源 / 计费 / 状态筛选与排序随浏览器保存；搜索词只在本次有效
function persistModelFilters() {
  try {
    localStorage.setItem(MODEL_FILTER_KEY, JSON.stringify({
      provider: _modelFilters.provider, cost: _modelFilters.cost, status: _modelFilters.status, sort: _modelSort,
    }));
  } catch (e) {}
}
try {
  const _savedFilters = JSON.parse(localStorage.getItem(MODEL_FILTER_KEY) || '{}');
  if (_savedFilters && typeof _savedFilters === 'object') {
    if (typeof _savedFilters.provider === 'string') _modelFilters.provider = _savedFilters.provider;
    if (['free', 'pass'].indexOf(_savedFilters.cost) !== -1) _modelFilters.cost = _savedFilters.cost;
    if (['enabled', 'disabled', 'failed', 'delisted'].indexOf(_savedFilters.status) !== -1) _modelFilters.status = _savedFilters.status;
    const sort = _savedFilters.sort;
    if (sort && ['id', 'ctx', 'out', 'test'].indexOf(sort.key) !== -1) _modelSort = { key: sort.key, dir: sort.dir === -1 ? -1 : 1 };
  }
} catch (e) {}
function modelSortCmp(a, b) {
  let r = 0;
  if (_modelSort.key === 'ctx') r = (a.context - b.context) || a.id.localeCompare(b.id);
  else if (_modelSort.key === 'out') r = (a.output - b.output) || a.id.localeCompare(b.id);
  else if (_modelSort.key === 'test') {
    const sa = !_modelTestResults[a.id] ? 2 : (_modelTestResults[a.id].ok ? 0 : 1);
    const sb = !_modelTestResults[b.id] ? 2 : (_modelTestResults[b.id].ok ? 0 : 1);
    r = (sa - sb) || a.id.localeCompare(b.id);
  } else r = a.id.localeCompare(b.id);
  return r * _modelSort.dir;
}

function modelColHead() {
  const col = (key, label) =>
    '<button type="button" class="model-col-sort' + (_modelSort.key === key ? ' active' : '') + '" data-sort="' + key + '"' +
      ' onclick="setModelSort(\'' + key + '\')" title="' + t('点击排序') + '">' +
      esc(t(label)) + (_modelSort.key === key ? ' <span class="model-col-arrow">' + (_modelSort.dir > 0 ? '▲' : '▼') + '</span>' : '') +
    '</button>';
  return '<div class="model-col-head">' +
    '<span class="model-col-switch"></span>' +
    '<span>' + col('id', '模型') + '</span>' +
    '<span>' + col('ctx', '上下文') + '</span>' +
    '<span>' + col('out', '输出') + '</span>' +
    '<span>' + col('test', '测试') + '</span>' +
    '<span class="model-col-actions">' + esc(t('操作')) + '</span>' +
  '</div>';
}

function syncModelToolbarState() {
  const search = _('modelSearch');
  if (search && search.value !== _modelFilters.search) search.value = _modelFilters.search;
  const cost = _('modelFilterCost');
  if (cost && cost.value !== _modelFilters.cost) cost.value = _modelFilters.cost;
  const clear = _('modelClearFilters');
  if (clear) clear.hidden = !modelFilterActive();
  const syncAll = _('syncAllModelsBtn');
  if (syncAll) syncAll.hidden = !modelSyncCanRemove();
}

// 分组体按需构建：折叠的分组不生成行；一次最多 MODEL_ROWS_PAGE 行
function modelGroupBodyHTML(key) {
  const rows = _modelGroupRows.get(key) || [];
  const shown = Math.max(MODEL_ROWS_PAGE, _modelGroupShown[key] || 0);
  const scope = modelGroupProviderIDs(key);
  let html = rows.slice(0, shown).map(m => renderModelRow(m, scope)).join('');
  if (rows.length > shown) {
    html += '<div class="model-more-row"><button type="button" class="btn btn-sm" data-focus="more" onclick="showMoreModelRows(this)">' +
      esc(t('显示更多')) + ' (' + (rows.length - shown) + ')</button></div>';
  }
  return html;
}

function showMoreModelRows(btn) {
  const body = btn && btn.closest ? btn.closest('.model-group-body') : null;
  if (!body) return;
  const key = body.dataset.key || '';
  const before = Math.max(MODEL_ROWS_PAGE, _modelGroupShown[key] || 0);
  _modelGroupShown[key] = before + MODEL_ROWS_PAGE;
  body.innerHTML = modelGroupBodyHTML(key);
  // 焦点落在第一条新增的行，键盘与读屏用户不会被甩回页首
  const rows = body.querySelectorAll('.model-row');
  const target = rows[before] && rows[before].querySelector('[data-focus="switch"]');
  if (target) target.focus();
}

// 模型管理列表渲染：单级分组（来源 / 提供商）+ 表头排序 + 工具栏筛选；
// 一个模型出现在它所属的每个来源下（计数、筛选、批量操作口径一致）；
// 分组默认折叠，折叠分组不构建行，展开时再生成
function renderModelList() {
  const listEl = _('modelsList');
  if (!listEl) return;
  const merged = mergeModelsById(_cachedModels);
  renderModelStats(merged);
  buildProviderOptions(merged);
  syncModelToolbarState();
  if (!merged.length) {
    listEl.innerHTML = listEmptyState(ICON_MODELS, t('暂无模型'),
      '<button type="button" class="btn btn-sm btn-primary" onclick="openProviderSetup()">' + t('连接 Provider') + '</button> ' +
      '<button type="button" class="btn btn-sm" onclick="syncAllModels()">' + t('同步全部模型') + '</button>');
    return;
  }
  const filtered = merged.filter(modelMatchesFilters);
  if (!filtered.length) {
    listEl.innerHTML = listEmptyState(ICON_MODELS, t('无匹配模型'),
      modelFilterActive() ? '<button type="button" class="btn btn-sm" onclick="clearModelFilters()">' + t('清除筛选') + '</button>' : '');
    return;
  }
  const sources = new Map(); // sourceKey -> rows[]
  for (const m of filtered) {
    for (const sk of (_modelFilters.provider ? [_modelFilters.provider] : modelSourceKeys(m))) {
      if (!sources.has(sk)) sources.set(sk, []);
      sources.get(sk).push(m);
    }
  }
  const canProviderAdmin = modelSyncCanRemove();
  const groupActions = key =>
    '<span class="model-group-actions" onclick="event.stopPropagation()">' +
      '<button class="btn" onclick="toggleModelsBatchEl(this, false)">' + t('全部启用') + '</button>' +
      '<button class="btn" onclick="toggleModelsBatchEl(this, true)">' + t('全部停用') + '</button>' +
      (canProviderAdmin && modelGroupSyncable(key) ?
        '<button type="button" class="btn" data-model-sync="' + escAttr(key) + '" onclick="syncModelGroup(this)">' + t('同步') + '</button>' : '') +
      (canProviderAdmin && key !== 'src:other' ?
        '<button type="button" class="btn" data-model-provider-action="edit" onclick="openModelProviderAction(this, &quot;edit&quot;)">' + t('编辑 Provider') + '</button>' +
        '<button type="button" class="btn btn-danger" data-model-provider-action="remove" onclick="openModelProviderAction(this, &quot;remove&quot;)">' + t('移除模型') + '</button>' : '') +
    '</span>';
  const groupStats = rows => {
    let en = 0, fail = 0;
    for (const m of rows) {
      if (!m._disabled) en++;
      if (_modelTestResults[m.id] && _modelTestResults[m.id].ok === false) fail++;
    }
    return { en, fail };
  };
  // 组头 = 一个真正的按钮（键盘可达、aria-expanded）+ 并列的操作按钮；点击组头空白处同样折叠
  const groupHead = (key, label, rows, open) => {
    const st = groupStats(rows);
    const name = label + ', ' + st.en + '/' + rows.length + ' ' + t('已启用') + (st.fail ? ', ' + st.fail + ' ' + t('测试失败') : '');
    return '<div class="model-group-head model-group-src-head' + (open ? ' expanded' : '') + '" data-key="' + escAttr(key) + '" data-provider-ids="' + escAttr(JSON.stringify(modelGroupProviders(key).map(p => p.id))) + '" onclick="toggleModelGroupEl(this)">' +
      '<button type="button" class="model-group-toggle" aria-expanded="' + (open ? 'true' : 'false') + '" aria-label="' + escAttr(name) + '" title="' + escAttr(t('点击展开/折叠')) + '">' +
        '<span class="model-group-caret">' + uiIcon('caretRight') + '</span>' +
        '<span class="model-group-label">' + esc(label) + '</span>' +
        '<span class="model-group-count" title="' + escAttr(t('已启用') + ' / ' + t('全部')) + '">' + st.en + '/' + rows.length + '</span>' +
        (st.fail ? '<span class="model-group-fail">' + st.fail + ' ' + t('测试失败') + '</span>' : '') +
      '</button>' +
      groupActions(key) +
    '</div>';
  };
  const filtering = modelFilterActive();
  _modelGroupRows = new Map();
  listEl.innerHTML = modelColHead() + Array.from(sources.keys()).sort(sourceSortKey).map(sk => {
    const rows = sources.get(sk).slice().sort(modelSortCmp);
    _modelGroupRows.set(sk, rows);
    // 有筛选时命中的分组默认展开（本次筛选内可手动折叠）；没有筛选时按已保存的状态
    const sOpen = filtering ? _modelFilterOpen[sk] !== false : _modelGroupOpen[sk] === true;
    return '<div class="model-group model-group-src">' +
      groupHead(sk, sourceGroupLabel(sk), rows, sOpen) +
      '<div class="model-group-body" data-key="' + escAttr(sk) + '" data-rendered="' + (sOpen ? '1' : '0') + '" style="display:' + (sOpen ? 'block' : 'none') + '">' +
        (sOpen ? modelGroupBodyHTML(sk) : '') + '</div>' +
    '</div>';
  }).join('');
}

// 重绘后把焦点还给原来的控件（开关、测试按钮、列头排序等），键盘与读屏用户不会掉回页首
function renderModelListKeepFocus() {
  const active = typeof document !== 'undefined' ? document.activeElement : null;
  const inList = !!(active && active.closest && active.closest('#modelsList'));
  const row = inList ? active.closest('.model-row') : null;
  const body = inList ? active.closest('.model-group-body') : null;
  const token = row && body && active.dataset ? { group: body.dataset.key, id: row.dataset.modelId, role: active.dataset.focus || 'switch' } : null;
  const sortKey = inList && active.dataset && active.dataset.sort ? active.dataset.sort : '';
  renderModelList();
  if (token) {
    const nb = Array.from(document.querySelectorAll('#modelsList .model-group-body')).find(b => b.dataset.key === token.group);
    const nr = nb && Array.from(nb.querySelectorAll('.model-row')).find(r => r.dataset.modelId === token.id);
    const target = nr && (nr.querySelector('[data-focus="' + token.role + '"]') || nr.querySelector('[data-focus="switch"]'));
    if (target) target.focus();
  } else if (sortKey) {
    const btn = document.querySelector('#modelsList .model-col-sort[data-sort="' + sortKey + '"]');
    if (btn) btn.focus();
  }
}

// 展开 / 折叠分组：有筛选时记在本次筛选里，没有筛选时才持久化
function setModelGroupOpen(head, open) {
  head.classList.toggle('expanded', open);
  const toggle = head.querySelector ? head.querySelector('.model-group-toggle') : null;
  if (toggle) toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  const body = head.nextElementSibling; // 直接相邻的 .model-group-body
  if (body && body.classList.contains('model-group-body')) {
    if (open && body.dataset.rendered !== '1') {
      body.innerHTML = modelGroupBodyHTML(head.dataset.key || '');
      body.dataset.rendered = '1';
    }
    body.style.display = open ? 'block' : 'none';
  }
}

function rememberModelGroupOpen(key, open) {
  if (modelFilterActive()) { _modelFilterOpen[key] = open; return; }
  _modelGroupOpen[key] = open;
  try { localStorage.setItem(MODEL_GROUP_OPEN_KEY, JSON.stringify(_modelGroupOpen)); } catch (e) {}
}

function setAllModelGroups(open) {
  _setToolbarMoreOpen(false);
  document.querySelectorAll('#modelsList .model-group-head').forEach(h => {
    setModelGroupOpen(h, open);
    rememberModelGroupOpen(h.dataset.key || '', open);
  });
}

function toggleModelGroupEl(head) {
  if (!head) return;
  const open = !head.classList.contains('expanded');
  setModelGroupOpen(head, open);
  rememberModelGroupOpen(head.dataset.key || '', open);
}

// ===== 启用 / 停用：先改本地缓存并立即重绘，再按顺序提交；失败时回滚 =====
// 不再每次切换都重新拉取 5 个全局接口 + 模型目录 + 配置
let _modelToggleChain = Promise.resolve();
function setModelsDisabledLocal(ids, disabled) {
  const set = new Set(ids);
  const prev = [];
  _cachedModels.forEach(m => { if (set.has(m.id)) { prev.push([m, !!m.disabled]); m.disabled = disabled; } });
  const offerings = typeof _globalData !== 'undefined' && _globalData ? _globalData.offerings || [] : [];
  offerings.forEach(o => { if (set.has(o.modelId)) { prev.push([o, !!o.disabled]); o.disabled = disabled; } });
  return () => prev.forEach(entry => { entry[0].disabled = entry[1]; });
}

async function commitModelToggle(ids, disabled) {
  const undo = setModelsDisabledLocal(ids, disabled);
  renderModelListKeepFocus();
  const run = _modelToggleChain.then(() => api('POST', '/models/toggle', { ids: ids, disabled: disabled }));
  _modelToggleChain = run.catch(() => {});
  try {
    await run;
    return true;
  } catch (e) {
    undo();
    renderModelListKeepFocus();
    toast(t('保存失败: ') + e.message, 'error');
    return false;
  }
}

// 后端在停用当前默认模型时会清空默认；默认模型下拉需跟着刷新
function refreshDefaultModelSetting() {
  if (_('settingDefModel')) loadConfig();
}

async function toggleModel(id, disabled) {
  if (!(await commitModelToggle([id], disabled))) return;
  toast((disabled ? t('已停用') : t('已启用')) + ': ' + id, 'success');
  refreshDefaultModelSetting();
}

// 批量启停「当前筛选结果」：与按分组批量共用 /models/toggle 批量接口
async function bulkToggleFiltered(disabled) {
  _setToolbarMoreOpen(false);
  const ids = mergeModelsById(_cachedModels).filter(modelMatchesFilters).map(m => m.id);
  if (!ids.length) { toast(t('没有符合条件的模型'), 'info'); return; }
  const confirmMsg = disabled ? t('确定停用当前筛选的全部模型？') : t('确定启用当前筛选的全部模型？');
  if (!(await showConfirm(confirmMsg + ' (' + ids.length + ')'))) return;
  const btn = disabled ? _('bulkDisableFilteredBtn') : _('bulkEnableFilteredBtn');
  if (btn) btn.disabled = true;
  const ok = await commitModelToggle(ids, disabled);
  if (btn) btn.disabled = false;
  if (!ok) return;
  toast((disabled ? t('已停用') : t('已启用')) + ' ' + ids.length + ' ' + t('个模型'), 'success');
  refreshDefaultModelSetting();
}

// 将分组 key 解析为合并行集合：src:xxx 匹配属于该来源的全部模型（含同时来自其他来源的）
function mergedRowsForGroupKey(key) {
  const merged = mergeModelsById(_cachedModels);
  if (!key || key.indexOf('src:') !== 0) return merged.filter(m => vendorKey(m) === key);
  const direct = merged.filter(m => modelSourceKeys(m).indexOf(key) !== -1);
  const slash = key.indexOf('/');
  if (direct.length || slash === -1) return direct;
  // 旧的「来源/厂商」二级 key
  const srcKey = key.slice(0, slash);
  const vKey = key.slice(slash + 1);
  return merged.filter(m => modelSourceKeys(m).indexOf(srcKey) !== -1 && vendorKey(m) === vKey);
}

// 分组按钮入口：从按钮所在分组头读取 key（两级通用）
function toggleModelsBatchEl(btn, disabled) {
  const head = btn && btn.closest ? btn.closest('.model-group-head') : null;
  if (!head) return;
  toggleModelsBatch(head.dataset.key || '', disabled);
}

async function toggleModelsBatch(groupKey, disabled) {
  const ids = mergedRowsForGroupKey(groupKey).map(m => m.id);
  if (!ids.length) return;
  const confirmMsg = disabled ? t('确定停用该分组全部模型？') : t('确定启用该分组全部模型？');
  if (!(await showConfirm(confirmMsg + ' (' + ids.length + ')'))) return;
  if (!(await commitModelToggle(ids, disabled))) return;
  toast((disabled ? t('已停用') : t('已启用')) + ' ' + ids.length + ' ' + t('个模型'), 'success');
  refreshDefaultModelSetting();
}

// 免费导向的批量开关：一次作用于全部合并行（不受工具栏筛选影响）
async function bulkModelToggle(mode) {
  _setToolbarMoreOpen(false);
  const merged = mergeModelsById(_cachedModels);
  let rows, disabled, btn, confirmMsg;
  if (mode === 'free') {
    rows = merged.filter(m => m._costs.indexOf('free') !== -1);
    disabled = false;
    btn = _('bulkEnableFreeBtn');
    confirmMsg = t('确定启用全部免费模型？');
  } else {
    // 停用付费：仅纯付费模型（free+pass 混合的保持启用，优先保免费）
    rows = merged.filter(m => m._costs.indexOf('pass') !== -1 && m._costs.indexOf('free') === -1);
    disabled = true;
    btn = _('bulkDisablePaidBtn');
    confirmMsg = t('确定停用全部付费模型？');
  }
  const ids = rows.map(m => m.id);
  if (!ids.length) { toast(t('没有符合条件的模型'), 'info'); return; }
  if (!(await showConfirm(confirmMsg + ' (' + ids.length + ')'))) return;
  if (btn) btn.disabled = true;
  const ok = await commitModelToggle(ids, disabled);
  if (btn) btn.disabled = false;
  if (!ok) return;
  toast((disabled ? t('已停用') : t('已启用')) + ' ' + ids.length + ' ' + t('个模型'), 'success');
  refreshDefaultModelSetting();
}

// ===== 模型工具栏「更多」溢出菜单：开关 + 点击外部收起（vanilla，无外部库） =====
function _setToolbarMoreOpen(open) {
  const more = _('modelBulkMore'), btn = _('toolbarMoreBtn');
  if (more) more.classList.toggle('open', open);
  if (btn) btn.setAttribute('aria-expanded', open ? 'true' : 'false');
  if (more && open) _alignToolbarMoreMenu(more);
}
// 菜单默认贴按钮右缘向左展开；按钮靠近左侧（工具栏换行后）放不下时改为贴左缘向右展开，
// 这样菜单总是接在按钮正下方，不会伸出视口
function _alignToolbarMoreMenu(more) {
  more.classList.remove('align-left');
  const menu = more.querySelector ? more.querySelector('.toolbar-more-menu') : null;
  if (!menu || !more.getBoundingClientRect) return;
  const width = menu.offsetWidth || 160;
  if (more.getBoundingClientRect().right - width < 8) more.classList.add('align-left');
}
function toggleToolbarMore(ev) {
  ev.stopPropagation();
  const more = _('modelBulkMore');
  if (more) _setToolbarMoreOpen(!more.classList.contains('open'));
}
document.addEventListener('click', e => {
  const more = _('modelBulkMore');
  if (more && more.classList.contains('open') && !more.contains(e.target)) _setToolbarMoreOpen(false);
});
// Esc 收起菜单并把焦点还给按钮
document.addEventListener('keydown', e => {
  if (e.key !== 'Escape') return;
  const more = _('modelBulkMore');
  if (!more || !more.classList.contains('open')) return;
  _setToolbarMoreOpen(false);
  const btn = _('toolbarMoreBtn');
  if (btn) btn.focus();
});

// 修改模型上下文/最大输出：压缩阈值按此计算，设置后 zen 同步保留该值
let _mmTierWasManual = false;
function showModelMetaEditor(id, ctx, out, cost, tierSignal) {
  _('mmId').value = id;
  _('mmCtx').value = ctx || 200000;
  _('mmOut').value = out || 32768;
  const tier = _('mmTier');
  _mmTierWasManual = tierSignal === 'manual';
  tier.value = _mmTierWasManual ? (cost || 'auto') : 'auto';
  let note = '';
  if (tierSignal && tierSignal !== 'manual') note = t('当前判定') + ': ' + esc(t(_tierSignals[tierSignal] || tierSignal));
  else if (tierSignal === 'manual') note = t('当前判定') + ': ' + t('手动设置');
  else if (cost) note = t('当前判定') + ': ' + (cost === 'free' ? t('免费') : t('付费')) + ' · ' + t('自动');
  _('mmTierNote').textContent = note;
  _('modelMetaEditor').style.display = 'flex';
  try { _('modelMetaEditor').scrollIntoView({ behavior: 'smooth', block: 'nearest' }); } catch (e) {}
}

function hideModelMetaEditor() { _('modelMetaEditor').style.display = 'none'; }

async function saveModelMeta(btn) {
  const id = _('mmId').value;
  const c = parseInt(_('mmCtx').value, 10) || 0;
  const o = parseInt(_('mmOut').value, 10) || 0;
  const tier = _('mmTier').value;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/models/context', { id: id, context: c, output: o });
    if (tier !== 'auto') { await api('POST', '/models/cost', { id: id, cost: tier }); }
    else if (_mmTierWasManual) { await api('POST', '/models/cost', { id: id, cost: '' }); }
    toast(t('已保存') + ': ' + id + ' ctx=' + formatTokenCount(c), 'success');
    hideModelMetaEditor();
    await loadModels();
  } catch (e) { toast(t('保存失败') + ': ' + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

let _confirmResolve = null;
function showConfirm(msg) {
  return new Promise(resolve => {
    _confirmResolve = resolve;
    _('confirmMsg').textContent = msg;
    _('confirmModal').style.display = 'flex';
  });
}
function resolveConfirm(val) {
  _('confirmModal').style.display = 'none';
  if (_confirmResolve) { _confirmResolve(val); _confirmResolve = null; }
}

// Esc 逐层关闭弹层：确认 → 模型元信息 → 拉取模型选择器 → 组合测试 → 模型同步（组合选择器的搜索框自带 Esc 处理）
document.addEventListener('keydown', e => {
  if (!e || e.key !== 'Escape') return;
  if (_('confirmModal') && _('confirmModal').style.display === 'flex') { resolveConfirm(false); return; }
  if (_('diffModal') && _('diffModal').style.display === 'flex') { resolveDiff(false); return; }
  if (_('modelProviderActionModal') && _('modelProviderActionModal').style.display === 'flex') { closeModelProviderAction(); return; }
  if (_('modelMetaEditor') && _('modelMetaEditor').style.display === 'flex') { hideModelMetaEditor(); return; }
  if (_('provPickerModal') && _('provPickerModal').style.display === 'flex') { closeProvModelPicker(); return; }
  if (_('presetPickerModal') && _('presetPickerModal').style.display === 'flex') { closePresetPicker(); return; }
  if (_('importPreviewModal') && _('importPreviewModal').style.display === 'flex') { closeImportPreview(); return; }
  if (_('connLoginModal') && _('connLoginModal').style.display === 'flex') { closeDeviceLogin(); return; }
  if (_('connImportModal') && _('connImportModal').style.display === 'flex') { hideConnectionImport(); return; }
  if (_('comboTestModal') && _('comboTestModal').style.display === 'flex') { closeComboTest(); return; }
  if (_('modelSyncOverlay') && _('modelSyncOverlay').style.display === 'flex') { closeModelSyncModal(); }
});

function editModelMeta(id, ctx, out, cost, tierSignal) {
  showModelMetaEditor(id, ctx, out, cost, tierSignal);
}

// 模型分组渲染已迁移至 renderModelList（按提供商分组 + 筛选 + 批量启停）

async function deleteModel(id) {
  if (!(await showConfirm(t('确认删除模型 ') + id + ' ?'))) return;
  try {
    const [catalog, offeringResponse] = await Promise.all([
      readModelCatalog(),
      api('GET', '/model-offerings'),
    ]);
    const records = (catalog.models || []).filter(model => model.id === id);
    const offerings = ((offeringResponse.data && offeringResponse.data.offerings) || []).filter(offering => offering.modelId === id);
    if (records.length > 1 || offerings.length > 1) {
      toast(t('同一模型由多个 Provider 提供，请在 Provider 分组中选择具体实例后移除'), 'error');
      return;
    }

    // A single administrator-added compatibility row has no provider-scoped
    // owner to infer. Keep the legacy endpoint only for this unambiguous case.
    const local = records.length === 1 && records[0].custom && !records[0].source;
    if (local || offerings.length === 0) {
      await api('POST', '/models/delete', { id });
    } else {
      const offering = offerings[0];
      const providerId = String(offering.providerId || '');
      const source = providerId === 'cline' ? 'remote' : providerId === 'opencode' ? 'zen' : 'provider';
      const response = await api('POST', '/models/remove', { models: [{ id: id, source: source, providerId: providerId }] });
      const result = response.data || {};
      if (!Array.isArray(result.removed) || !Array.isArray(result.skipped) || !Array.isArray(result.errors)) throw new Error(t('移除结果无效，请刷新后重试'));
      if (result.errors.length) throw new Error(result.errors.map(item => item.error).filter(Boolean).join('; '));
      if (typeof reconcileRemovedModelDrafts === 'function') reconcileRemovedModelDrafts(result.removed.concat(result.skipped), result.removed);
    }
    toast(t('模型已删除: ') + id, 'success');
    await Promise.allSettled([loadModels(), loadProviders(), loadGlobal(false), loadCombos()]);
    await loadConfig({ preserveEdits: true });
  } catch (e) { toast(t('删除失败: ') + e.message, 'error'); }
}

// ========== 配置加载 ==========
// 同一时刻的多次 GET /config 合并为一次请求（路由页的表单与检查器同时需要它）
let _adminConfigInflight = null;
let _lastAdminConfig = null; // 最近一次从服务端读到的配置：保存失败时用它还原下拉框
function fetchAdminConfig() {
  if (!_adminConfigInflight) _adminConfigInflight = api('GET', '/config').finally(() => { _adminConfigInflight = null; });
  return _adminConfigInflight;
}

async function loadConfig(options = {}) {
  if (typeof adminHasPermission === 'function' && !adminHasPermission('provider.write')) {
    if (typeof applyRoutingAccess === 'function') applyRoutingAccess();
    return;
  }
  try {
    const d = await fetchAdminConfig();
    const c = d.data;
    _lastAdminConfig = c;
    if (c.address) _('settingAddr').value = c.address;
    if (c.strategy) _('settingStrategy').value = c.strategy;
    const chainField = _('settingModelChain');
    if (!options.preserveEdits || !chainField.dataset.dirty) chainField.value = (c.modelChain || []).join(', ');
    if (c.onlyFree !== undefined && _('settingOnlyFree')) _('settingOnlyFree').value = String(c.onlyFree);
    if (c.version) {
      if (_('settingVersion')) _('settingVersion').value = c.version;
      if (_('aboutVersion')) _('aboutVersion').textContent = t('版本 ') + c.version;
    }
    if (_('aboutApiAddr') && c.address) _('aboutApiAddr').textContent = c.address;
    if (_('aboutBuildRow') && c.build && (c.build.commit || c.build.date || c.build.goVersion)) {
      _('aboutBuildRow').hidden = false;
      _('aboutGitInfo').textContent = [c.build.commit ? 'git ' + c.build.commit : '', c.build.date || '', c.build.goVersion || ''].filter(Boolean).join(' · ');
    }
    if (c.poolPath) _('settingPoolPath').value = c.poolPath;
    if (c.defaultModel !== undefined) {
      const sel = _('settingDefModel');
      // 免费一组、付费一组（optgroup 分类 + 选项标注计费）；
      // 当前默认值不在可用列表时补在开头，避免选中值丢失
      const catalog = mergeModelsById(_cachedModels || []);
      const has = id => catalog.some(m => m.id === id);
      let opts = '';
      if (c.defaultModel && !has(c.defaultModel)) {
        opts += '<option value="' + esc(c.defaultModel) + '" selected>' + esc(c.defaultModel) + esc(t('（当前）')) + '</option>';
      }
      [{ key: 'free', label: t('免费模型'), tag: t('（免费）') },
       { key: 'pass', label: t('付费模型'), tag: t('（付费）') }].forEach(g => {
        // 已停用的模型不进默认模型下拉
        // 同一模型 ID 只列一次；任一来源为免费即归入免费组
        const items = catalog.filter(m => !m._disabled && (m._costs.indexOf('free') !== -1) === (g.key === 'free'));
        if (!items.length) return;
        opts += '<optgroup label="' + esc(g.label) + ' (' + items.length + ')">' +
          items.map(m => '<option value="' + esc(m.id) + '"' + (m.id === c.defaultModel ? ' selected' : '') + '>' + esc(m.id) + esc(g.tag) + '</option>').join('') +
          '</optgroup>';
      });
      sel.innerHTML = opts || '<option value="">' + t('（无可用模型）') + '</option>';
      sel.dataset.configLoaded = '1';
    }
    // 本机 IP 展示（监听 0.0.0.0 时局域网访问地址）
    const ips = c.localIPs || [];
    if (ips.length) {
      _('localIPsRow').style.display = '';
      _('localIPsList').innerHTML = ips.map(ip =>
        '<span class="access-role">' + esc(ip) + '</span>'
      ).join('');
    } else {
      _('localIPsRow').style.display = 'none';
    }
    // 监听地址下拉：补入本机 IP 选项并选中当前值
    const listenSel = _('settingListenHost');
    if (listenSel) {
      if (!listenSel.dataset.inited) {
        listenSel.dataset.inited = '1';
        let opts = '<option value="127.0.0.1">' + t('127.0.0.1（仅本机）') + '</option>';
        if (c.host && ['', '127.0.0.1', 'localhost', '::1'].indexOf(c.host) === -1) {
          opts += '<option value="' + esc(c.host) + '" disabled>' + esc(c.host) + t('（需可信代理配置）') + '</option>';
        }
        listenSel.innerHTML = opts;
      }
      if (c.host) listenSel.value = c.host;
    }
    // 管理员认证始终启用；不提供恢复匿名访问的开关。
    if (_('passwordStatus')) _('passwordStatus').textContent = t('始终启用');
    // 非回环监听安全警告（0.0.0.0 / 局域网 IP 都会暴露管理后台）
    const h = c.host || '';
    const safeHosts = ['', '127.0.0.1', 'localhost', '::1'];
    _('listenWarn').style.display = (safeHosts.indexOf(h) === -1) ? '' : 'none';
    if (c.headers) {
      const tbody = _('headersTableBody');
      tbody.innerHTML = Object.entries(c.headers).map(([k, v]) =>
        '<tr>' +
          '<td><input type="text" class="header-key w-full mono-plain" value="' + esc(k) + '"></td>' +
          '<td><input type="text" class="header-val w-full mono-plain" value="' + esc(v) + '"></td>' +
          '<td><button class="btn btn-sm btn-danger" onclick="this.closest(\'tr\').remove()" title="' + t('移除') + '" aria-label="' + t('移除') + '">'+uiIcon('close')+'</button></td>' +
        '</tr>'
      ).join('');
    }
    if (typeof fillTuning === 'function') fillTuning(c, options);
    renderRouteSummary(c);
    return c;
  } catch (e) { toast(t('加载失败') + ': ' + e.message, 'error'); }
}

function renderRouteSummary(c) {
  const model = _('routeFlowModel'), strategy = _('routeFlowStrategy'), fallback = _('routeFlowFallback');
  if (model) model.textContent = c.defaultModel || t('请求中指定的模型');
  if (strategy) strategy.textContent = c.strategy || 'round_robin';
  if (fallback) {
    const chain = Array.isArray(c.modelChain) ? c.modelChain.filter(Boolean) : [];
    fallback.textContent = chain.length ? chain.join(' → ') : t('内置免费回退链');
    fallback.title = fallback.textContent;
  }
}

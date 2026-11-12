// One setup entry point over the existing credential owners. No new API or store.
let _providerSetupLoad = null;
let _providerSetupError = false;
let _providerSetupMounted = null;
let _providerSetupFocus = null;
let _providerSetupReturnFocus = true;
let _providerSetupAuthContext = null;
let _providerSetupGeneration = 0;

async function loadProviderSetupDirectory() {
  if (_providerSetupLoad) return _providerSetupLoad;
  _providerSetupError = false;
  _providerSetupLoad = (async () => {
    try {
      await Promise.all([
        loadConnections(),
        _presetList.length ? Promise.resolve() : api('GET', '/providers/presets').then(d => {
          _presetList = (d.data && d.data.presets) || [];
        }),
      ]);
      if (_connLoadFailed) _providerSetupError = true;
    } catch (e) { _providerSetupError = true; }
    finally { _providerSetupLoad = null; renderProviderSetupChoices(); }
  })();
  return _providerSetupLoad;
}

// Connection categories, in the order an operator usually wants them. The
// category is derived from how the service actually authenticates, not from its
// billing kind: a preset that runs an OAuth login flow is an OAuth service even
// when it also has a free tier.
const SETUP_CATEGORIES = [
  { id: 'oauth', label: 'OAuth 登录', note: '浏览器登录账号，无需手填密钥' },
  { id: 'apikey', label: 'API Key 连接', note: '填写服务地址与 API Key' },
  { id: 'free', label: '免费 / 免密钥', note: '公共端点，无需 API Key' },
  { id: 'local', label: '本地与自建', note: '本机或自建 OpenAI 兼容服务' },
];
let _providerSetupCategory = 'all';
const _providerSetupExpanded = {};
// Long groups are capped so 100+ services stay scannable; the cap is per
// category and resets whenever the operator changes the filter.
const SETUP_GROUP_CAP = 8;

function toggleProviderSetupGroup(id) {
  _providerSetupExpanded[id] = !_providerSetupExpanded[id];
  renderProviderSetupChoices();
}

function providerSetupCategoryOf(choice) {
  if (choice.kind === 'cline') return 'oauth';
  if (choice.kind === 'opencode') return 'apikey';
  if (choice.kind === 'api') return 'apikey';
  if (choice.kind === 'oauth') return 'oauth';
  const preset = choice.preset;
  if (preset) {
    if (preset.loginFlow) return _connFlows.includes(preset.authFlow) ? 'oauth' : 'apikey';
    if (preset.kind === 'local') return 'local';
    if (preset.kind === 'free') return 'free';
  }
  return 'apikey';
}

function providerSetupHost(baseURL) {
  try { return new URL(baseURL).host; } catch (e) { return baseURL || ''; }
}

function renderProviderSetupFilters(choices) {
  const box = _('providerSetupFilters');
  if (!box) return;
  const counts = {};
  choices.forEach(c => { const id = providerSetupCategoryOf(c); counts[id] = (counts[id] || 0) + 1; });
  const chips = [{ id: 'all', label: '全部', count: choices.length }]
    .concat(SETUP_CATEGORIES.filter(c => counts[c.id]).map(c => ({ id: c.id, label: c.label, count: counts[c.id] })));
  box.innerHTML = chips.map(c =>
    '<button type="button" class="connect-filter-chip' + (c.id === _providerSetupCategory ? ' is-active' : '') + '"' +
    ' aria-pressed="' + (c.id === _providerSetupCategory ? 'true' : 'false') + '"' +
    ' data-setup-filter="' + escAttr(c.id) + '" onclick="setProviderSetupCategory(' + globalJS(c.id) + ')">' +
    '<span>' + esc(t(c.label)) + '</span><span class="connect-filter-count">' + c.count + '</span></button>').join('');
}

function setProviderSetupCategory(id) {
  _providerSetupCategory = id;
  Object.keys(_providerSetupExpanded).forEach(k => { delete _providerSetupExpanded[k]; });
  renderProviderSetupChoices();
}

function renderProviderSetupChoices() {
  const box = _('providerSetupChoices');
  if (!box) return;
  if (_providerSetupLoad) { box.innerHTML = globalSkeleton(); return; }
  if (_providerSetupError) {
    if (_('providerSetupFilters')) _('providerSetupFilters').innerHTML = '';
    box.innerHTML = '<div class="setup-error">' + esc(t('无法加载服务目录')) +
      '<button type="button" class="btn btn-sm" onclick="loadProviderSetupDirectory()">' + esc(t('重试')) + '</button></div>';
    return;
  }
  const choices = [
    { kind: 'cline', name: 'Cline', logo: '/admin/assets/providers/cline.png', note: '浏览器登录、刷新令牌或批量导入' },
    { kind: 'opencode', name: 'OpenCode Zen', logo: '/admin/assets/providers/opencode.png', note: '公共模型或 API Key 连接' },
    { kind: 'api', name: '自定义 API 或本地服务', note: '填写服务地址和 API Key' },
    ..._presetList.map(p => ({ kind: 'preset', key: p.key, name: p.name, logo: p.logo || '', preset: p,
      note: p.loginFlow ? (_connFlows.includes(p.authFlow) ? '浏览器登录或导入 Token' : '导入 Token')
        : (p.kind === 'local' ? '本地服务' : p.kind === 'free' ? '无需 API Key' : 'API Key 连接') })),
  ];
  // A supported flow without a preset remains an account-import workflow; do
  // not invent the endpoint/model configuration needed to route that service.
  const presetFlows = new Set(_presetList.filter(p => p.loginFlow).map(p => p.authFlow));
  _connFlows.filter(flow => !presetFlows.has(flow)).forEach(flow => {
    choices.push({ kind: 'oauth', key: flow, name: flowLabel(flow), logo: _flowLogos[flow] || '', note: '登录账号后配置 Provider' });
  });
  renderProviderSetupFilters(choices);
  const query = _('providerSetupSearch').value.trim().toLowerCase();
  const matched = choices.filter(c => (c.name + ' ' + t(c.note)).toLowerCase().includes(query));
  const shown = _providerSetupCategory === 'all' ? matched : matched.filter(c => providerSetupCategoryOf(c) === _providerSetupCategory);
  if (!shown.length) {
    box.innerHTML = globalEmpty(_providerSetupCategory === 'all' ? '没有匹配的服务' : '该连接方式下没有匹配的服务');
    return;
  }
  const card = c => {
    // Inside a category group the type label is redundant, so show where the
    // service actually lives; keep the type label when the list is flat.
    const note = c.preset && c.preset.baseURL && _providerSetupCategory === 'all'
      ? providerSetupHost(c.preset.baseURL) : t(c.note);
    const monogram = '<span class="connect-provider-monogram">' + esc(Array.from(c.name)[0]) + '</span>';
    const icon = c.logo
      ? '<span class="connect-provider-icon"><img src="' + escAttr(c.logo) + '" alt="" aria-hidden="true" onerror="this.parentElement.classList.add(\'logo-error\')">' + monogram + '</span>'
      : '<span class="connect-provider-icon" aria-hidden="true">' + monogram + '</span>';
    return '<button type="button" class="connect-provider-choice" data-setup-kind="' + escAttr(c.kind) + '" data-setup-key="' + escAttr(c.key || '') + '"' +
      ' data-setup-category="' + escAttr(providerSetupCategoryOf(c)) + '"' +
      ' onclick="chooseProviderSetup(' + globalJS(c.kind) + ',' + globalJS(c.key || '') + ')">' +
      icon + '<span><strong>' + esc(t(c.name)) +
      '</strong><small>' + esc(note) + '</small></span><span aria-hidden="true">→</span></button>';
  };
  // A single flat list when the operator narrowed to one category; otherwise
  // group under a heading so 100+ services stay scannable.
  if (_providerSetupCategory !== 'all') { box.innerHTML = '<div class="connect-provider-grid">' + shown.map(card).join('') + '</div>'; return; }
  box.innerHTML = SETUP_CATEGORIES.map(cat => {
    const group = matched.filter(c => providerSetupCategoryOf(c) === cat.id);
    if (!group.length) return '';
    const expanded = !!_providerSetupExpanded[cat.id];
    const visible = expanded ? group : group.slice(0, SETUP_GROUP_CAP);
    const more = group.length > visible.length
      ? '<button type="button" class="btn btn-sm connect-group-more" onclick="toggleProviderSetupGroup(' + globalJS(cat.id) + ')">' +
        esc(t('显示全部')) + ' ' + group.length + '</button>'
      : (expanded && group.length > SETUP_GROUP_CAP
        ? '<button type="button" class="btn btn-sm connect-group-more" onclick="toggleProviderSetupGroup(' + globalJS(cat.id) + ')">' + esc(t('收起')) + '</button>' : '');
    return '<section class="connect-provider-group" aria-labelledby="setupGroup-' + cat.id + '">' +
      '<h3 class="connect-provider-group-title" id="setupGroup-' + cat.id + '">' + esc(t(cat.label)) +
      '<span class="connect-provider-group-note">' + esc(t(cat.note)) + '</span></h3>' +
      '<div class="connect-provider-grid">' + visible.map(card).join('') + '</div>' + more + '</section>';
  }).join('') || globalEmpty('没有匹配的服务');
}

async function openProviderSetup(kind, key, preserveRoute) {
  _providerSetupFocus = document.activeElement;
  _providerSetupReturnFocus = true;
  if (!preserveRoute) applyTab('providers');
  showProviderSetupDirectory();
  const generation = ++_providerSetupGeneration;
  setDrawerGuard('providerSetup', () => {
    const closedGeneration = ++_providerSetupGeneration;
    const mounted = !!_providerSetupMounted;
    restoreProviderSetupForm();
    if (mounted) loadGlobal(false);
    if (_providerSetupReturnFocus) setTimeout(() => {
      if (closedGeneration !== _providerSetupGeneration) return;
      const target = [_providerSetupFocus, _('connectProviderBtn'), document.querySelector('.nav-item.active'), _('mobileNavBtn')]
        .find(el => el && el.isConnected && el.getClientRects().length);
      if (target) target.focus();
    }, 0);
    return true;
  });
  openDrawer('providerSetup');
  _('providerSetupSearch').focus();
  const pending = loadProviderSetupDirectory();
  renderProviderSetupChoices();
  await pending;
  if (generation === _providerSetupGeneration && kind && _('providerSetup').classList.contains('open') && !_providerSetupError) await chooseProviderSetup(kind, key);
}

function restoreProviderSetupForm() {
  const mounted = _providerSetupMounted;
  if (!mounted) return;
  mounted.marker.replaceWith(mounted.node);
  mounted.node.style.display = mounted.display;
  if (mounted.ariaHidden == null) mounted.node.removeAttribute('aria-hidden');
  else mounted.node.setAttribute('aria-hidden', mounted.ariaHidden);
  _providerSetupMounted = null;
}

function showProviderSetupDirectory() {
  restoreProviderSetupForm();
  _('providerSetupWorkspace').hidden = true;
  _('providerSetupDirectory').hidden = false;
  _('providerSetupSearch').value = '';
  _providerSetupCategory = 'all';
  Object.keys(_providerSetupExpanded).forEach(k => { delete _providerSetupExpanded[k]; });
  renderProviderSetupChoices();
  if (_('providerSetup').classList.contains('open')) _('providerSetupSearch').focus();
}

function mountProviderSetupForm(node, title) {
  if (!node) return;
  restoreProviderSetupForm();
  const marker = document.createComment('provider setup form location');
  node.before(marker);
  _providerSetupMounted = { node, marker, display: node.style.display, ariaHidden: node.getAttribute('aria-hidden') };
  _('providerSetupContent').append(node);
  node.style.display = 'block';
  node.setAttribute('aria-hidden', 'false');
  _('providerSetupSelectedTitle').textContent = title;
  _('providerSetupWorkspace').hidden = false;
  _('providerSetupDirectory').hidden = true;
  const first = node.querySelector('button,input,select,textarea');
  if (first) first.focus();
  applyLang();
}

async function closeProviderSetup(returnFocus) {
  _providerSetupReturnFocus = returnFocus !== false;
  await closeDrawer('providerSetup');
}

async function chooseProviderSetup(kind, key) {
  if (kind === 'cline') {
    mountProviderSetupForm(_('loginImportSection'), 'Cline');
    toggleLoginImport(true);
    document.querySelector('#importTabs [data-tab="oauth"]').click();
    return;
  }
  if (kind === 'opencode') {
    const node = _('providerNativeOpenCode');
    mountProviderSetupForm(node, 'OpenCode Zen');
    await Promise.all([loadOcConfig(), loadZenHeaders()]);
    if (_providerSetupMounted && _providerSetupMounted.node === node && _('providerSetup').classList.contains('open')) _('ocEnabled').focus();
    loadStats();
    return;
  }
  if (kind === 'cline-config') {
    mountProviderSetupForm(_('providerNativeCline'), 'Cline');
    await loadConfig();
    return;
  }
  const preset = _presetList.find(p => kind === 'preset' ? p.key === key : kind === 'oauth' && p.loginFlow && p.authFlow === key);
  if (kind === 'preset' && !preset) return;
  if (kind === 'oauth' && !_connFlows.includes(key)) return;
  await closeProviderSetup(false);
  if (preset) applyPreset(preset.key);
  else {
    showProviderEditor();
    if (kind === 'oauth') prepareProviderLogin({ authFlow: key, name: flowLabel(key) });
  }
  _('provName').focus();
}

function setupProviderInstance(id) {
  const instance = ((_globalData && _globalData.instances) || []).find(p => String(p.id) === String(id));
  if (!instance) return;
  if (instance.runtimeBuiltin && instance.id === 'cline') openProviderSetup('cline-config');
  else if (instance.runtimeBuiltin && instance.id === 'opencode') openProviderSetup('opencode');
  else if (instance.deletable) editProvider(instance.id);
}

function prepareProviderLogin(preset) {
  _providerSetupAuthContext = { flow: preset.authFlow || '', sessionId: null };
  const box = _('providerAuthSetup');
  box.hidden = false;
  box.innerHTML = '<strong>' + esc(t('连接登录账号')) + ' · ' + esc(preset.name || flowLabel(preset.authFlow)) + '</strong>' +
    '<p id="providerAuthSetupStatus">' + esc(t('先登录或导入 Token，再确认模型并保存 Provider。')) + '</p>' +
    '<div class="action-row">' + (_connFlows.includes(preset.authFlow)
      ? '<button type="button" id="providerAuthLoginBtn" class="btn btn-sm btn-primary" onclick="startProviderLogin(' + globalJS(preset.authFlow) + ',this)">' + esc(t('登录账号')) + '</button>' : '') +
    '<button type="button" class="btn btn-sm" onclick="importProviderSetupToken(' + globalJS(preset.authFlow || '') + ')">' + esc(t('导入 Token')) + '</button></div>';
}

function providerSetupLoginStarted(flow, sessionId, expectedContext) {
  const context = _providerSetupAuthContext;
  if (context && (expectedContext === undefined || context === expectedContext) && context.flow === flow && _('providerEditor').classList.contains('open')) context.sessionId = sessionId;
}

function providerSetupLoginComplete(connection, sessionId, expectedContext) {
  const context = _providerSetupAuthContext;
  if (!context || (expectedContext !== undefined && context !== expectedContext) || !connection || !connection.id || context.flow !== connection.provider ||
      (sessionId && context.sessionId !== sessionId) || !_('providerEditor').classList.contains('open')) return;
  toggleConnPick(connection.id, true);
  renderConnPick();
  const status = _('providerAuthSetupStatus');
  if (status) status.textContent = t('账号已连接并选中。确认模型后保存 Provider 即可使用。');
}

async function importProviderSetupToken(flow) {
  await showConnectionImport();
  const select = _('connImpProvider');
  if (flow && Array.from(select.options).some(option => option.value === flow)) {
    select.value = flow;
    connImportProviderChange();
  }
}

document.addEventListener('keydown', event => {
  if (event.key !== 'Tab' || _openDrawers[_openDrawers.length - 1] !== 'providerSetup' || _modalLayerOpen()) return;
  const items = Array.from(_('providerSetup').querySelectorAll('button,input,select,textarea,a[href]'))
    .filter(el => !el.disabled && el.getClientRects().length);
  if (!items.length) return;
  const first = items[0], last = items[items.length - 1];
  if (event.shiftKey && (document.activeElement === first || !_('providerSetup').contains(document.activeElement))) {
    event.preventDefault(); last.focus();
  } else if (!event.shiftKey && (document.activeElement === last || !_('providerSetup').contains(document.activeElement))) {
    event.preventDefault(); first.focus();
  }
});

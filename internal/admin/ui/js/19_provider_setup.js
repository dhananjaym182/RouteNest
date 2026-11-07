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

function renderProviderSetupChoices() {
  const box = _('providerSetupChoices');
  if (!box) return;
  if (_providerSetupLoad) { box.innerHTML = globalSkeleton(); return; }
  if (_providerSetupError) {
    box.innerHTML = '<div class="setup-error">' + esc(t('无法加载服务目录')) +
      '<button type="button" class="btn btn-sm" onclick="loadProviderSetupDirectory()">' + esc(t('重试')) + '</button></div>';
    return;
  }
  const choices = [
    { kind: 'cline', name: 'Cline', note: '浏览器登录、刷新令牌或批量导入' },
    { kind: 'opencode', name: 'OpenCode Zen', note: '公共模型或 API Key 连接' },
    { kind: 'api', name: '自定义 API 或本地服务', note: '填写服务地址和 API Key' },
    ..._presetList.map(p => ({ kind: 'preset', key: p.key, name: p.name,
      note: p.loginFlow ? (_connFlows.includes(p.authFlow) ? '浏览器登录或导入 Token' : '导入 Token')
        : (p.kind === 'local' ? '本地服务' : p.kind === 'free' ? '无需 API Key' : 'API Key 连接') })),
  ];
  // A supported flow without a preset remains an account-import workflow; do
  // not invent the endpoint/model configuration needed to route that service.
  const presetFlows = new Set(_presetList.filter(p => p.loginFlow).map(p => p.authFlow));
  _connFlows.filter(flow => !presetFlows.has(flow)).forEach(flow => {
    choices.push({ kind: 'oauth', key: flow, name: flowLabel(flow), note: '登录账号后配置 Provider' });
  });
  const query = _('providerSetupSearch').value.trim().toLowerCase();
  const shown = choices.filter(c => (c.name + ' ' + t(c.note)).toLowerCase().includes(query));
  box.innerHTML = shown.length ? shown.map(c =>
    '<button type="button" class="connect-provider-choice" data-setup-kind="' + escAttr(c.kind) + '" data-setup-key="' + escAttr(c.key || '') +
    '" onclick="chooseProviderSetup(' + globalJS(c.kind) + ',' + globalJS(c.key || '') + ')">' +
    '<span class="connect-provider-icon" aria-hidden="true">' + esc(Array.from(c.name)[0]) + '</span><span><strong>' + esc(t(c.name)) +
    '</strong><small>' + esc(t(c.note)) + '</small></span><span aria-hidden="true">→</span></button>').join('') : globalEmpty('没有匹配的服务');
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

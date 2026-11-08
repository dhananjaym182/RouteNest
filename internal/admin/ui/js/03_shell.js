// ========== API 请求 ==========
function adminCookie(name) {
  const prefix = name + '=';
  for (const part of document.cookie.split(';')) {
    const value = part.trim();
    if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
  }
  return '';
}

async function adminFetch(method, path, body) {
  const opts = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined && body !== null) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    const csrf = adminCookie('routenest_admin_csrf');
    if (csrf) opts.headers['X-RouteNest-CSRF'] = csrf;
  }
  return fetch(API + path, opts);
}

async function api(method, path, body) {
  let res;
  try {
    res = await adminFetch(method, path, body);
    setGatewayReachability(true);
  } catch (e) {
    setGatewayReachability(false);
    throw e;
  }
  const data = await res.json().catch(() => ({ error: 'invalid_response' }));
  if (res.status === 401) {
    showLogin();
    throw new Error(t('需要登录'));
  }
  if (!data.success && data.error) throw new Error(data.error);
  return data;
}

let _gatewayReachable = null;
function gatewayV1URL() {
  const { protocol, host } = window.location;
  return protocol + '//' + host + '/v1';
}
function setGatewayReachability(ok) {
  _gatewayReachable = ok;
  const el = _('gatewayStatus'), label = _('gatewayStatusLabel');
  if (!el || !label) return;
  el.classList.toggle('is-online', ok === true);
  el.classList.toggle('is-offline', ok === false);
  el.classList.toggle('is-checking', ok == null);
  label.textContent = gatewayV1URL();
}
function copyGatewayUrl(btn) {
  const url = gatewayV1URL();
  const done = () => {
    if (!btn) return;
    const prev = btn.innerHTML;
    btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="var(--green)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12"/></svg>';
    btn.classList.add('is-copied');
    setTimeout(() => { btn.innerHTML = prev; btn.classList.remove('is-copied'); }, 1200);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(url).then(done).catch(() => { fallbackCopy(url); done(); });
  } else {
    fallbackCopy(url); done();
  }
  try { toast(t('已复制到剪贴板'), 'success'); } catch (e) {}
}
function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  try { document.execCommand('copy'); } catch (e) {}
  document.body.removeChild(ta);
}

// ========== 后台登录 ==========
let _adminAuthMode = 'login';
let _adminAuthStatus = null;
let _adminPrincipal = null;
const ADMIN_PASSWORD_MIN_LENGTH = 8;

function adminPasswordTooShort(password) {
  return Array.from(password).length < ADMIN_PASSWORD_MIN_LENGTH;
}

function setAdminAuthMode(mode) {
  _adminAuthMode = mode || 'login';
  const bootstrap = _adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery';
  const legacy = _adminAuthMode === 'legacy';
  const usernameRow = _('loginUsernameRow');
  const secretRow = _('bootstrapSecretRow');
  const passwordRow = _('loginPasswordRow');
  const newPasswordRow = _('loginNewPasswordRow');
  if (usernameRow) usernameRow.style.display = bootstrap ? 'none' : '';
  if (secretRow) secretRow.style.display = bootstrap ? '' : 'none';
  if (passwordRow) passwordRow.style.display = bootstrap ? 'none' : '';
  if (newPasswordRow) newPasswordRow.style.display = (bootstrap || legacy) ? '' : 'none';
  if (_('loginUsername')) _('loginUsername').disabled = bootstrap;
  if (_('bootstrapSecret')) _('bootstrapSecret').disabled = !bootstrap;
  if (_('loginPassword')) _('loginPassword').disabled = bootstrap;
  if (_('loginNewPassword')) _('loginNewPassword').disabled = !bootstrap && !legacy;
  if (_('loginConfirmPassword')) _('loginConfirmPassword').disabled = !bootstrap && !legacy;
  if (_('loginPasswordLabel')) _('loginPasswordLabel').textContent = t(legacy ? '当前密码' : '密码');
  if (_('loginLocalHelp')) _('loginLocalHelp').style.display = bootstrap ? 'none' : '';
  const title = _('loginTitle'), help = _('loginHelp'), button = _('loginSubmit');
  if (_adminAuthMode === 'bootstrap') {
    if (title) title.textContent = t('初始化管理员');
    if (help) help.textContent = t('输入本机终端或桌面弹窗中显示的一次性密钥，并设置永久密码。');
    if (button) button.textContent = t('完成初始化');
  } else if (_adminAuthMode === 'recovery') {
    if (title) title.textContent = t('完成离线恢复');
    if (help) help.textContent = t('输入本机离线恢复密钥，并设置新的永久密码。');
    if (button) button.textContent = t('完成恢复');
  } else if (_adminAuthMode === 'legacy') {
    if (title) title.textContent = t('升级管理员密码');
    if (help) help.textContent = t('旧密码验证成功后必须升级为至少 8 位的新密码。');
    if (button) button.textContent = t('升级并登录');
  } else {
    if (title) title.textContent = 'RouteNest ' + t('管理后台');
    if (help) help.textContent = t('请输入管理员用户名和密码。');
    if (button) button.textContent = t('登录');
  }
  renderSSOLoginProviders();
}

function showLogin(mode) {
  if (mode) setAdminAuthMode(mode);
  const ov = _('loginOverlay');
  if (ov && ov.style.display !== 'flex') {
    ov.style.display = 'flex';
    setTimeout(() => {
      const target = (_adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery') ? _('bootstrapSecret') : _('loginPassword');
      if (target) target.focus();
    }, 50);
  }
}

function renderSSOLoginProviders() {
  const host = _('loginSSOProviders'), buttons = _('loginSSOButtons');
  if (!host || !buttons) return;
  buttons.textContent = '';
  const providers = (_adminAuthStatus && _adminAuthStatus.ssoProviders) || [];
  const available = Array.isArray(providers) && _adminAuthMode === 'login' ? providers.filter(provider =>
    provider.enabled === true && provider.ready === true && (provider.type === 'oidc' || provider.type === 'saml')
  ) : [];
  host.style.display = available.length ? '' : 'none';
  available.forEach(provider => {
    const button = document.createElement('button');
    button.type = 'button'; button.className = 'btn btn-block';
    button.textContent = t('使用单点登录') + ' · ' + provider.id + ' (' + provider.type.toUpperCase() + ')';
    button.addEventListener('click', () => startSSOLogin(provider));
    buttons.appendChild(button);
  });
}

async function startSSOLogin(provider) {
  if (!provider || !provider.enabled || !provider.ready || _adminAuthMode !== 'login') return;
  if (provider.type !== 'oidc' && provider.type !== 'saml') return;
  const buttons = _('loginSSOButtons').querySelectorAll('button');
  buttons.forEach(button => { button.disabled = true; });
  _('loginError').textContent = '';
  try {
    const response = await fetch(API + '/auth/' + provider.type + '/start', {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider: provider.id, returnLocation: location.pathname + location.search + location.hash })
    });
    const envelope = await response.json().catch(() => ({}));
    const result = envelope.data || envelope;
    if (!response.ok || envelope.success === false || !result.redirectURL) throw new Error(envelope.error || t('单点登录暂不可用'));
    const redirect = new URL(result.redirectURL, location.href);
    if ((redirect.protocol !== 'https:' && redirect.protocol !== 'http:') || redirect.username || redirect.password) throw new Error(t('单点登录地址无效'));
    location.assign(redirect.href);
  } catch (e) { _('loginError').textContent = e.message || t('单点登录失败，请重试'); }
  finally { buttons.forEach(button => { button.disabled = false; }); }
}

async function submitLogin() {
  const button = _('loginSubmit');
  if (button && button.disabled) return;
  const username = (_('loginUsername') && _('loginUsername').value.trim()) || 'admin';
  const pwd = (_('loginPassword') && _('loginPassword').value) || '';
  const secret = (_('bootstrapSecret') && _('bootstrapSecret').value) || '';
  const newPassword = (_('loginNewPassword') && _('loginNewPassword').value) || '';
  const confirm = (_('loginConfirmPassword') && _('loginConfirmPassword').value) || '';
  const bootstrap = _adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery';
  const rotating = bootstrap || _adminAuthMode === 'legacy';
  if (!bootstrap && !pwd) { _('loginError').textContent = t('请输入管理员密码'); return; }
  if (bootstrap && !secret) { _('loginError').textContent = t('请输入一次性本机密钥'); return; }
  if (rotating && (adminPasswordTooShort(newPassword) || newPassword !== confirm)) {
    _('loginError').textContent = adminPasswordTooShort(newPassword) ? t('永久密码至少 8 位') : t('两次输入的密码不一致');
    return;
  }
  _('loginError').textContent = '';
  if (button) button.disabled = true;
  try {
    let path = '/login', body = { username: username, password: pwd };
    if (_adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery') {
      path = '/auth/bootstrap'; body = { secret: secret, password: newPassword };
    } else if (_adminAuthMode === 'legacy') {
      path = '/auth/legacy/rotate'; body = { username: username, oldPassword: pwd, newPassword: newPassword };
    }
    const res = await fetch(API + path, {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok && data.success) {
      location.reload();
    } else if (data.error === 'password_rotation_required') {
      setAdminAuthMode('legacy');
      _('loginError').textContent = t('此安装仍使用旧密码格式，请设置新的永久密码。');
    } else {
      _('loginError').textContent = data.error || t('登录失败');
      if (_('loginPassword')) _('loginPassword').value = '';
    }
  } catch (e) { _('loginError').textContent = t('网络错误，请重试'); }
  finally { if (button) button.disabled = false; }
}

function adminRoleLabel(role) {
  const labels = { viewer: '查看者', provider_admin: '提供方管理员', credential_admin: '凭据管理员', security_admin: '安全管理员', break_glass: '应急管理员' };
  return t(labels[role] || role);
}

function renderAdminRoleBadges(host, roles) {
  if (!host) return;
  host.textContent = '';
  if (!Array.isArray(roles) || !roles.length) { host.textContent = '—'; return; }
  roles.forEach(role => {
    const badge = document.createElement('span'); badge.className = 'access-role';
    badge.textContent = adminRoleLabel(role); badge.title = role; host.appendChild(badge);
  });
}

function renderAdminPrincipal(principal) {
  _adminPrincipal = principal || null;
  const identity = _('adminIdentity');
  if (identity) identity.textContent = principal ? principal.username : t('未登录');
  renderAdminRoleBadges(_('adminRoles'), principal && principal.roles);
  const method = _('adminAuthMethod');
  if (method) method.textContent = principal ? (principal.method === 'local' ? t('本地账号') : String(principal.method).toUpperCase() + (principal.provider ? ' · ' + principal.provider : '')) : '—';
  if (_('localAdminPasswordForm')) _('localAdminPasswordForm').hidden = !principal || principal.method !== 'local';
  if (_('ssoAdminPasswordNotice')) _('ssoAdminPasswordNotice').hidden = !principal || principal.method === 'local';
  applyAdminPermissionVisibility();
}

const ADMIN_ROLE_PERMISSIONS = Object.freeze({
  viewer: ['admin.view'],
  provider_admin: ['admin.view', 'provider.write', 'dangerous.execute'],
  credential_admin: ['admin.view', 'credential.write', 'credential.export', 'dangerous.execute'],
  security_admin: ['admin.view', 'security.write'],
  break_glass: ['admin.view', 'provider.write', 'credential.write', 'credential.export', 'security.write', 'dangerous.execute']
});

function adminHasPermission(permission) {
  const roles = (_adminPrincipal && Array.isArray(_adminPrincipal.roles)) ? _adminPrincipal.roles : [];
  return roles.some(role => (ADMIN_ROLE_PERMISSIONS[role] || []).includes(permission));
}

function applyAdminPermissionVisibility() {
  document.querySelectorAll('[data-admin-permission]').forEach(element => {
    const required = String(element.dataset.adminPermission || '').split(/\s+/).filter(Boolean);
    element.hidden = !required.every(adminHasPermission);
  });
  const activeSecuritySubtab = document.querySelector('.subtabs[data-page="security"] .subtab.active');
  if (activeSecuritySubtab && activeSecuritySubtab.hidden && typeof switchPageSubTab === 'function') {
    switchPageSubTab('security', 'access', true);
  }
}

async function initializeAdminAuth() {
  try {
    const statusResponse = await fetch(API + '/auth/status', { credentials: 'same-origin' });
    const statusEnvelope = await statusResponse.json();
    if (!statusResponse.ok || !statusEnvelope.success) throw new Error(statusEnvelope.error || 'security_unavailable');
    _adminAuthStatus = statusEnvelope.data || {};
    renderSSOLoginProviders();
    if (_adminAuthStatus.bootstrapRequired) { showLogin('bootstrap'); return false; }
    if (_adminAuthStatus.recoveryRequired) { showLogin('recovery'); return false; }
    const sessionResponse = await fetch(API + '/auth/session', { credentials: 'same-origin' });
    const sessionEnvelope = await sessionResponse.json().catch(() => ({}));
    if (sessionResponse.status === 401) {
      showLogin(_adminAuthStatus.legacyRotationRequired ? 'legacy' : 'login');
      return false;
    }
    if (!sessionResponse.ok || !sessionEnvelope.success) throw new Error(sessionEnvelope.error || 'session_unavailable');
    renderAdminPrincipal(sessionEnvelope.data.principal);
    return true;
  } catch (e) {
    setGatewayReachability(false);
    showLogin('login');
    _('loginError').textContent = t('安全服务不可用: ') + e.message;
    return false;
  }
}

async function logoutAdmin() {
  try {
    await api('POST', '/logout', {});
  } catch (e) {}
  location.reload();
}

// 用系统默认浏览器打开外部链接（桌面 WebView 内导航不会跳外部浏览器，需走后端）
async function openExternal(url) {
  try {
    await api('POST', '/open-external', { url: url });
    toast(t('已在系统浏览器中打开'), 'success');
  } catch (e) { toast(t('打开失败: ') + e.message, 'error'); }
}

// ========== 主题（浅色 / 深色） ==========
const THEME_KEY = 'cline_admin_theme';
function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  const themeMeta = document.querySelector('meta[name="theme-color"]');
  if (themeMeta) themeMeta.setAttribute('content', theme === 'dark' ? '#0B1020' : '#F7F8FC');
  document.querySelectorAll('[data-theme-opt]').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.themeOpt === theme);
    btn.setAttribute('aria-pressed', btn.dataset.themeOpt === theme ? 'true' : 'false');
  });
}
function setTheme(theme) {
  if (theme !== 'light' && theme !== 'dark') return;
  try { localStorage.setItem(THEME_KEY, theme); } catch (e) {}
  applyTheme(theme);
}
applyTheme(document.documentElement.dataset.theme || 'light');

// ========== 移动端导航 ==========
let _mobileNavReturnFocus = null;
function toggleMobileNav(force) {
  const wasOpen = document.documentElement.dataset.mobileNav === 'open';
  const open = force === undefined ? !wasOpen : !!force;
  if (open && !wasOpen) _mobileNavReturnFocus = document.activeElement;
  if (open) document.documentElement.dataset.mobileNav = 'open';
  else delete document.documentElement.dataset.mobileNav;
  const btn = _('mobileNavBtn');
  if (btn) {
    btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    btn.setAttribute('aria-label', t(open ? '关闭导航' : '打开导航'));
  }
  const main = document.querySelector('.main');
  if (main) {
    if (open) main.setAttribute('inert', '');
    else main.removeAttribute('inert');
  }
  if (open) {
    requestAnimationFrame(() => {
      const target = document.querySelector('.sidebar .nav-item.active') || document.querySelector('.sidebar .nav-item');
      if (target) target.focus();
    });
  } else if (wasOpen && _mobileNavReturnFocus && typeof _mobileNavReturnFocus.focus === 'function') {
    _mobileNavReturnFocus.focus();
    _mobileNavReturnFocus = null;
  }
}
function closeMobileNav() { toggleMobileNav(false); }
window.addEventListener('resize', () => { if (window.innerWidth > 900) closeMobileNav(); });

// ========== 抽屉（右侧滑出编辑器） ==========
const _openDrawers = [];
// A closed drawer is only translated off-screen by CSS, so it stays visible to
// assistive tech and keeps its buttons in the tab order. inert removes both.
function setDrawerInert(dr, inert) {
  if (!dr) return;
  if (inert) {
    dr.setAttribute('inert', '');
    dr.setAttribute('aria-hidden', 'true');
  } else {
    dr.removeAttribute('inert');
    dr.removeAttribute('aria-hidden');
  }
}
function openDrawer(name) {
  const ov = _(name + 'Overlay'), dr = _(name);
  if (!dr) return;
  if (ov) ov.classList.add('open');
  dr.classList.add('open');
  setDrawerInert(dr, false);
  if (!_openDrawers.includes(name)) _openDrawers.push(name);
  refreshSaveBar();
}
const _drawerGuards = {};
function setDrawerGuard(name, fn) { if (fn) _drawerGuards[name] = fn; else delete _drawerGuards[name]; }
async function closeDrawer(name) {
  const g = _drawerGuards[name];
  if (g) {
    let ok = true;
    try { ok = await g(); } catch (e) { ok = true; }
    if (!ok) return;
  }
  const ov = _(name + 'Overlay'), dr = _(name);
  if (ov) ov.classList.remove('open');
  if (dr) dr.classList.remove('open');
  setDrawerInert(dr, true);
  const i = _openDrawers.indexOf(name);
  if (i >= 0) _openDrawers.splice(i, 1);
  refreshSaveBar();
}
// ESC closes the topmost drawer, but only when no modal layer is open on top
// of it (modal Esc handling lives in 13_models and runs on the same event).
const _ESC_MODAL_IDS = ['confirmModal','diffModal','modelMetaEditor','provPickerModal','comboModelPicker','presetPickerModal','importPreviewModal','connLoginModal','connImportModal','comboTestModal','modelSyncOverlay','logDetailModal'];
function _modalLayerOpen() {
  return _ESC_MODAL_IDS.some(id => { const el = _(id); return el && el.style.display === 'flex'; });
}
document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && document.documentElement.dataset.mobileNav === 'open') { closeMobileNav(); return; }
  if (e.key === 'Escape' && _openDrawers.length && !_modalLayerOpen()) closeDrawer(_openDrawers[_openDrawers.length - 1]);
});
// Drawers ship closed in the markup, so seed inert before the first open.
document.querySelectorAll('.drawer').forEach(dr => setDrawerInert(dr, !dr.classList.contains('open')));

// ========== 未保存更改：悬浮保存条 + 变更预览弹窗 ==========
var _verCheckDone = false; // 「关于」页更新检查只自动跑一次（checkForUpdate 在 05_settings 定义）
// 每个面（provider 抽屉 / 路由页调优卡）注册一个 ctx；同一时刻只展示优先级最高且脏的那个。
const _saveBarCtxs = {};
const _SAVE_BAR_ORDER = ['provider', 'tuning'];
function setSaveBarCtx(surface, ctx) {
  if (ctx) _saveBarCtxs[surface] = ctx; else delete _saveBarCtxs[surface];
  refreshSaveBar();
}
function _currentSaveBarCtx() {
  if (_modalLayerOpen()) return null; // 弹窗（含变更预览/确认）盖住时收起保存条
  for (const k of _SAVE_BAR_ORDER) {
    const c = _saveBarCtxs[k];
    if (c && c.host() && c.dirty()) return c;
  }
  return null;
}
function refreshSaveBar() {
  const bar = _('saveDiscardBar');
  if (!bar) return;
  const ctx = _currentSaveBarCtx();
  bar.classList.toggle('show', !!ctx);
  // The bar fades out with opacity only; without this its buttons stay tabbable.
  if (ctx) {
    bar.removeAttribute('inert');
    bar.removeAttribute('aria-hidden');
  } else {
    bar.setAttribute('inert', '');
    bar.setAttribute('aria-hidden', 'true');
  }
  const diffBtn = _('saveBarDiff');
  if (diffBtn) diffBtn.hidden = !(ctx && typeof ctx.diff === 'function');
  // 有未保存更改时，关闭/刷新页面前提示（浏览器统一展示原生确认文案）
  // 这里不看 host：脏数据藏在别的子页或收起的抽屉里时同样要提醒，避免静默丢失
  const anyDirty = Object.keys(_saveBarCtxs).some(k => {
    const c = _saveBarCtxs[k];
    return !!c && c.dirty();
  });
  if (anyDirty) window.onbeforeunload = e => { e.preventDefault(); return ''; };
  else window.onbeforeunload = null;
}
function discardSaveBar() { const c = _currentSaveBarCtx(); if (c) c.discard(); }
function saveViaBar() { const c = _currentSaveBarCtx(); if (c) c.save(); }
async function diffViaBar() {
  const c = _currentSaveBarCtx();
  if (!c || typeof c.diff !== 'function') return;
  let changes = [];
  try { changes = (await c.diff()) || []; } catch (e) { toast(t('无法生成差异预览'), 'error'); return; }
  showDiffModal(changes);
}
// 表单任意输入都会冒泡到这里，统一做脏检查（事件委托，兼容动态生成的行）
document.addEventListener('input', refreshSaveBar, true);
document.addEventListener('change', refreshSaveBar, true);

function fmtDiffVal(v) {
  if (v === undefined || v === null) return '';
  if (Array.isArray(v)) v = v.map(x => (x && typeof x === 'object') ? JSON.stringify(x) : String(x)).join(', ');
  else if (typeof v === 'object') v = JSON.stringify(v);
  else v = String(v);
  if (v.length > 90) v = v.slice(0, 90) + '…';
  return v;
}
// 两个同构浅对象（值可为标量/数组）→ [{label, from, to}]；值应已经 fmtDiffVal 字符串化
function diffFlat(base, cur) {
  const out = [];
  const keys = Object.keys(base);
  Object.keys(cur).forEach(k => { if (keys.indexOf(k) === -1) keys.push(k); });
  for (const k of keys) {
    const a = base[k] === undefined ? '' : base[k];
    const b = cur[k] === undefined ? '' : cur[k];
    if (String(a) === String(b)) continue;
    out.push({ label: k, from: String(a), to: String(b) });
  }
  return out;
}
const SENSITIVE_LABEL_RE = /(key|token|secret|password|authorization)/i;
let _diffResolve = null;
function showDiffModal(changes) {
  return new Promise(resolve => {
    const list = _('diffList');
    if (list) list.textContent = '';
    (changes || []).forEach(ch => {
      const sens = SENSITIVE_LABEL_RE.test(ch.label);
      const row = document.createElement('div'); row.className = 'diff-row';
      const lab = document.createElement('span'); lab.className = 'diff-label'; lab.textContent = ch.label;
      const from = document.createElement('span'); from.className = 'diff-from';
      const to = document.createElement('span'); to.className = 'diff-to';
      if (sens) {
        from.textContent = ch.from ? '••••' : '';
        to.textContent = ch.to ? '••••' : '';
        if (ch.from || ch.to) from.title = to.title = t('敏感值已隐藏');
      } else {
        from.textContent = ch.from;
        to.textContent = ch.to;
      }
      const arrow = document.createElement('span'); arrow.className = 'diff-arrow'; arrow.textContent = '→';
      row.appendChild(lab); row.appendChild(from); row.appendChild(arrow); row.appendChild(to);
      if (list) list.appendChild(row);
    });
    const m = _('diffModal');
    if (m) m.style.display = 'flex';
    _diffResolve = resolve;
  });
}
function resolveDiff(val) {
  const m = _('diffModal');
  if (m) m.style.display = 'none';
  if (_diffResolve) { const r = _diffResolve; _diffResolve = null; r(!!val); }
}

// ========== 侧栏收起 ==========
const NAV_COLLAPSE_KEY = 'cline_admin_nav';
// 轨道提示单例元素引用：必须在 syncNavCollapseBtn() 首次顶层调用之前初始化，
// 否则 _navTipHide 读取 TDZ 中的 let 会抛错并中断后续脚本（悬停监听器将不会注册）。
let _navRailTipEl = null;
function syncNavCollapseBtn() {
  const collapsed = document.documentElement.dataset.nav === 'collapsed';
  const label = collapsed ? t('展开侧栏') : t('收起侧栏');
  // 头部按钮（仅窄屏可见）与边缘悬浮钮（仅桌面可见）共用文案；图标翻转由 CSS 完成，
  // 不再重写 innerHTML
  document.querySelectorAll('.nav-collapse-btn,.nav-edge-toggle').forEach(btn => {
    btn.title = label; btn.setAttribute('aria-label', label);
  });
  _navTipHide(); // 状态切换后旧提示文案即失效
}
function toggleNav() {
  const collapsed = document.documentElement.dataset.nav === 'collapsed';
  if (collapsed) delete document.documentElement.dataset.nav;
  else document.documentElement.dataset.nav = 'collapsed';
  try { localStorage.setItem(NAV_COLLAPSE_KEY, collapsed ? 'expanded' : 'collapsed'); } catch (e) {}
  syncNavCollapseBtn();
}
syncNavCollapseBtn();

// ========== 收起态轨道提示（控制台 nav-tooltip 等价物） ==========
// 单例固定元素；仅 data-nav=collapsed 且宽屏时显示，文案取 .nav-label 文本节点
// （applyLang 已保证语言正确），因此无需额外的提示词条映射。
function _navTipHide() { if (_navRailTipEl) _navRailTipEl.classList.remove('show'); }
function _navTipShow(text, centerY) {
  if (window.innerWidth <= 760) return;
  if (!_navRailTipEl) {
    _navRailTipEl = document.createElement('div');
    _navRailTipEl.className = 'nav-rail-tip';
    document.body.appendChild(_navRailTipEl);
  }
  const tip = _navRailTipEl;
  tip.textContent = text;
  tip.style.display = 'block';
  const h = tip.getBoundingClientRect().height;
  tip.style.left = '72px';
  tip.style.top = Math.max(6, Math.min(centerY - h / 2, window.innerHeight - h - 6)) + 'px';
  tip.classList.add('show');
  tip.style.display = '';
}
(function initNavRailTip() {
  const nav = document.querySelector('.sidebar-nav');
  if (!nav) return;
  const collapsed = () => document.documentElement.dataset.nav === 'collapsed';
  const itemOf = e => {
    const it = e.target && e.target.closest ? e.target.closest('.nav-item') : null;
    return it && nav.contains(it) ? it : null;
  };
  const showForItem = e => {
    if (!collapsed()) return;
    const it = itemOf(e);
    if (!it) return;
    const lab = it.querySelector('.nav-label');
    if (!lab || !lab.textContent) return;
    const r = it.getBoundingClientRect();
    _navTipShow(lab.textContent, r.top + r.height / 2);
  };
  nav.addEventListener('mouseover', showForItem);
  nav.addEventListener('mouseout', e => { if (itemOf(e)) _navTipHide(); });
  nav.addEventListener('click', _navTipHide);
  nav.addEventListener('scroll', _navTipHide);
  const edge = document.querySelector('.nav-edge-toggle');
  if (edge) {
    const show = () => {
      const label = document.documentElement.dataset.nav === 'collapsed' ? t('展开侧栏') : t('收起侧栏');
      _navTipShow(label + ' · Ctrl+B', 56 + 12);
    };
    edge.addEventListener('mouseenter', show);
    edge.addEventListener('mouseleave', _navTipHide);
    edge.addEventListener('focus', show);
    edge.addEventListener('blur', _navTipHide);
  }
})();

// Ctrl/Cmd+B 收起/展开侧栏（输入控件聚焦时不拦截）
document.addEventListener('keydown', e => {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === 'b' || e.key === 'B')) {
    const evTarget = e.target;
    if (evTarget && (evTarget.tagName === 'INPUT' || evTarget.tagName === 'TEXTAREA' || evTarget.isContentEditable)) return;
    e.preventDefault();
    toggleNav();
  }
});

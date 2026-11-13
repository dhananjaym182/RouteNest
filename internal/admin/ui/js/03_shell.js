// ========== API 请求 ==========
function adminCookie(name) {
  const prefix = name + '=';
  for (const part of document.cookie.split(';')) {
    const value = part.trim();
    if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
  }
  return '';
}

let _nativeAdminCSRF = '';
let _nativeGatewayURL = '';

async function initializeNativeConsoleTransport() {
  if (!nativeDesktopAvailable()) return;
  const status = await nativeDesktopCall('DesktopStatus');
  if (status && typeof status.gatewayURL === 'string') {
    const gateway = new URL(status.gatewayURL);
    if (!['http:', 'https:'].includes(gateway.protocol) || gateway.username || gateway.password || gateway.search || gateway.hash || gateway.pathname !== '/v1') throw new Error('invalid_gateway_url');
    _nativeGatewayURL = gateway.href;
  }
}

async function adminFetch(method, path, body) {
  if (nativeDesktopAvailable()) {
    const bridge = nativeDesktopBridge();
    if (!bridge || typeof bridge.AdminRequest !== 'function') throw new Error('bridge_unavailable');
    const request = { method, path: API + path };
    if (body !== undefined && body !== null) request.body = JSON.stringify(body);
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') request.csrf = _nativeAdminCSRF;
    const result = await bridge.AdminRequest(request);
    if (!result || !Number.isInteger(result.status) || typeof result.body !== 'string' || result.status < 100 || result.status > 599) throw new Error('invalid_response');
    if (typeof result.csrf === 'string') _nativeAdminCSRF = result.csrf;
    else if (result.status === 401 || result.status >= 200 && result.status < 300) _nativeAdminCSRF = '';
    return new Response(result.body || null, { status: result.status, headers: { 'Content-Type': result.contentType || 'application/json' } });
  }
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
  if (nativeDesktopAvailable()) return _nativeGatewayURL || '—';
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
    // Only swap the icon SVG — the label <span> must stay intact
    const icon = btn.querySelector('svg[aria-hidden]');
    const tickSVG = '<svg viewBox="0 0 24 24" fill="none" stroke="var(--green)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12"/></svg>';
    const prevSVG = icon ? icon.outerHTML : '';
    if (icon) icon.outerHTML = tickSVG;
    btn.classList.add('is-copied');
    setTimeout(() => {
      const tick = btn.querySelector('svg[aria-hidden]');
      if (tick && prevSVG) tick.outerHTML = prevSVG;
      btn.classList.remove('is-copied');
    }, 1200);
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
let _adminLoginFocusEstablished = false;
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
  const ssoProviders = (_adminAuthStatus && _adminAuthStatus.ssoProviders) || [];
  const hasSSO = Array.isArray(ssoProviders) && ssoProviders.some(p => p.enabled && p.ready && (p.type === 'oidc' || p.type === 'saml'));
  if (_('loginLocalHelp')) _('loginLocalHelp').style.display = (!bootstrap && hasSSO) ? '' : 'none';
  const forgot = _('forgotPasswordButton');
  const hasRecoveryCode = !!(_adminAuthStatus && _adminAuthStatus.recoveryCodeAvailable);
  if (forgot) {
    forgot.hidden = _adminAuthMode !== 'login';
    // Without a recovery code the link is a disclosure for the reset guidance, not a page link.
    if (hasRecoveryCode) forgot.removeAttribute('aria-expanded');
    else forgot.setAttribute('aria-expanded', 'false');
  }
  // The reset guidance stays out of the page until the user asks for it via "Forgot password?".
  const resetHint = _('noRecoveryCodeHint');
  if (resetHint) { resetHint.hidden = true; resetHint.textContent = ''; }
  renderBootstrapSecretHelp(bootstrap);
  const desktopTrust = _('desktopTrustLoginRow');
  if (desktopTrust) desktopTrust.hidden = _adminAuthMode !== 'login' || !nativeDesktopAvailable();
  const browserNotice = _('browserLoginNotice');
  if (browserNotice) browserNotice.textContent = t(nativeDesktopAvailable() ? '可选择信任此设备。RouteNest 只保存可撤销的操作系统保护凭据，不保存密码。' : '普通浏览器需要独立登录，不会使用桌面设备凭据。');
  const title = _('loginTitle'), help = _('loginHelp'), button = _('loginSubmit');
  if (_adminAuthMode === 'bootstrap') {
    if (title) title.textContent = t('初始化管理员');
    if (help) help.textContent = t('输入启动 RouteNest 时显示的一次性密钥，并设置永久密码。');
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
    if (title) title.textContent = t('登录');
    if (help) help.textContent = t('请输入管理员用户名和密码。');
    if (button) button.textContent = t('登录');
  }
  renderSSOLoginProviders();
  if (typeof updateAdminAuthPageTitle === 'function') updateAdminAuthPageTitle();
}

// Where the one-time key comes from depends on how RouteNest was started, so say so per platform.
function renderBootstrapSecretHelp(visible) {
  const box = _('bootstrapSecretHelp');
  if (!box) return;
  box.textContent = '';
  box.hidden = !visible;
  if (!visible) return;
  const title = document.createElement('strong');
  title.textContent = t('到哪里找这个密钥？');
  const list = document.createElement('ul');
  for (const line of [
    '在终端运行 RouteNest（Windows、macOS、Linux）：密钥已打印在启动它的终端或控制台窗口中。重新启动 RouteNest 会生成并打印新的密钥。',
    'Docker、系统服务或没有终端时：使用首次启动前设置的 ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD 的值。',
    '桌面应用不需要此密钥：请在应用自带的设置窗口中创建密码。',
  ]) {
    const item = document.createElement('li');
    item.textContent = t(line);
    list.appendChild(item);
  }
  box.appendChild(title);
  box.appendChild(list);
}

function focusAdminLoginTarget(attempt = 0) {
  const root = document.documentElement;
  if (root && root.dataset.adminAuthView !== 'login') return;
  const target = (_adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery') ? _('bootstrapSecret') : _('loginPassword');
  const active = document.activeElement;
  const activeInAuthPage = active && typeof active.closest === 'function' ? active.closest('#adminAuthPage') : active && active !== document.body;
  if (_adminLoginFocusEstablished && activeInAuthPage) return;
  if (target && !target.disabled && !target.closest('[inert]')) {
    target.focus();
    if (document.activeElement === target) {
      _adminLoginFocusEstablished = true;
      return;
    }
  }
  if (attempt < 4) setTimeout(() => focusAdminLoginTarget(attempt + 1), 50);
}

function showLogin(mode) {
  renderAdminPrincipal(null);
  setAdminAuthMode(mode || _adminAuthMode);
  if (typeof syncAdminAuthRoute === 'function') syncAdminAuthRoute();
  else if (_('loginOverlay')) _('loginOverlay').style.display = 'flex';
  setTimeout(() => focusAdminLoginTarget(), 50);
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
  if (_('loginLocalHelp')) _('loginLocalHelp').style.display = available.length ? '' : 'none';
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
    if (typeof rememberAdminAuthDestination === 'function') rememberAdminAuthDestination();
    const returnLocation = location.pathname + '#' + (typeof _adminAuthReturnTab === 'string' ? _adminAuthReturnTab : 'dashboard');
    const response = await adminFetch('POST', '/auth/' + provider.type + '/start', { provider: provider.id, returnLocation });
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
  if (button) button.disabled = true;
  const username = (_('loginUsername') && _('loginUsername').value.trim()) || 'admin';
  const pwd = (_('loginPassword') && _('loginPassword').value) || '';
  const secret = (_('bootstrapSecret') && _('bootstrapSecret').value) || '';
  const newPassword = (_('loginNewPassword') && _('loginNewPassword').value) || '';
  const confirm = (_('loginConfirmPassword') && _('loginConfirmPassword').value) || '';
  const bootstrap = _adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery';
  const rotating = bootstrap || _adminAuthMode === 'legacy';
  if (!bootstrap && !pwd) { _('loginError').textContent = t('请输入管理员密码'); if (button) button.disabled = false; return; }
  if (bootstrap && !secret) { _('loginError').textContent = t('请输入一次性本机密钥'); if (button) button.disabled = false; return; }
  if (rotating && (adminPasswordTooShort(newPassword) || newPassword !== confirm)) {
    _('loginError').textContent = adminPasswordTooShort(newPassword) ? t('永久密码至少 8 位') : t('两次输入的密码不一致');
    if (button) button.disabled = false;
    return;
  }
  _('loginError').textContent = '';
  try {
    let path = '/login', body = { username: username, password: pwd };
    if (_adminAuthMode === 'bootstrap' || _adminAuthMode === 'recovery') {
      path = '/auth/bootstrap'; body = { secret: secret, password: newPassword };
    } else if (_adminAuthMode === 'legacy') {
      path = '/auth/legacy/rotate'; body = { username: username, oldPassword: pwd, newPassword: newPassword };
    }
    if (_adminAuthMode === 'login' && nativeDesktopAvailable()) {
      const trustDevice = !!(_('desktopTrustLoginCheck') && _('desktopTrustLoginCheck').checked);
      const result = await nativeDesktopCall('SignIn', { username, password: pwd, trustDevice });
      if (!result || typeof result.handoffToken !== 'string' || !result.handoffToken || result.adminURL !== '/admin/') throw new Error(t('登录失败'));
      if (_('loginPassword')) _('loginPassword').value = '';
      location.replace(result.adminURL + '#desktop-handoff=' + encodeURIComponent(result.handoffToken));
      return;
    }
    const res = await adminFetch('POST', path, body);
    const data = await res.json().catch(() => ({}));
    const issued = (data && data.data) || {};
    const restartRequired = data.error === 'security_restart_required' || issued.restartRequired === true;
    if (bootstrap && typeof issued.recoveryCode === 'string' && issued.recoveryCode && ((res.ok && data.success) || restartRequired)) {
      showSetupRecoveryCode(issued.recoveryCode, restartRequired);
      return;
    }
    if (res.ok && data.success) {
      location.reload();
    } else if (data.error === 'password_rotation_required') {
      setAdminAuthMode('legacy');
      _('loginError').textContent = t('此安装仍使用旧密码格式，请设置新的永久密码。');
    } else {
      _('loginError').textContent = data.error || t('登录失败');
      if (_('loginPassword')) _('loginPassword').value = '';
    }
  } catch (reason) {
    const code = typeof desktopSecurityErrorParts === 'function' ? desktopSecurityErrorParts(reason).code : reason.message;
    if (code === 'password_rotation_required') {
      setAdminAuthMode('legacy');
      _('loginError').textContent = t('此安装仍使用旧密码格式，请设置新的永久密码。');
    } else {
      _('loginError').textContent = nativeDesktopAvailable() ? desktopSecurityErrorText(reason, '登录失败') : t('网络错误，请重试');
    }
    if (_('loginPassword')) _('loginPassword').value = '';
  }
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
  const root = document.documentElement;
  if (root) root.dataset.adminAuth = _adminPrincipal ? 'authenticated' : 'signed-out';
  if (_adminPrincipal && typeof updateAdminAuthPageTitle === 'function') updateAdminAuthPageTitle();
  const authPage = _('adminAuthPage');
  if (authPage) authPage.hidden = !!_adminPrincipal;
  const login = _('loginOverlay');
  const recovery = _('passwordRecoveryOverlay');
  if (_adminPrincipal) {
    if (login) { login.hidden = true; login.style.display = 'none'; }
    if (recovery) { recovery.hidden = true; recovery.style.display = 'none'; }
    if (typeof restoreAdminAuthDestination === 'function') restoreAdminAuthDestination();
  } else if (typeof showAdminAuthPage === 'function') {
    showAdminAuthPage(root && root.dataset.adminAuthView || 'login');
  } else if (login) { login.hidden = false; login.style.display = 'flex'; }
  const identity = _('adminIdentity');
  if (identity) identity.textContent = principal ? principal.username : t('未登录');
  renderAdminRoleBadges(_('adminRoles'), principal && principal.roles);
  const method = _('adminAuthMethod');
  if (method) method.textContent = principal ? (principal.method === 'local' ? t('本地账号') : String(principal.method).toUpperCase() + (principal.provider ? ' · ' + principal.provider : '')) : '—';
  if (_('localAdminPasswordForm')) _('localAdminPasswordForm').hidden = !principal || principal.method !== 'local';
  if (_('ssoAdminPasswordNotice')) _('ssoAdminPasswordNotice').hidden = !principal || principal.method === 'local';
  applyAdminPermissionVisibility();
  const recoveryCodeManagement = _('recoveryCodeManagement');
  if (recoveryCodeManagement) {
    recoveryCodeManagement.hidden = !principal || principal.userID !== 'break-glass' || principal.method !== 'local' || !adminHasPermission('security.write');
  }
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
  if (typeof applyCredentialAccess === 'function') applyCredentialAccess();
  if (typeof applyRoutingAccess === 'function') applyRoutingAccess();
  document.querySelectorAll('.page-subtabs').forEach(nav => {
    const active = nav.querySelector('.subtab.active');
    if (!active || !active.hidden || typeof switchPageSubTab !== 'function') return;
    const fallback = Array.from(nav.querySelectorAll('.subtab')).find(tab => !tab.hidden);
    if (fallback) switchPageSubTab(nav.dataset.page, fallback.dataset.sub, true);
  });
}

async function initializeAdminAuth() {
  try {
    await initializeNativeConsoleTransport();
    if (typeof consumeDesktopSessionHandoff === 'function') await consumeDesktopSessionHandoff();
    const statusResponse = await adminFetch('GET', '/auth/status');
    const statusEnvelope = await statusResponse.json();
    if (!statusResponse.ok || !statusEnvelope.success) throw new Error(statusEnvelope.error || 'security_unavailable');
    _adminAuthStatus = statusEnvelope.data || {};
    if (typeof renderRecoveryCodeStatus === 'function') renderRecoveryCodeStatus();
    renderSSOLoginProviders();
    if (_adminAuthStatus.bootstrapRequired) { showLogin('bootstrap'); return false; }
    if (_adminAuthStatus.recoveryRequired) { showLogin('recovery'); return false; }
    const sessionResponse = await adminFetch('GET', '/auth/session');
    const sessionEnvelope = await sessionResponse.json().catch(() => ({}));
    if (sessionResponse.status === 401) {
      showLogin(_adminAuthStatus.legacyRotationRequired ? 'legacy' : 'login');
      return false;
    }
    const principal = sessionEnvelope.data && sessionEnvelope.data.principal;
    if (!sessionResponse.ok || !sessionEnvelope.success || !principal || typeof principal.userID !== 'string' || !principal.userID || typeof principal.username !== 'string' || !Array.isArray(principal.roles)) throw new Error(sessionEnvelope.error || 'session_unavailable');
    renderAdminPrincipal(principal);
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
    if (nativeDesktopAvailable()) {
      const result = await nativeDesktopCall('SignOutDesktop');
      _nativeAdminCSRF = '';
      if (result && result.automaticSignInPauseIncomplete) toast(t('会话已结束，但无法确认自动登录已暂停。请修复凭据存储或通过浏览器撤销此设备。'), 'error');
      else if (result && result.storageWarning) toast(t('会话已结束，但本机自动登录状态更新失败。服务器端自动登录已暂停。'), 'error');
    } else {
      await api('POST', '/logout', {});
    }
    renderAdminPrincipal(null);
    location.reload();
  } catch (reason) {
    const error = _('desktopSignInError');
    const message = nativeDesktopAvailable() ? desktopSecurityErrorText(reason, '退出登录失败') : reason.message || t('退出登录失败');
    if (error) error.textContent = message;
    else toast(message, 'error');
  }
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
  if (themeMeta) themeMeta.setAttribute('content', theme === 'dark' ? '#0C111D' : '#F7F8FA');
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
  const sidebar = _('appSidebar');
  if (main) {
    if (open) main.setAttribute('inert', '');
    else main.removeAttribute('inert');
  }
  if (sidebar) {
    if (window.innerWidth <= 900 && !open) sidebar.setAttribute('inert', '');
    else sidebar.removeAttribute('inert');
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
function syncMobileNavState() {
  if (window.innerWidth > 900) closeMobileNav();
  else toggleMobileNav(document.documentElement.dataset.mobileNav === 'open');
}
window.addEventListener('resize', syncMobileNavState);
syncMobileNavState();

// ========== 抽屉（右侧滑出编辑器） ==========
const _openDrawers = [];
const _drawerReturnFocus = {};
const _focusableSelector = 'a[href],button:not(:disabled),input:not(:disabled):not([type="hidden"]),select:not(:disabled),textarea:not(:disabled),[tabindex]:not([tabindex="-1"])';
function visibleFocusable(root) {
  if (!root) return [];
  return Array.from(root.querySelectorAll(_focusableSelector)).filter(el => !el.closest('[inert]') && el.getClientRects().length > 0);
}
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
  if (!dr.classList.contains('open')) _drawerReturnFocus[name] = document.activeElement;
  if (ov) ov.classList.add('open');
  dr.classList.add('open');
  setDrawerInert(dr, false);
  if (!dr.hasAttribute('tabindex')) dr.tabIndex = -1;
  if (!dr.contains(document.activeElement)) dr.focus({ preventScroll: true });
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
  const returnFocus = _drawerReturnFocus[name];
  delete _drawerReturnFocus[name];
  if (returnFocus && returnFocus.isConnected && typeof returnFocus.focus === 'function') {
    setTimeout(() => returnFocus.focus(), 0);
  }
}
// ESC closes the topmost drawer, but only when no modal layer is open on top
// of it (modal Esc handling lives in 13_models and runs on the same event).
const _ESC_MODAL_IDS = ['confirmModal','diffModal','modelProviderActionModal','modelMetaEditor','provPickerModal','comboModelPicker','presetPickerModal','importPreviewModal','connLoginModal','connImportModal','comboTestModal','modelSyncOverlay','logDetailModal'];
function _modalLayerOpen() {
  return _ESC_MODAL_IDS.some(id => { const el = _(id); return el && el.style.display === 'flex'; });
}
document.addEventListener('keydown', e => {
  if (e.key === 'Tab' && _openDrawers.length && !_modalLayerOpen()) {
    const dr = _(_openDrawers[_openDrawers.length - 1]);
    const items = visibleFocusable(dr);
    if (!items.length) { e.preventDefault(); if (dr) dr.focus(); return; }
    const first = items[0], last = items[items.length - 1];
    if (!dr.contains(document.activeElement) || (e.shiftKey && document.activeElement === first) || (!e.shiftKey && document.activeElement === last)) {
      e.preventDefault();
      (e.shiftKey ? last : first).focus();
      return;
    }
  }
  if (e.key === 'Escape' && document.documentElement.dataset.mobileNav === 'open') { closeMobileNav(); return; }
  if (e.key === 'Escape' && _openDrawers.length && !_modalLayerOpen()) closeDrawer(_openDrawers[_openDrawers.length - 1]);
});
// Drawers ship closed in the markup, so seed inert before the first open.
document.querySelectorAll('.drawer').forEach(dr => setDrawerInert(dr, !dr.classList.contains('open')));

// Modal overlays use several feature-specific open/close functions. Observe the
// common visibility boundary so every modal gets the same focus trap and return.
const _modalStack = [];
const _modalReturnFocus = new WeakMap();
const _modalBackgroundInert = new Map();
const _modalVisible = el => !!el && !el.hidden && getComputedStyle(el).display !== 'none';
function syncModalBackgroundInert() {
  const top = [..._modalStack].reverse().find(_modalVisible);
  for (const [element, previous] of _modalBackgroundInert) {
    if (previous) element.setAttribute('inert', '');
    else element.removeAttribute('inert');
  }
  _modalBackgroundInert.clear();
  if (!top) return;
  for (const element of document.body.children) {
    if (element === top || element.contains(top)) continue;
    _modalBackgroundInert.set(element, element.hasAttribute('inert'));
    element.setAttribute('inert', '');
  }
}
function modalFocusScope(overlay) {
  return overlay && (overlay.querySelector('[role="alertdialog"],[role="dialog"],.modal-card,.login-card,#modelSyncModal') || overlay);
}
function syncModalFocusState(overlay) {
  const visible = _modalVisible(overlay);
  const index = _modalStack.indexOf(overlay);
  if (visible && index < 0) {
    _modalReturnFocus.set(overlay, overlay.contains(document.activeElement) ? null : document.activeElement);
    _modalStack.push(overlay);
    requestAnimationFrame(() => {
      if (!_modalVisible(overlay) || overlay.contains(document.activeElement)) return;
      const scope = modalFocusScope(overlay);
      const target = visibleFocusable(scope)[0] || scope;
      if (target && !target.hasAttribute('tabindex') && target === scope) target.tabIndex = -1;
      if (target) target.focus({ preventScroll: true });
    });
  } else if (!visible && index >= 0) {
    _modalStack.splice(index, 1);
    const beneath = _modalStack[_modalStack.length - 1];
    const returnFocus = _modalReturnFocus.get(overlay);
    _modalReturnFocus.delete(overlay);
    setTimeout(() => {
      if (returnFocus && returnFocus.isConnected && (!beneath || beneath.contains(returnFocus))) {
        returnFocus.focus();
      } else if (beneath && _modalVisible(beneath)) {
        const scope = modalFocusScope(beneath);
        const target = visibleFocusable(scope)[0] || scope;
        if (target) target.focus({ preventScroll: true });
      } else if (returnFocus && returnFocus.isConnected && typeof returnFocus.focus === 'function') {
        returnFocus.focus();
      }
    }, 0);
  }
  syncModalBackgroundInert();
}
const _modalOverlays = Array.from(document.querySelectorAll('.overlay-modal,#modelSyncOverlay'));
const _modalObserver = new MutationObserver(records => records.forEach(record => syncModalFocusState(record.target)));
_modalOverlays.forEach(overlay => {
  _modalObserver.observe(overlay, { attributes: true, attributeFilter: ['style', 'class', 'hidden'] });
  syncModalFocusState(overlay);
});
document.addEventListener('keydown', event => {
  if (event.key !== 'Tab') return;
  const overlay = [..._modalStack].reverse().find(_modalVisible);
  if (!overlay) return;
  const scope = modalFocusScope(overlay);
  const items = visibleFocusable(scope);
  if (!items.length) { event.preventDefault(); scope.focus(); event.stopImmediatePropagation(); return; }
  const first = items[0], last = items[items.length - 1];
  if (!scope.contains(document.activeElement) || (event.shiftKey && document.activeElement === first) || (!event.shiftKey && document.activeElement === last)) {
    event.preventDefault();
    (event.shiftKey ? last : first).focus();
    event.stopImmediatePropagation();
  }
}, true);

// ========== 未保存更改：悬浮保存条 + 变更预览弹窗 ==========
var _verCheckDone = false; // 「关于」页更新检查只自动跑一次（checkForUpdate 在 05_settings 定义）
// 每个面（provider 抽屉 / 路由页调优卡）注册一个 ctx；同一时刻只展示优先级最高且脏的那个。
const _saveBarCtxs = {};
const _SAVE_BAR_ORDER = ['provider', 'combo', 'tuning'];
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

// Administrator recovery and desktop-session handoff. Recovery always requires
// the durable credential; localhost or an application window grants no authority.
let _passwordRecoveryCompleted = false;
let _passwordRecoveryRestartRequired = false;
let _passwordRecoveryBusy = false;
// The result screen is shared: after a password reset it shows the replacement code; after first-run
// (or offline-recovery) setup it shows the code issued with the new password.
let _passwordRecoverySetup = false;
let _issuedRecoveryCodeRestartRequired = false;
let _adminAuthReturnTab = 'dashboard';
const ADMIN_AUTH_DESTINATIONS = new Set(['dashboard', 'providers', 'credentials', 'network', 'models', 'routing', 'combos', 'logs', 'quota', 'health', 'security', 'about', 'quickstart', 'global', 'accounts', 'import', 'upstreams']);

function rememberAdminAuthDestination() {
  const requested = String(location.hash || '').replace(/^#/, '');
  const remembered = window.history && window.history.state && window.history.state.routenestAuthReturnTab;
  if (ADMIN_AUTH_DESTINATIONS.has(requested)) _adminAuthReturnTab = requested;
  else if (ADMIN_AUTH_DESTINATIONS.has(remembered)) _adminAuthReturnTab = remembered;
}

function setAdminAuthRoute(view, push = false) {
  const hash = view === 'login' ? '#login' : '#recover';
  if (location.hash === hash || !window.history) return;
  const method = push ? 'pushState' : 'replaceState';
  window.history[method]({ routenestAuthReturnTab: _adminAuthReturnTab }, '', location.pathname + hash);
}

function restoreAdminAuthDestination() {
  if (!['#login', '#recover'].includes(location.hash)) return;
  rememberAdminAuthDestination();
  window.history.replaceState(null, '', location.pathname + '#' + _adminAuthReturnTab);
}

function updateAdminAuthPageTitle() {
  const appTitle = LANG === 'en' ? 'RouteNest Admin' : 'RouteNest 管理面板';
  // Signed in: "<page> | RouteNest", following the page name shown in the top bar.
  if (_adminPrincipal) {
    const page = _('currentPageTitle');
    const name = page && page.textContent ? page.textContent.trim() : '';
    document.title = name ? name + ' | RouteNest' : appTitle;
    return;
  }
  const root = document.documentElement;
  // Still verifying the session: do not claim "Sign in" yet.
  if (root && root.dataset.adminAuth === 'pending') { document.title = appTitle; return; }
  const view = root && root.dataset.adminAuthView;
  const heading = view === 'recovery-result' ? (_passwordRecoverySetup ? '保存恢复码' : '保存新的恢复码') : view === 'recover' ? '重置管理员密码' : _adminAuthMode === 'bootstrap' ? '初始化管理员' : _adminAuthMode === 'recovery' ? '完成离线恢复' : _adminAuthMode === 'legacy' ? '升级管理员密码' : '登录';
  document.title = t(heading) + ' | RouteNest';
}

function showAdminAuthPage(view) {
  const root = document.documentElement;
  if (root) root.dataset.adminAuthView = view;
  const page = _('adminAuthPage');
  if (page) page.hidden = false;
  const login = _('loginOverlay');
  const recovery = _('passwordRecoveryOverlay');
  if (login) { login.hidden = view !== 'login'; login.style.display = view === 'login' ? 'flex' : 'none'; }
  if (recovery) {
    recovery.hidden = view === 'login';
    recovery.style.display = view === 'login' ? 'none' : 'flex';
    recovery.setAttribute('aria-labelledby', view === 'recovery-result' ? 'passwordRecoveryResultTitle' : 'passwordRecoveryTitle');
    if (view === 'recovery-result') recovery.removeAttribute('aria-describedby');
    else recovery.setAttribute('aria-describedby', 'passwordRecoveryHelp');
  }
  updateAdminAuthPageTitle();
}

function clearPasswordRecoveryInputs() {
  for (const id of ['passwordRecoveryCode', 'passwordRecoveryNewPassword', 'passwordRecoveryConfirmPassword']) {
    if (_(id)) _(id).value = '';
  }
  if (_('passwordRecoveryReplacementCode')) _('passwordRecoveryReplacementCode').textContent = '';
}

function syncAdminAuthRoute() {
  const root = document.documentElement;
  if (!root || root.dataset.adminAuth === 'pending') return;
  if (_adminPrincipal) { restoreAdminAuthDestination(); return; }
  rememberAdminAuthDestination();
  if (_passwordRecoveryBusy || _passwordRecoveryCompleted) {
    setAdminAuthRoute('recover');
    showAdminAuthPage(_passwordRecoveryCompleted ? 'recovery-result' : 'recover');
    return;
  }
  if (location.hash === '#recover' && _adminAuthMode === 'login' && _adminAuthStatus && _adminAuthStatus.recoveryCodeAvailable) {
    if (root.dataset.adminAuthView !== 'recover') openPasswordRecovery(false);
    return;
  }
  clearPasswordRecoveryInputs();
  setAdminAuthRoute('login');
  showAdminAuthPage('login');
}

async function consumeDesktopSessionHandoff() {
  const prefix = '#desktop-handoff=';
  if (!location.hash.startsWith(prefix)) return;
  const token = decodeURIComponent(location.hash.slice(prefix.length));
  rememberAdminAuthDestination();
  history.replaceState(null, '', location.pathname + '#' + _adminAuthReturnTab);
  if (!token) return;
  const response = await adminFetch('POST', '/auth/desktop-handoff', { token });
  const envelope = await response.json().catch(() => ({}));
  if (!response.ok || !envelope.success) throw new Error(envelope.error || 'desktop_handoff_failed');
}

// Wording for the shared result screen. Reset and setup differ only in copy and in what
// "continue" does; the code box, copy button and save confirmation are identical.
function setRecoveryResultCopy(setup, restartRequired) {
  const set = (id, key) => { if (_(id)) _(id).textContent = t(key); };
  set('passwordRecoveryResultTitle', setup ? '保存恢复码' : '保存新的恢复码');
  set('passwordRecoveryResultIntro', setup ? '管理员密码已设置。此恢复码只显示一次，忘记密码时需要用它重置。' : '旧恢复码已失效。此新恢复码只显示一次。');
  set('passwordRecoverySavedLabel', setup ? '我已安全保存恢复码。' : '我已安全保存新的恢复码。');
  set('passwordRecoveryRestartNotice', setup ? '管理员密码已设置，但安全存储需要重启后才能完成。请先保存恢复码，重启 RouteNest，然后重新加载此页面并登录。' : '恢复已持久化，但安全存储需要重启后完成恢复。保存恢复码，然后关闭并重新打开 RouteNest。');
  set('passwordRecoveryFinish', setup ? (restartRequired ? '我已重启，重新加载页面' : '继续进入控制台') : (restartRequired ? '关闭并重新打开 RouteNest' : '返回登录'));
}

// First-run (or offline-recovery) setup succeeded: the server issued a recovery code with the
// password. Show it exactly once and require the user to confirm they saved it.
function showSetupRecoveryCode(code, restartRequired) {
  _passwordRecoveryCompleted = true;
  _passwordRecoverySetup = true;
  _passwordRecoveryRestartRequired = !!restartRequired;
  for (const id of ['bootstrapSecret', 'loginNewPassword', 'loginConfirmPassword', 'loginPassword']) if (_(id)) _(id).value = '';
  _('passwordRecoveryReplacementCode').textContent = code;
  _('passwordRecoverySaved').checked = false;
  _('passwordRecoveryRestartNotice').hidden = !restartRequired;
  setRecoveryResultCopy(true, !!restartRequired);
  updatePasswordRecoveryFinish();
  _('passwordRecoveryFormView').hidden = true;
  _('passwordRecoveryResultView').hidden = false;
  setAdminAuthRoute('recover');
  showAdminAuthPage('recovery-result');
  setTimeout(() => _('passwordRecoveryReplacementCode').focus(), 40);
}

// "Forgot password?": with a recovery code it opens the reset page. Without one there is nothing to
// reset with, so it reveals (or hides again) how an administrator can recover, only on request.
function onForgotPassword() {
  if (_adminAuthStatus && _adminAuthStatus.recoveryCodeAvailable) { openPasswordRecovery(); return; }
  const hint = _('noRecoveryCodeHint');
  if (!hint || _adminAuthMode !== 'login') return;
  const open = hint.hidden;
  hint.hidden = !open;
  hint.textContent = open ? t('此安装尚未设置恢复码。其他用户请联系安全管理员；主管理员请在服务器上停止 RouteNest，并在终端运行 routenest auth recover。') : '';
  const link = _('forgotPasswordButton');
  if (link) link.setAttribute('aria-expanded', open ? 'true' : 'false');
  // The card grows downward; make sure the revealed text is on screen on short viewports.
  if (open && typeof hint.scrollIntoView === 'function') hint.scrollIntoView({ block: 'nearest' });
}

function openPasswordRecovery(push = true) {
  if (_adminPrincipal || _adminAuthMode !== 'login' || !(_adminAuthStatus && _adminAuthStatus.recoveryCodeAvailable)) return;
  if (_passwordRecoveryBusy || _passwordRecoveryCompleted) { syncAdminAuthRoute(); return; }
  _passwordRecoverySetup = false;
  _passwordRecoveryRestartRequired = false;
  _('passwordRecoveryFormView').hidden = false;
  _('passwordRecoveryResultView').hidden = true;
  _('passwordRecoveryForm').reset();
  _('passwordRecoveryError').textContent = '';
  if (_('loginPassword')) _('loginPassword').value = '';
  setAdminAuthRoute('recover', push);
  showAdminAuthPage('recover');
  setTimeout(() => _('passwordRecoveryCode').focus(), 40);
}

function closePasswordRecovery(push = true) {
  if (_passwordRecoveryBusy || _passwordRecoveryCompleted) { syncAdminAuthRoute(); return; }
  clearPasswordRecoveryInputs();
  setAdminAuthRoute('login', push);
  showAdminAuthPage('login');
  setTimeout(() => _('loginPassword').focus(), 40);
}

async function submitPasswordRecovery() {
  if (_passwordRecoveryBusy || _passwordRecoveryCompleted) return;
  const code = _('passwordRecoveryCode').value;
  const password = _('passwordRecoveryNewPassword').value;
  const confirmation = _('passwordRecoveryConfirmPassword').value;
  const error = _('passwordRecoveryError');
  if (!code) { error.textContent = t('请输入恢复码'); return; }
  if (adminPasswordTooShort(password)) { error.textContent = t('永久密码至少 8 位'); return; }
  if (password !== confirmation) { error.textContent = t('两次输入的密码不一致'); return; }
  const button = _('passwordRecoverySubmit');
  _passwordRecoveryBusy = true;
  button.disabled = true; error.textContent = '';
  if (_('passwordRecoveryBack')) _('passwordRecoveryBack').setAttribute('aria-disabled', 'true');
  try {
    const response = await adminFetch('POST', '/auth/recovery', { recoveryCode: code, newPassword: password, confirmPassword: confirmation });
    const envelope = await response.json().catch(() => ({}));
    const result = envelope.data || {};
    if ((!response.ok || envelope.success !== true) && !(envelope.error === 'security_restart_required' && result.recoveryCode)) throw new Error(envelope.error || t('恢复失败'));
    if (typeof result.recoveryCode !== 'string' || !result.recoveryCode) throw new Error(t('恢复失败'));
    _passwordRecoveryCompleted = true;
    _passwordRecoveryRestartRequired = envelope.error === 'security_restart_required' || result.restartRequired === true;
    _('passwordRecoveryCode').value = '';
    _('passwordRecoveryNewPassword').value = '';
    _('passwordRecoveryConfirmPassword').value = '';
    _('passwordRecoveryReplacementCode').textContent = result.recoveryCode || '';
    _('passwordRecoverySaved').checked = false;
    _('passwordRecoveryRestartNotice').hidden = !_passwordRecoveryRestartRequired;
    _passwordRecoverySetup = false;
    setRecoveryResultCopy(false, _passwordRecoveryRestartRequired);
    updatePasswordRecoveryFinish();
    _('passwordRecoveryFormView').hidden = true;
    _('passwordRecoveryResultView').hidden = false;
    setAdminAuthRoute('recover');
    showAdminAuthPage('recovery-result');
    setTimeout(() => _('passwordRecoveryReplacementCode').focus(), 40);
  } catch (reason) {
    error.textContent = reason.message === 'authentication_failed' ? t('恢复码无效、已使用或已被替换。') : (reason.message || t('恢复失败'));
  } finally {
    _passwordRecoveryBusy = false;
    button.disabled = false;
    if (_('passwordRecoveryBack')) _('passwordRecoveryBack').removeAttribute('aria-disabled');
  }
}

function updatePasswordRecoveryFinish() {
  _('passwordRecoveryFinish').disabled = !_('passwordRecoverySaved').checked;
}

function finishPasswordRecovery() {
  if (!_passwordRecoveryCompleted || !_('passwordRecoverySaved').checked) return;
  if (_passwordRecoverySetup) {
    // The code is on screen only until here; the session cookie set by setup (or, after a
    // restart, a fresh sign-in) takes over on reload.
    if (_('passwordRecoveryReplacementCode')) _('passwordRecoveryReplacementCode').textContent = '';
    _passwordRecoveryCompleted = false;
    _passwordRecoverySetup = false;
    location.reload();
    return;
  }
  if (_passwordRecoveryRestartRequired) {
    if (window.runtime && typeof window.runtime.Quit === 'function') window.runtime.Quit();
    else window.close();
    return;
  }
  _passwordRecoveryCompleted = false;
  closePasswordRecovery(false);
  setAdminAuthMode('login');
  showLogin('login');
  _('loginPassword').value = '';
  _('loginPassword').focus();
}

async function copyRecoveryValue(id, button) {
  const value = (_(id) && _(id).textContent) || '';
  if (!value) return;
  try { await navigator.clipboard.writeText(value); }
  catch (error) { fallbackCopy(value); }
  const previous = button.textContent;
  button.textContent = t('已复制');
  setTimeout(() => { button.textContent = previous; }, 1200);
}

function renderRecoveryCodeStatus() {
  const status = _('recoveryCodeStatus');
  if (!status) return;
  const available = !!(_adminAuthStatus && _adminAuthStatus.recoveryCodeAvailable);
  status.textContent = t(available ? '已启用' : '未生成');
  status.classList.toggle('is-success', available);
}

async function issueRecoveryCode() {
  const password = _('recoveryCodeCurrentPassword').value;
  const error = _('recoveryCodeIssueError');
  const button = _('issueRecoveryCodeButton');
  if (!password) { error.textContent = t('请输入当前密码'); return; }
  button.disabled = true; error.textContent = '';
  try {
    const response = await adminFetch('POST', '/auth/recovery-code', { currentPassword: password });
    const envelope = await response.json().catch(() => ({}));
    const result = envelope.data || {};
    if (!response.ok && !(envelope.error === 'security_restart_required' && result.recoveryCode)) throw new Error(envelope.error || t('生成恢复码失败'));
    _issuedRecoveryCodeRestartRequired = envelope.error === 'security_restart_required' || result.restartRequired === true;
    _('recoveryCodeCurrentPassword').value = '';
    _('issuedRecoveryCode').textContent = result.recoveryCode || '';
    _('issuedRecoveryCodeSaved').checked = false;
    _('issuedRecoveryCodeRestartNotice').hidden = !_issuedRecoveryCodeRestartRequired;
    _('issuedRecoveryCodeFinish').textContent = t(_issuedRecoveryCodeRestartRequired ? '关闭并重新打开 RouteNest' : '完成');
    updateIssuedRecoveryCodeFinish();
    _('issuedRecoveryCodePanel').hidden = false;
    if (_adminAuthStatus) _adminAuthStatus.recoveryCodeAvailable = true;
    renderRecoveryCodeStatus();
    _('issuedRecoveryCode').focus();
  } catch (reason) {
    error.textContent = reason.message === 'authentication_failed' ? t('当前密码不正确') : (reason.message || t('生成恢复码失败'));
  } finally { button.disabled = false; }
}

function updateIssuedRecoveryCodeFinish() {
  _('issuedRecoveryCodeFinish').disabled = !_('issuedRecoveryCodeSaved').checked;
}

function finishIssuedRecoveryCode() {
  if (!_('issuedRecoveryCodeSaved').checked) return;
  _('issuedRecoveryCode').textContent = '';
  if (_issuedRecoveryCodeRestartRequired) {
    if (window.runtime && typeof window.runtime.Quit === 'function') window.runtime.Quit();
    else window.close();
    return;
  }
  _issuedRecoveryCodeRestartRequired = false;
  _('issuedRecoveryCodePanel').hidden = true;
}

window.addEventListener('hashchange', syncAdminAuthRoute);
window.addEventListener('popstate', syncAdminAuthRoute);
window.addEventListener('beforeunload', event => {
  if (!_passwordRecoveryBusy && !_passwordRecoveryCompleted) return;
  event.preventDefault();
  event.returnValue = '';
});

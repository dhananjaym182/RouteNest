// Administrator recovery and desktop-session handoff. Recovery always requires
// the durable credential; localhost or an application window grants no authority.
let _passwordRecoveryCompleted = false;
let _passwordRecoveryRestartRequired = false;
let _issuedRecoveryCodeRestartRequired = false;

async function consumeDesktopSessionHandoff() {
  const prefix = '#desktop-handoff=';
  if (!location.hash.startsWith(prefix)) return;
  const token = decodeURIComponent(location.hash.slice(prefix.length));
  history.replaceState(null, '', location.pathname + location.search + '#dashboard');
  if (!token) return;
  const response = await fetch(API + '/auth/desktop-handoff', {
    method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token })
  });
  const envelope = await response.json().catch(() => ({}));
  if (!response.ok || !envelope.success) throw new Error(envelope.error || 'desktop_handoff_failed');
}

function openPasswordRecovery() {
  if (!(_adminAuthStatus && _adminAuthStatus.recoveryCodeAvailable)) return;
  _passwordRecoveryCompleted = false;
  _passwordRecoveryRestartRequired = false;
  _('passwordRecoveryFormView').hidden = false;
  _('passwordRecoveryResultView').hidden = true;
  _('passwordRecoveryForm').reset();
  _('passwordRecoveryError').textContent = '';
  _('passwordRecoveryOverlay').style.display = 'flex';
  setTimeout(() => _('passwordRecoveryCode').focus(), 40);
}

function closePasswordRecovery() {
  if (_passwordRecoveryCompleted && !_('passwordRecoverySaved').checked) return;
  _('passwordRecoveryOverlay').style.display = 'none';
  _('passwordRecoveryCode').value = '';
  _('passwordRecoveryNewPassword').value = '';
  _('passwordRecoveryConfirmPassword').value = '';
  _('passwordRecoveryReplacementCode').textContent = '';
}

async function submitPasswordRecovery() {
  const code = _('passwordRecoveryCode').value;
  const password = _('passwordRecoveryNewPassword').value;
  const confirmation = _('passwordRecoveryConfirmPassword').value;
  const error = _('passwordRecoveryError');
  if (!code) { error.textContent = t('请输入恢复码'); return; }
  if (adminPasswordTooShort(password)) { error.textContent = t('永久密码至少 8 位'); return; }
  if (password !== confirmation) { error.textContent = t('两次输入的密码不一致'); return; }
  const button = _('passwordRecoverySubmit');
  button.disabled = true; error.textContent = '';
  try {
    const response = await fetch(API + '/auth/recovery', {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ recoveryCode: code, newPassword: password, confirmPassword: confirmation })
    });
    const envelope = await response.json().catch(() => ({}));
    const result = envelope.data || {};
    if (!response.ok && !(envelope.error === 'security_restart_required' && result.recoveryCode)) throw new Error(envelope.error || t('恢复失败'));
    _passwordRecoveryCompleted = true;
    _passwordRecoveryRestartRequired = envelope.error === 'security_restart_required' || result.restartRequired === true;
    _('passwordRecoveryCode').value = '';
    _('passwordRecoveryNewPassword').value = '';
    _('passwordRecoveryConfirmPassword').value = '';
    _('passwordRecoveryReplacementCode').textContent = result.recoveryCode || '';
    _('passwordRecoverySaved').checked = false;
    _('passwordRecoveryRestartNotice').hidden = !_passwordRecoveryRestartRequired;
    _('passwordRecoveryFinish').textContent = t(_passwordRecoveryRestartRequired ? '关闭并重新打开 RouteNest' : '返回登录');
    updatePasswordRecoveryFinish();
    _('passwordRecoveryFormView').hidden = true;
    _('passwordRecoveryResultView').hidden = false;
    setTimeout(() => _('passwordRecoveryReplacementCode').focus(), 40);
  } catch (reason) {
    error.textContent = reason.message === 'authentication_failed' ? t('恢复码无效、已使用或已被替换。') : (reason.message || t('恢复失败'));
  } finally { button.disabled = false; }
}

function updatePasswordRecoveryFinish() {
  _('passwordRecoveryFinish').disabled = !_('passwordRecoverySaved').checked;
}

function finishPasswordRecovery() {
  if (!_passwordRecoveryCompleted || !_('passwordRecoverySaved').checked) return;
  if (_passwordRecoveryRestartRequired) {
    if (window.runtime && typeof window.runtime.Quit === 'function') window.runtime.Quit();
    else window.close();
    return;
  }
  _passwordRecoveryCompleted = false;
  closePasswordRecovery();
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

document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && _('passwordRecoveryOverlay') && _('passwordRecoveryOverlay').style.display === 'flex' && !_passwordRecoveryCompleted) {
    closePasswordRecovery();
  }
});

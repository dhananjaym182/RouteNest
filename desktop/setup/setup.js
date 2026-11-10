const setupCopy = {
  en: {
    platformLabel: 'Desktop access',
    pageTitle: 'RouteNest · Desktop access',
    checkingTitle: 'Checking this installation',
    checkingBody: 'RouteNest is verifying local security storage before opening.',
    stepOne: 'Step 1 of 3',
    welcomeTitle: 'Welcome to RouteNest',
    welcomeBody: 'Create the local administrator that protects provider credentials, routing, and gateway access.',
    trustLocal: 'Your password is hashed and stored only on this device.',
    trustRecovery: 'A recovery code will be shown once after setup.',
    trustNoTerminal: 'No terminal commands or bootstrap secrets are required.',
    begin: 'Create administrator',
    stepTwo: 'Step 2 of 3',
    passwordTitle: 'Create administrator password',
    passwordBody: 'Use at least 8 characters. A password manager is recommended.',
    usernameLabel: 'Username',
    passwordLabel: 'Password',
    confirmLabel: 'Confirm password',
    newPasswordLabel: 'New password',
    trustDevice: 'Trust this device for automatic sign-in',
    trustDeviceHelp: 'Optional. RouteNest stores a revocable credential in protected OS storage, never your password. Only enable this on a device you control.',
    trustStored: 'This device is trusted. Manage automatic sign-in in Administrator Security.',
    trustEnabled: 'Automatic sign-in is enabled for this device.',
    trustUnavailable: 'Protected OS credential storage is unavailable. Use your password; device trust stays off until the store is available.',
    trustCorrupt: 'The local desktop credential is corrupted. Use your password, then explicitly forget this device in Administrator Security before trusting it again.',
    trustMissing: 'No desktop credential is saved. Use your password, or explicitly choose to trust this device.',
    trustRevoked: 'This desktop credential was revoked. Use your password. You can explicitly trust this device again if you control it.',
    trustExpired: 'This desktop credential has expired. Use your password. You can explicitly trust this device again if you control it.',
    trustPaused: 'Automatic sign-in is paused. Sign in with your password; resume it in Administrator Security when you choose.',
    trustUnknown: 'Device trust could not be verified. Use your password and check Administrator Security.',
    trustNotRequested: 'Automatic sign-in was not requested. Use your password next time.',
    trustFailed: 'Your administrator account is ready, but this device was not trusted. Use your password and review Administrator Security.',
    localAccess: 'Local administrator',
    loginTitle: 'Sign in to RouteNest',
    loginBody: 'Use your local administrator account. Ordinary browsers authenticate separately.',
    signIn: 'Sign in',
    forgotPassword: 'Forgot password?',
    accountRecovery: 'Account recovery',
    forgotTitle: 'Reset administrator password',
    forgotBody: "Enter the recovery code saved for the local emergency administrator. Resetting the password revokes that account's sessions and trusted desktop devices.",
    recoveryCodeLabel: 'Recovery code',
    resetPassword: 'Reset password',
    backToSignIn: 'Back to sign in',
    recoveryGuidance: 'Lost the recovery code? Stop RouteNest and use the documented offline emergency recovery procedure from a local terminal. Other local users should ask a security administrator to reset their password.',
    back: 'Back',
    create: 'Create administrator',
    stepThree: 'Step 3 of 3',
    recoveryTitle: 'Save your recovery code',
    recoveryBody: 'This single-use code resets a forgotten administrator password. RouteNest cannot display it again.',
    replacementTitle: 'Save your replacement recovery code',
    replacementBody: 'Your password was reset. The old recovery code no longer works. Save this replacement before returning to sign in; it cannot be displayed again.',
    passwordReset: 'Password reset. Sign in with your new password. Automatic sign-in has not been resumed.',
    restartNotice: 'The change was saved, but RouteNest must restart to finish it. Save this recovery code before closing and reopening RouteNest.',
    restartTitle: 'Restart RouteNest',
    restartBody: 'Close and reopen RouteNest to finish the security update. Use your password to sign in when it opens.',
    copy: 'Copy',
    copied: 'Recovery code copied.',
    copyFailed: 'Clipboard access failed. Select the recovery code and copy it manually.',
    show: 'Show',
    hide: 'Hide',
    showValue: 'Show',
    hideValue: 'Hide',
    languageSwitch: 'Switch to Chinese',
    recoveryWarning: 'Store it in a password manager or another secure location. Do not save it beside provider credentials.',
    savedConfirm: 'I saved the recovery code securely.',
    finish: 'Open RouteNest',
    restartFinish: 'Close RouteNest',
    errorTitle: 'Security storage needs attention',
    errorBody: 'RouteNest cannot safely open this installation.',
    errorHelp: 'Restore the existing data directory from backup, or use the documented offline recovery procedure when administrator authority is intact. Existing credentials will not be silently reset.',
    shortPassword: 'Use at least 8 characters, including a non-space character.',
    longPassword: 'Use a password of at most 1024 UTF-8 bytes.',
    passwordRequired: 'Enter your password.',
    recoveryRequired: 'Enter your recovery code.',
    mismatch: 'The passwords do not match.',
    setupFailed: 'Setup could not be completed. Please try again.',
    signInFailed: 'Sign-in failed. Check your username and password.',
    recoveryFailed: 'Password recovery could not be completed. Please try again.',
    recoveryInvalid: 'The recovery code is invalid, already used, or replaced.',
    saveRequired: 'Confirm that you saved the recovery code.',
    bridgeUnavailable: 'The native desktop connection is unavailable. Close and reopen RouteNest; do not reset the data directory.',
    invalidResponse: 'RouteNest returned an incomplete response. Close and reopen the app, then sign in with your password.',
    invalidAdminURL: 'RouteNest refused to leave the embedded administrator page. Close and reopen the app.',
    serviceUnavailable: 'The security service is unavailable. Keep the existing data directory and try again after repairing or restarting RouteNest.',
    rateLimited: 'Too many attempts. Wait a moment before trying again.',
    sessionExpired: 'Your session ended. Sign in again with your password.',
    setupUnavailable: 'Setup has already completed or is no longer available. Reopen RouteNest and sign in with your password.',
    signInAgain: 'The sign-in handoff is no longer available. Sign in with your password.',
    automaticFailed: 'Automatic sign-in could not complete. You can still sign in with your password.',
    setupSaved: 'Administrator setup is saved. Sign in with the password you just created.',
    legacyTitle: 'Upgrade your administrator password',
    legacyBody: 'This installation uses an older password format. Continue to the existing administrator sign-in to verify it and choose a new password.',
    offlineTitle: 'Complete offline recovery',
    offlineBody: 'Offline recovery is pending. Continue to the administrator recovery screen and enter the one-time secret from your local terminal.',
    continue: 'Continue in RouteNest'
  },
  zh: {
    platformLabel: '桌面访问',
    pageTitle: 'RouteNest · 桌面访问',
    checkingTitle: '正在检查此安装',
    checkingBody: 'RouteNest 正在验证本机安全存储，然后再打开应用。',
    stepOne: '第 1 步，共 3 步',
    welcomeTitle: '欢迎使用 RouteNest',
    welcomeBody: '创建本机管理员，用于保护 Provider 凭据、路由和网关访问。',
    trustLocal: '密码经过哈希处理，仅保存在此设备。',
    trustRecovery: '设置完成后只显示一次恢复码。',
    trustNoTerminal: '无需终端命令或复制引导密钥。',
    begin: '创建管理员',
    stepTwo: '第 2 步，共 3 步',
    passwordTitle: '创建管理员密码',
    passwordBody: '至少使用 8 个字符，建议由密码管理器生成。',
    usernameLabel: '用户名',
    passwordLabel: '密码',
    confirmLabel: '确认密码',
    newPasswordLabel: '新密码',
    trustDevice: '信任此设备并自动登录',
    trustDeviceHelp: '可选。RouteNest 仅在操作系统的受保护存储中保存可撤销的设备凭据，绝不保存密码。请仅在自己掌控的设备上启用。',
    trustStored: '此设备已受信任。可在「管理员安全」中管理自动登录。',
    trustEnabled: '此设备已启用自动登录。',
    trustUnavailable: '操作系统的受保护凭据存储不可用。请使用密码登录；存储恢复前不会启用设备信任。',
    trustCorrupt: '本机桌面凭据已损坏。请使用密码登录，然后在「管理员安全」中明确选择忘记此设备，再重新信任。',
    trustMissing: '尚未保存桌面凭据。请使用密码登录，或明确选择信任此设备。',
    trustRevoked: '此桌面凭据已被撤销。请使用密码登录；若仍掌控此设备，可明确选择重新信任。',
    trustExpired: '此桌面凭据已过期。请使用密码登录；若仍掌控此设备，可明确选择重新信任。',
    trustPaused: '自动登录已暂停。请使用密码登录，并按需在「管理员安全」中恢复自动登录。',
    trustUnknown: '无法验证设备信任状态。请使用密码登录并检查「管理员安全」。',
    trustNotRequested: '未请求自动登录。下次请使用密码登录。',
    trustFailed: '管理员账号已就绪，但未能信任此设备。请使用密码登录并检查「管理员安全」。',
    localAccess: '本地管理员',
    loginTitle: '登录 RouteNest',
    loginBody: '使用本地管理员账号登录。普通浏览器需要独立认证。',
    signIn: '登录',
    forgotPassword: '忘记密码？',
    accountRecovery: '账号恢复',
    forgotTitle: '重置管理员密码',
    forgotBody: '输入为本地应急管理员保存的恢复码。重置密码会撤销该账号的会话和受信任桌面设备。',
    recoveryCodeLabel: '恢复码',
    resetPassword: '重置密码',
    backToSignIn: '返回登录',
    recoveryGuidance: '恢复码遗失？请停止 RouteNest，并从本机终端执行文档中的离线应急恢复流程。其他本地用户应联系安全管理员重置密码。',
    back: '返回',
    create: '创建管理员',
    stepThree: '第 3 步，共 3 步',
    recoveryTitle: '保存恢复码',
    recoveryBody: '忘记管理员密码时，使用此一次性恢复码重置密码。RouteNest 无法再次显示它。',
    replacementTitle: '保存新的恢复码',
    replacementBody: '密码已重置，旧恢复码已失效。请保存此新恢复码后再返回登录；它无法再次显示。',
    passwordReset: '密码已重置，请使用新密码登录。自动登录尚未恢复。',
    restartNotice: '更改已保存，但必须重启 RouteNest 才能完成。请先保存此恢复码，再关闭并重新打开 RouteNest。',
    restartTitle: '重启 RouteNest',
    restartBody: '关闭并重新打开 RouteNest 以完成安全更新。重新打开后请使用密码登录。',
    copy: '复制',
    copied: '恢复码已复制。',
    copyFailed: '无法访问剪贴板，请选中恢复码后手动复制。',
    show: '显示',
    hide: '隐藏',
    showValue: '显示',
    hideValue: '隐藏',
    languageSwitch: '切换为英语',
    recoveryWarning: '请保存到密码管理器或其他安全位置，不要与 Provider 凭据放在一起。',
    savedConfirm: '我已安全保存恢复码。',
    finish: '打开 RouteNest',
    restartFinish: '关闭 RouteNest',
    errorTitle: '安全存储需要处理',
    errorBody: 'RouteNest 无法安全打开此安装。',
    errorHelp: '请从备份恢复现有数据目录；若管理员权限文件完整，也可使用文档中的离线恢复流程。不会静默重置现有凭据。',
    shortPassword: '密码至少需要 8 个字符，且不能全部为空白字符。',
    longPassword: '密码的 UTF-8 长度不能超过 1024 字节。',
    passwordRequired: '请输入密码。',
    recoveryRequired: '请输入恢复码。',
    mismatch: '两次输入的密码不一致。',
    setupFailed: '无法完成设置，请重试。',
    signInFailed: '登录失败，请检查用户名和密码。',
    recoveryFailed: '无法完成密码恢复，请重试。',
    recoveryInvalid: '恢复码无效、已使用或已被替换。',
    saveRequired: '请确认已保存恢复码。',
    bridgeUnavailable: '本机桌面连接不可用。请关闭并重新打开 RouteNest，不要重置数据目录。',
    invalidResponse: 'RouteNest 返回的信息不完整。请关闭并重新打开应用，然后使用密码登录。',
    invalidAdminURL: 'RouteNest 已拒绝离开内嵌管理员页面。请关闭并重新打开应用。',
    serviceUnavailable: '安全服务不可用。请保留现有数据目录，修复或重启 RouteNest 后重试。',
    rateLimited: '尝试次数过多，请稍后重试。',
    sessionExpired: '会话已结束，请使用密码重新登录。',
    setupUnavailable: '设置已完成或已不可用。请重新打开 RouteNest 并使用密码登录。',
    signInAgain: '登录交接已不可用，请使用密码登录。',
    automaticFailed: '自动登录未能完成，仍可使用密码登录。',
    setupSaved: '管理员设置已保存，请使用刚创建的密码登录。',
    legacyTitle: '升级管理员密码',
    legacyBody: '此安装使用旧密码格式。请继续前往现有管理员登录流程，验证旧密码并设置新密码。',
    offlineTitle: '完成离线恢复',
    offlineBody: '离线恢复尚未完成。请继续前往管理员恢复界面，输入本地终端提供的一次性密钥。',
    continue: '在 RouteNest 中继续'
  }
};

let setupLang = (navigator.language || 'en').toLowerCase().startsWith('zh') ? 'zh' : 'en';
try {
  const savedLanguage = localStorage.getItem('cline_admin_lang');
  if (savedLanguage === 'en' || savedLanguage === 'zh') setupLang = savedLanguage;
} catch {}

let setupState = {};
let setupResult = null;
let setupStateLoaded = false;
let automaticAttempted = false;
let setupBusy = false;
let setupFinishAttempted = false;
const setupMessages = new Map();
const byId = id => document.getElementById(id);
const copyText = key => setupCopy[setupLang][key] || setupCopy.en[key] || key;
const setupViews = ['loadingView', 'welcomeView', 'loginView', 'passwordView', 'forgotView', 'recoveryView', 'continuationView', 'restartView', 'errorView'];

function setupErrorParts(reason, depth = 0) {
  if (depth > 5 || reason == null) return { code: '', message: '' };
  if (typeof reason === 'string') {
    const text = reason.trim();
    try {
      const parsed = JSON.parse(text);
      if (parsed !== text) return setupErrorParts(parsed, depth + 1);
    } catch {}
    return { code: text, message: text };
  }
  if (typeof reason !== 'object') return { code: '', message: '' };
  const nested = setupErrorParts(reason.error || reason.reason || reason.message, depth + 1);
  return { code: typeof reason.code === 'string' ? reason.code : nested.code, message: nested.message };
}

function setupErrorText(reason, fallback = 'setupFailed', recovery = false) {
  const detail = setupErrorParts(reason);
  const codes = {
    authentication_failed: recovery ? 'recoveryInvalid' : 'signInFailed',
    unauthorized: recovery ? 'recoveryInvalid' : 'signInFailed',
    invalid_credentials: 'signInFailed',
    invalid_request: 'invalidResponse',
    permission_denied: 'signInFailed',
    password_rotation_required: 'legacyBody',
    automatic_sign_in_unavailable: 'automaticFailed',
    invalid_recovery_code: 'recoveryInvalid',
    recovery_code_invalid: 'recoveryInvalid',
    password_too_short: 'shortPassword',
    invalid_password: 'shortPassword',
    password_too_long: 'longPassword',
    password_mismatch: 'mismatch',
    confirmation_mismatch: 'mismatch',
    password_confirmation_mismatch: 'mismatch',
    credential_storage_unavailable: 'trustUnavailable',
    storage_unavailable: 'trustUnavailable',
    desktop_credential_unavailable: 'trustUnavailable',
    desktop_storage_unavailable: 'trustUnavailable',
    credential_storage_corrupt: 'trustCorrupt',
    storage_corrupt: 'trustCorrupt',
    desktop_credential_corrupt: 'trustCorrupt',
    desktop_storage_corrupt: 'trustCorrupt',
    desktop_credential_missing: 'trustMissing',
    desktop_device_revoked: 'trustRevoked',
    trusted_device_revoked: 'trustRevoked',
    desktop_device_untrusted: 'trustRevoked',
    trusted_device_untrusted: 'trustRevoked',
    desktop_device_expired: 'trustExpired',
    trusted_device_expired: 'trustExpired',
    automatic_sign_in_paused: 'trustPaused',
    automatic_sign_in_disabled: 'trustPaused',
    desktop_auto_sign_in_disabled: 'trustPaused',
    security_restart_required: 'restartNotice',
    security_unavailable: 'serviceUnavailable',
    session_expired: 'sessionExpired',
    session_required: 'sessionExpired',
    desktop_session_required: 'sessionExpired',
    setup_unavailable: 'setupUnavailable',
    setup_already_completed: 'setupUnavailable',
    desktop_handoff_failed: 'signInAgain',
    desktop_handoff_unavailable: 'signInAgain',
    handoff_unavailable: 'signInAgain',
    rate_limited: 'rateLimited',
    too_many_requests: 'rateLimited',
    unavailable: 'trustUnavailable',
    corrupt: 'trustCorrupt',
    missing: 'trustMissing',
    revoked: 'trustRevoked',
    expired: 'trustExpired',
    paused: 'trustPaused',
    bridge_unavailable: 'bridgeUnavailable',
    invalid_response: 'invalidResponse',
    invalid_admin_url: 'invalidAdminURL',
    'password confirmation does not match': 'mismatch',
    'desktop setup is unavailable': 'setupUnavailable'
  };
  const key = codes[detail.code] || codes[detail.message];
  return key ? copyText(key) : detail.message || copyText(fallback);
}

function setSetupMessage(id, key, reason, recovery = false) {
  const node = byId(id);
  if (!node) return;
  if (!key && reason === undefined) {
    setupMessages.delete(id);
    node.textContent = '';
    return;
  }
  setupMessages.set(id, { key, reason, recovery });
  node.textContent = reason === undefined ? copyText(key) : setupErrorText(reason, key, recovery);
}

function setupFieldError(id, key, inputID, reason, recovery = false) {
  setSetupMessage(id, key, reason, recovery);
  const input = byId(inputID);
  if (input) {
    input.setAttribute('aria-invalid', 'true');
    input.focus();
  } else byId(id).focus();
}

function applyCopy() {
  document.documentElement.lang = setupLang === 'zh' ? 'zh-CN' : 'en';
  document.title = copyText('pageTitle');
  document.querySelectorAll('[data-copy]').forEach(node => { node.textContent = copyText(node.dataset.copy); });
  document.querySelectorAll('[data-copy-aria]').forEach(node => { node.setAttribute('aria-label', copyText(node.dataset.copyAria)); });
  document.querySelectorAll('.visibility-button').forEach(button => {
    const shown = byId(button.dataset.target).type === 'text';
    button.textContent = copyText(shown ? 'hide' : 'show');
    button.setAttribute('aria-label', copyText(shown ? 'hideValue' : 'showValue') + (setupLang === 'en' ? ' ' : '') + copyText(button.dataset.label));
    button.setAttribute('aria-pressed', String(shown));
  });
  byId('languageButton').textContent = setupLang === 'zh' ? 'English' : '中文';
  byId('languageButton').setAttribute('aria-label', copyText('languageSwitch'));
  for (const [id, message] of setupMessages) setSetupMessage(id, message.key, message.reason, message.recovery);
}

function showView(id, focusID) {
  setupViews.forEach(name => { byId(name).hidden = name !== id; });
  const focus = byId(focusID) || byId(id).querySelector('h1');
  if (focus) focus.focus();
}

function setupBridge() {
  return window.go && window.go.main && window.go.main.DesktopSetupBridge;
}

async function waitForSetupBridge() {
  for (let attempt = 0; attempt < 100; attempt++) {
    const bridge = setupBridge();
    if (bridge && typeof bridge.State === 'function') return bridge;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw { code: 'bridge_unavailable' };
}

async function setupBridgeCall(method, ...args) {
  const bridge = setupBridge();
  if (!bridge || typeof bridge[method] !== 'function') throw { code: 'bridge_unavailable' };
  const response = await bridge[method](...args);
  if (response && (response.success === false || response.ok === false || response.error)) {
    const data = response.data || response;
    if ((method === 'Complete' || method === 'RecoverPassword') && data.recoveryCode && setupErrorParts(response).code === 'security_restart_required') {
      return { ...data, restartRequired: true };
    }
    throw response;
  }
  return response && response.data && typeof response.data === 'object' ? response.data : response;
}

function setupStatusName(value) {
  const name = typeof value === 'string' ? value : value && (value.state || value.status || value.code);
  const aliases = { disabled: 'paused', active: 'trusted', untrusted: 'revoked', not_found: 'missing' };
  return aliases[name] || name || '';
}

function setupDeviceState() {
  const storage = setupState.credentialStorage || {};
  const local = setupStatusName(storage);
  const device = setupStatusName(setupState.deviceStatus);
  const available = storage.available === true && local !== 'unavailable';
  const stored = storage.stored === true;
  let status = device || local || 'unknown';
  if (local === 'corrupt' || local === 'unavailable') status = local;
  else if (!available) status = 'unavailable';
  else if (!['revoked', 'expired', 'corrupt', 'missing'].includes(device) && stored) {
    status = storage.automaticSignIn === false || device === 'paused' || local === 'paused' ? 'paused' : 'trusted';
  }
  const trusted = stored && (status === 'trusted' || status === 'paused');
  return { status, trusted, canTrust: available && local !== 'corrupt' && status !== 'corrupt' && !trusted };
}

function renderSetupDeviceState() {
  const device = setupDeviceState();
  const labels = { stored: 'trustStored', trusted: 'trustStored', paused: 'trustPaused', missing: 'trustMissing', corrupt: 'trustCorrupt', unavailable: 'trustUnavailable', revoked: 'trustRevoked', expired: 'trustExpired' };
  const key = labels[device.status] || 'trustUnknown';
  for (const id of ['desktopDeviceNotice', 'setupDeviceNotice']) {
    setSetupMessage(id, key);
    byId(id).hidden = false;
  }
  for (const [optionID, inputID] of [['desktopTrustOption', 'desktopTrustDeviceCheck'], ['setupTrustOption', 'trustDeviceCheck']]) {
    byId(optionID).hidden = device.trusted;
    byId(inputID).disabled = !device.canTrust;
    if (!device.canTrust) byId(inputID).checked = false;
  }
}

function applySetupStatus(result) {
  if (!result || typeof result !== 'object') return;
  if (result.credentialStorage) setupState.credentialStorage = result.credentialStorage;
  if (result.deviceStatus) setupState.deviceStatus = result.deviceStatus;
  else {
    const status = setupStatusName(result.status);
    if (['trusted', 'paused', 'missing', 'corrupt', 'unavailable', 'revoked', 'expired'].includes(status)) setupState.deviceStatus = status;
  }
  renderSetupDeviceState();
}

function updateSetupStatusFromError(reason) {
  const detail = setupErrorParts(reason);
  const statuses = {
    credential_storage_unavailable: 'unavailable', storage_unavailable: 'unavailable', desktop_credential_unavailable: 'unavailable', desktop_storage_unavailable: 'unavailable',
    credential_storage_corrupt: 'corrupt', storage_corrupt: 'corrupt', desktop_credential_corrupt: 'corrupt', desktop_storage_corrupt: 'corrupt',
    desktop_credential_missing: 'missing', desktop_device_revoked: 'revoked', trusted_device_revoked: 'revoked', desktop_device_expired: 'expired', trusted_device_expired: 'expired',
    automatic_sign_in_paused: 'paused', automatic_sign_in_disabled: 'paused', desktop_auto_sign_in_disabled: 'paused'
  };
  const status = statuses[detail.code] || statuses[detail.message];
  if (!status) return;
  setupState.deviceStatus = status;
  if (['unavailable', 'corrupt', 'missing'].includes(status)) setupState.credentialStorage = { ...(setupState.credentialStorage || {}), available: status !== 'unavailable', state: status, stored: false, automaticSignIn: false };
  renderSetupDeviceState();
}

function requestedDeviceTrust(id) {
  return setupDeviceState().canTrust && !byId(id).disabled && byId(id).checked;
}

function clearSetupSecrets() {
  for (const id of ['passwordInput', 'confirmInput', 'desktopPasswordInput', 'recoveryCodeInput', 'recoveryPasswordInput', 'recoveryConfirmInput']) {
    byId(id).value = '';
    byId(id).type = 'password';
  }
  byId('recoveryCode').textContent = '';
  setSetupMessage('copyStatus');
  setupResult = null;
}

function navigateDesktopAdmin(adminURL = '/admin/', token) {
  if (adminURL !== '/admin/') throw { code: 'invalid_admin_url' };
  if (token !== undefined && (typeof token !== 'string' || !token)) throw { code: 'desktop_handoff_unavailable' };
  clearSetupSecrets();
  location.replace('/admin/' + (token ? '#desktop-handoff=' + encodeURIComponent(token) : ''));
}

function finishDesktopHandoff(result) {
  if (!result || typeof result.handoffToken !== 'string' || !result.handoffToken) throw { code: 'desktop_handoff_unavailable' };
  const token = result.handoffToken;
  result.handoffToken = '';
  navigateDesktopAdmin(result.adminURL, token);
}

async function attemptInitialAutomaticSignIn() {
  if (automaticAttempted || !['setup', 'login'].includes(setupState.mode) || !['/', '/index.html'].includes(location.pathname)) return false;
  automaticAttempted = true;
  try {
    const result = await setupBridgeCall('AutomaticSignIn');
    applySetupStatus(result);
    if (result && result.handoffToken) {
      finishDesktopHandoff(result);
      return true;
    }
  } catch (reason) {
    updateSetupStatusFromError(reason);
    if (setupState.mode === 'login') setSetupMessage('desktopLoginError', 'automaticFailed', reason);
  }
  return false;
}

async function loadState() {
  if (setupStateLoaded) return;
  setupStateLoaded = true;
  try {
    await waitForSetupBridge();
    setupState = await setupBridgeCall('State') || {};
    renderSetupDeviceState();
    if (setupState.mode === 'ready') {
      navigateDesktopAdmin(setupState.adminURL);
      return;
    }
    if (setupState.mode === 'setup' || setupState.mode === 'login') {
      if (await attemptInitialAutomaticSignIn()) return;
      showView(setupState.mode === 'setup' ? 'welcomeView' : 'loginView', setupState.mode === 'login' ? 'desktopPasswordInput' : undefined);
      return;
    }
    if (setupState.mode === 'legacy' || setupState.mode === 'recovery') {
      setSetupMessage('continuationTitle', setupState.mode === 'legacy' ? 'legacyTitle' : 'offlineTitle');
      setSetupMessage('continuationBody', setupState.mode === 'legacy' ? 'legacyBody' : 'offlineBody');
      showView('continuationView');
      return;
    }
    if (setupState.mode === 'restart') {
      showView('restartView');
      return;
    }
    setSetupMessage('errorDetails', 'serviceUnavailable', setupState.error || setupState.message || { code: 'security_unavailable' });
    showView('errorView');
  } catch (reason) {
    setSetupMessage('errorDetails', 'setupFailed', reason);
    showView('errorView');
  }
}

function setSetupBusy(formID, busy) {
  setupBusy = busy;
  const form = byId(formID);
  form.setAttribute('aria-busy', String(busy));
  form.querySelector('fieldset').disabled = busy;
}

function validateNewPassword(password, confirmation, errorID, passwordID, confirmationID) {
  if (Array.from(password).length < 8 || !password.trim()) {
    setupFieldError(errorID, 'shortPassword', passwordID);
    return false;
  }
  if (new TextEncoder().encode(password).length > 1024) {
    setupFieldError(errorID, 'longPassword', passwordID);
    return false;
  }
  if (password !== confirmation) {
    setupFieldError(errorID, 'mismatch', confirmationID);
    return false;
  }
  return true;
}

function presentRecoveryCode(result, kind) {
  if (!result || typeof result.recoveryCode !== 'string' || !result.recoveryCode) throw { code: 'invalid_response' };
  const trust = setupStatusName(result.trustStatus || result.deviceTrustStatus);
  if (kind === 'setup') {
    applySetupStatus(result);
    if (trust === 'trusted') {
      setupState.deviceStatus = 'trusted';
      if (!result.credentialStorage) setupState.credentialStorage = { available: true, stored: true, state: 'stored', automaticSignIn: true };
    } else if (trust === 'corrupt' || trust === 'unavailable') updateSetupStatusFromError({ code: 'desktop_storage_' + trust });
    renderSetupDeviceState();
  }
  setupState.mode = 'login';
  setupResult = { kind, restartRequired: result.restartRequired === true };
  setupFinishAttempted = false;
  byId('recoveryCode').textContent = result.recoveryCode;
  result.recoveryCode = '';
  result.handoffToken = '';
  byId('savedCheck').checked = false;
  byId('savedCheck').disabled = false;
  byId('copyButton').disabled = false;
  byId('finishButton').disabled = true;
  byId('restartNotice').hidden = !setupResult.restartRequired;
  byId('recoveryStep').dataset.copy = kind === 'setup' ? 'stepThree' : 'accountRecovery';
  byId('recoveryTitle').dataset.copy = kind === 'setup' ? 'recoveryTitle' : 'replacementTitle';
  byId('recoveryBody').dataset.copy = kind === 'setup' ? 'recoveryBody' : 'replacementBody';
  byId('finishButton').dataset.copy = setupResult.restartRequired ? 'restartFinish' : kind === 'setup' ? 'finish' : 'backToSignIn';
  setSetupMessage('recoveryError');
  setSetupMessage('copyStatus');
  const trustKey = { trusted: 'trustEnabled', stored: 'trustEnabled', unavailable: 'trustUnavailable', corrupt: 'trustCorrupt', not_requested: 'trustNotRequested', paused: 'trustPaused' }[trust] || 'trustFailed';
  setSetupMessage('trustDeviceResult', trustKey);
  byId('trustDeviceResult').hidden = kind !== 'setup';
  applyCopy();
  showView('recoveryView');
}

async function submitDesktopSignIn(event) {
  event.preventDefault();
  if (setupBusy || setupResult || setupState.mode === 'setup') return;
  setSetupMessage('desktopLoginError');
  const username = byId('desktopUsernameInput').value.trim() || 'admin';
  const password = byId('desktopPasswordInput').value;
  if (!password) {
    setupFieldError('desktopLoginError', 'passwordRequired', 'desktopPasswordInput');
    return;
  }
  const trustDevice = requestedDeviceTrust('desktopTrustDeviceCheck');
  setSetupBusy('desktopLoginForm', true);
  try {
    const result = await setupBridgeCall('SignIn', { username, password, trustDevice });
    byId('desktopPasswordInput').value = '';
    applySetupStatus(result);
    finishDesktopHandoff(result);
  } catch (reason) {
    updateSetupStatusFromError(reason);
    setSetupMessage('desktopLoginError', 'signInFailed', reason);
    if (setupErrorParts(reason).code === 'password_rotation_required') {
      setupState.mode = 'legacy';
      setSetupMessage('continuationTitle', 'legacyTitle');
      setSetupMessage('continuationBody', 'legacyBody');
      showView('continuationView');
    }
    byId('desktopPasswordInput').value = '';
    byId('desktopPasswordInput').setAttribute('aria-invalid', 'true');
  } finally {
    setSetupBusy('desktopLoginForm', false);
    if (!byId('loginView').hidden) byId('desktopPasswordInput').focus();
  }
}

async function submitDesktopSetup(event) {
  event.preventDefault();
  if (setupBusy || setupState.mode !== 'setup') return;
  setSetupMessage('setupError');
  const password = byId('passwordInput').value;
  const confirmation = byId('confirmInput').value;
  if (!validateNewPassword(password, confirmation, 'setupError', 'passwordInput', 'confirmInput')) return;
  const trustDevice = requestedDeviceTrust('trustDeviceCheck');
  setSetupBusy('passwordForm', true);
  try {
    const result = await setupBridgeCall('Complete', password, confirmation, trustDevice);
    byId('passwordInput').value = '';
    byId('confirmInput').value = '';
    presentRecoveryCode(result, 'setup');
  } catch (reason) {
    updateSetupStatusFromError(reason);
    setSetupMessage('setupError', 'setupFailed', reason);
  } finally {
    setSetupBusy('passwordForm', false);
    if (!byId('passwordView').hidden) byId('setupError').focus();
  }
}

function openDesktopRecovery() {
  if (setupBusy || setupResult) return;
  byId('desktopPasswordInput').value = '';
  byId('desktopTrustDeviceCheck').checked = false;
  byId('desktopRecoveryForm').reset();
  setSetupMessage('desktopRecoveryError');
  showView('forgotView', 'recoveryCodeInput');
}

function returnToDesktopSignIn() {
  if (setupBusy || setupResult) return;
  clearSetupSecrets();
  byId('desktopTrustDeviceCheck').checked = false;
  applyCopy();
  showView('loginView', 'desktopPasswordInput');
}

async function submitDesktopRecovery(event) {
  event.preventDefault();
  if (setupBusy || setupResult) return;
  setSetupMessage('desktopRecoveryError');
  const recoveryCode = byId('recoveryCodeInput').value.trim();
  const newPassword = byId('recoveryPasswordInput').value;
  const confirmation = byId('recoveryConfirmInput').value;
  if (!recoveryCode) {
    setupFieldError('desktopRecoveryError', 'recoveryRequired', 'recoveryCodeInput');
    return;
  }
  if (!validateNewPassword(newPassword, confirmation, 'desktopRecoveryError', 'recoveryPasswordInput', 'recoveryConfirmInput')) return;
  setSetupBusy('desktopRecoveryForm', true);
  try {
    const result = await setupBridgeCall('RecoverPassword', { recoveryCode, newPassword, confirmation });
    clearSetupSecrets();
    if (setupState.credentialStorage) setupState.credentialStorage.automaticSignIn = false;
    setupState.deviceStatus = 'revoked';
    renderSetupDeviceState();
    presentRecoveryCode(result, 'recovery');
  } catch (reason) {
    setSetupMessage('desktopRecoveryError', 'recoveryFailed', reason, true);
    byId('recoveryCodeInput').setAttribute('aria-invalid', 'true');
  } finally {
    setSetupBusy('desktopRecoveryForm', false);
    if (!byId('forgotView').hidden) byId('desktopRecoveryError').focus();
  }
}

function closeDesktopSetup() {
  clearSetupSecrets();
  showView('restartView');
  if (window.runtime && typeof window.runtime.Quit === 'function') window.runtime.Quit();
  else window.close();
}

async function finishSetupAfterSave() {
  if (setupBusy || setupFinishAttempted) return;
  if (!setupResult || !byId('savedCheck').checked) {
    setupFieldError('recoveryError', 'saveRequired', 'savedCheck');
    return;
  }
  setupFinishAttempted = true;
  byId('finishButton').disabled = true;
  byId('savedCheck').disabled = true;
  byId('copyButton').disabled = true;
  const result = setupResult;
  clearSetupSecrets();
  if (result.restartRequired) {
    closeDesktopSetup();
    return;
  }
  if (result.kind === 'recovery') {
    byId('desktopUsernameInput').value = 'admin';
    setSetupMessage('desktopLoginError');
    setSetupMessage('desktopLoginNotice', 'passwordReset');
    byId('desktopLoginNotice').hidden = false;
    returnToDesktopSignIn();
    return;
  }
  setupBusy = true;
  byId('recoveryView').setAttribute('aria-busy', 'true');
  try {
    const handoff = await setupBridgeCall('FinishSetup');
    if (handoff && handoff.restartRequired) {
      closeDesktopSetup();
      return;
    }
    finishDesktopHandoff(handoff);
  } catch (reason) {
    setSetupMessage('desktopLoginNotice', 'setupSaved');
    byId('desktopLoginNotice').hidden = false;
    setSetupMessage('desktopLoginError', 'signInAgain', reason);
    showView('loginView', 'desktopPasswordInput');
  } finally {
    setupBusy = false;
    byId('recoveryView').setAttribute('aria-busy', 'false');
  }
}

async function copySetupRecoveryCode() {
  const code = byId('recoveryCode').textContent;
  if (!code) return;
  let copied = false;
  try {
    await navigator.clipboard.writeText(code);
    copied = true;
  } catch {
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(byId('recoveryCode'));
    selection.removeAllRanges();
    selection.addRange(range);
    try { copied = document.execCommand('copy') === true; } catch {}
    if (!copied) byId('recoveryCode').focus();
  }
  setSetupMessage(copied ? 'copyStatus' : 'recoveryError', copied ? 'copied' : 'copyFailed');
}

byId('languageButton').addEventListener('click', () => {
  setupLang = setupLang === 'zh' ? 'en' : 'zh';
  try { localStorage.setItem('cline_admin_lang', setupLang); } catch {}
  applyCopy();
});
byId('beginButton').addEventListener('click', () => showView('passwordView', 'passwordInput'));
byId('backButton').addEventListener('click', () => {
  byId('passwordInput').value = '';
  byId('confirmInput').value = '';
  byId('trustDeviceCheck').checked = false;
  showView('welcomeView');
});
byId('desktopForgotButton').addEventListener('click', openDesktopRecovery);
byId('recoveryBackButton').addEventListener('click', returnToDesktopSignIn);
byId('desktopLoginForm').addEventListener('submit', submitDesktopSignIn);
byId('passwordForm').addEventListener('submit', submitDesktopSetup);
byId('desktopRecoveryForm').addEventListener('submit', submitDesktopRecovery);
byId('copyButton').addEventListener('click', copySetupRecoveryCode);
byId('savedCheck').addEventListener('change', () => {
  byId('finishButton').disabled = !byId('savedCheck').checked || setupFinishAttempted;
  setSetupMessage('recoveryError');
});
byId('finishButton').addEventListener('click', finishSetupAfterSave);
byId('restartButton').addEventListener('click', closeDesktopSetup);
byId('continueButton').addEventListener('click', () => {
  try { navigateDesktopAdmin(setupState.adminURL); }
  catch (reason) { setSetupMessage('errorDetails', 'setupFailed', reason); showView('errorView'); }
});
document.querySelectorAll('.visibility-button').forEach(button => button.addEventListener('click', () => {
  const input = byId(button.dataset.target);
  input.type = input.type === 'password' ? 'text' : 'password';
  applyCopy();
}));
document.querySelectorAll('input').forEach(input => input.addEventListener('input', () => input.removeAttribute('aria-invalid')));
document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && !byId('forgotView').hidden) returnToDesktopSignIn();
});
window.addEventListener('beforeunload', event => {
  if (setupResult && !byId('savedCheck').checked) {
    event.preventDefault();
    event.returnValue = '';
  }
});
window.addEventListener('pagehide', clearSetupSecrets);
window.addEventListener('pageshow', event => {
  if (event.persisted) {
    clearSetupSecrets();
    automaticAttempted = true;
    showView('loginView', 'desktopPasswordInput');
  }
});
applyCopy();
const setupReady = loadState();

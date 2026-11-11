let _trustedDesktopState = null;
let _trustedDesktopBusy = false;
let _trustedDesktopLoad = 0;
let _trustedDesktopLoadError = false;
let _desktopSessionEnding = false;

function nativeDesktopBridge() {
  return window.go && window.go.main && window.go.main.DesktopSetupBridge;
}

function nativeDesktopAvailable() {
  const bridge = nativeDesktopBridge();
  return !!(bridge && typeof bridge.DesktopStatus === 'function');
}

function desktopSecurityErrorParts(reason, depth = 0) {
  if (depth > 5 || reason == null) return { code: '', message: '' };
  if (typeof reason === 'string') {
    const text = reason.trim();
    try {
      const parsed = JSON.parse(text);
      if (parsed !== text) return desktopSecurityErrorParts(parsed, depth + 1);
    } catch {}
    return { code: text, message: text };
  }
  if (typeof reason !== 'object') return { code: '', message: '' };
  const nested = desktopSecurityErrorParts(reason.error || reason.reason || reason.message, depth + 1);
  return { code: typeof reason.code === 'string' ? reason.code : nested.code, message: nested.message };
}

function desktopSecurityErrorText(reason, fallback = '操作失败') {
  const detail = desktopSecurityErrorParts(reason);
  const codes = {
    authentication_failed: '当前密码不正确',
    invalid_credentials: '当前密码不正确',
    current_password_required: '请输入当前密码',
    unauthorized: '登录已失效，请使用密码重新登录。',
    session_required: '登录已失效，请使用密码重新登录。',
    session_expired: '登录已失效，请使用密码重新登录。',
    desktop_session_required: '登录已失效，请使用密码重新登录。',
    forbidden: '此操作需要安全管理员权限。',
    permission_denied: '此操作需要安全管理员权限。',
    credential_storage_unavailable: '操作系统的受保护凭据存储不可用。自动登录已安全停用，请使用密码登录。',
    desktop_credential_unavailable: '操作系统的受保护凭据存储不可用。自动登录已安全停用，请使用密码登录。',
    desktop_storage_unavailable: '操作系统的受保护凭据存储不可用。自动登录已安全停用，请使用密码登录。',
    credential_storage_corrupt: '本机凭据存储已损坏。RouteNest 不会静默重建设备身份；请忘记此设备后使用密码重新信任。',
    desktop_credential_corrupt: '本机凭据存储已损坏。RouteNest 不会静默重建设备身份；请忘记此设备后使用密码重新信任。',
    desktop_storage_corrupt: '本机凭据存储已损坏。RouteNest 不会静默重建设备身份；请忘记此设备后使用密码重新信任。',
    desktop_credential_missing: '缺少本机桌面凭据。请使用密码登录，并按需信任此设备。',
    trusted_device_revoked: '此设备已被撤销。请使用密码登录；仅在确认设备安全后重新信任。',
    desktop_device_revoked: '此设备已被撤销。请使用密码登录；仅在确认设备安全后重新信任。',
    trusted_device_untrusted: '此设备已被撤销。请使用密码登录；仅在确认设备安全后重新信任。',
    desktop_device_untrusted: '此设备已被撤销。请使用密码登录；仅在确认设备安全后重新信任。',
    trusted_device_expired: '此设备凭据已过期。请使用密码重新信任此设备。',
    desktop_device_expired: '此设备凭据已过期。请使用密码重新信任此设备。',
    automatic_sign_in_disabled: '自动登录已暂停。密码登录不会自动恢复；请在此明确开启。',
    desktop_auto_sign_in_disabled: '自动登录已暂停。密码登录不会自动恢复；请在此明确开启。',
    automatic_sign_in_paused: '自动登录已暂停。密码登录不会自动恢复；请在此明确开启。',
    confirmation_required: '请明确确认删除本机桌面凭据。',
    confirm_removal_required: '请明确确认删除本机桌面凭据。',
    security_restart_required: '安全更新需要重启 RouteNest 后完成。',
    security_unavailable: '安全服务不可用，请保留现有数据目录并重试。',
    rate_limited: '尝试次数过多，请稍后重试。',
    too_many_requests: '尝试次数过多，请稍后重试。',
    bridge_unavailable: '此操作只能在受信任的 RouteNest 桌面应用中完成。',
    invalid_request: '操作失败',
    invalid_response: '安全服务不可用，请保留现有数据目录并重试。'
  };
  const key = (detail.code === 'authentication_failed' || detail.code === 'invalid_credentials') && fallback === '登录失败'
    ? '登录失败' : codes[detail.code] || codes[detail.message];
  return key ? t(key) : detail.message || t(fallback);
}

async function nativeDesktopCall(method, payload) {
  const bridge = nativeDesktopBridge();
  if (!bridge || typeof bridge[method] !== 'function') throw { code: 'bridge_unavailable' };
  const response = payload === undefined ? await bridge[method]() : await bridge[method](payload);
  if (response && (response.success === false || response.ok === false || response.error)) throw response;
  return response && response.data && typeof response.data === 'object' ? response.data : response;
}

function updateTrustedDesktopFromError(reason) {
  if (!_trustedDesktopState) return;
  const detail = desktopSecurityErrorParts(reason);
  const codes = {
    desktop_storage_unavailable: 'unavailable', desktop_credential_unavailable: 'unavailable', credential_storage_unavailable: 'unavailable',
    desktop_storage_corrupt: 'corrupt', desktop_credential_corrupt: 'corrupt', credential_storage_corrupt: 'corrupt',
    desktop_credential_missing: 'missing', trusted_device_revoked: 'revoked', desktop_device_revoked: 'revoked',
    trusted_device_untrusted: 'revoked', desktop_device_untrusted: 'revoked', trusted_device_expired: 'expired', desktop_device_expired: 'expired'
  };
  const status = codes[detail.code] || codes[detail.message];
  if (!status) return;
  const nativeState = _trustedDesktopState.native || {};
  nativeState.deviceStatus = status;
  if (['unavailable', 'corrupt', 'missing'].includes(status)) nativeState.credentialStorage = { ...(nativeState.credentialStorage || {}), state: status, stored: false, available: status !== 'unavailable', automaticSignIn: false };
  _trustedDesktopState.native = nativeState;
}

function trustedDevicePassword() {
  return (_('desktopTrustCurrentPassword') && _('desktopTrustCurrentPassword').value) || '';
}

function clearTrustedDevicePassword() {
  if (_('desktopTrustCurrentPassword')) _('desktopTrustCurrentPassword').value = '';
}

function trustedDeviceError(reason, fallback = '操作失败', focus = false) {
  const error = _('desktopSignInError');
  const message = reason == null ? '' : desktopSecurityErrorText(reason, fallback);
  if (error) {
    error.textContent = message;
    error.tabIndex = -1;
    if (focus) error.focus();
  } else if (message) toast(message, 'error');
}

function requireTrustedDevicePassword() {
  const password = trustedDevicePassword();
  if (!password) {
    trustedDeviceError(t('请输入当前密码'));
    const input = _('desktopTrustCurrentPassword');
    if (input) { input.setAttribute('aria-invalid', 'true'); input.focus(); }
  }
  return password;
}

function trustedDeviceStatusName(value) {
  const name = typeof value === 'string' ? value : value && (value.state || value.status || value.code);
  return ({ disabled: 'paused', active: 'trusted', untrusted: 'revoked', not_found: 'missing' })[name] || name || '';
}

function trustedDeviceDate(value) {
  if (!value || typeof value !== 'string' || value.startsWith('0001-')) return null;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date : null;
}

function trustedDeviceLifecycle(device) {
  if (!device) return 'missing';
  const status = trustedDeviceStatusName(device);
  if (status === 'revoked' || device.revoked === true || trustedDeviceDate(device.revokedAt)) return 'revoked';
  const expiry = trustedDeviceDate(device.expiresAt);
  if (status === 'expired' || expiry && expiry.getTime() <= Date.now()) return 'expired';
  if (!expiry) return 'unknown';
  return 'trusted';
}

function trustedDesktopModel() {
  const state = _trustedDesktopState || {};
  const nativeState = state.native || {};
  const native = nativeDesktopAvailable();
  const devices = Array.isArray(state.devices) ? state.devices : [];
  const credential = nativeState.credentialStorage || {};
  const storage = native ? trustedDeviceStatusName(credential.state || nativeState.storage) || 'unknown' : 'browser';
  const available = native && credential.available !== false && !['unavailable', 'unknown'].includes(storage);
  const stored = credential.stored === undefined ? ['stored', 'paused'].includes(storage) : credential.stored === true;
  const localAutomatic = credential.automaticSignIn === undefined ? nativeState.automaticSignIn === true : credential.automaticSignIn === true;
  const deviceStatus = trustedDeviceStatusName(nativeState.deviceStatus);
  const currentID = native ? nativeState.deviceID || (nativeState.deviceStatus && nativeState.deviceStatus.deviceID) || '' : '';
  const current = devices.find(device => device.id === currentID) || null;
  const serverState = trustedDeviceLifecycle(current);
  const known = state.devicesKnown !== false && _trustedDesktopState !== null;
  let status = 'missing';
  if (!native) status = 'browser';
  else if (storage === 'corrupt' || storage === 'unavailable' || storage === 'unknown') status = storage;
  else if (!available) status = 'unavailable';
  else if (['revoked', 'expired', 'corrupt', 'unavailable'].includes(deviceStatus)) status = deviceStatus;
  else if (!known) status = 'unknown';
  else if (!stored) status = 'missing';
  else if (current && serverState !== 'trusted') status = serverState;
  else if (currentID && !current) status = 'revoked';
  else if (current) status = 'trusted';
  else status = 'unknown';
  const trusted = status === 'trusted';
  const automatic = trusted && localAutomatic && current.automaticSignIn === true && deviceStatus !== 'paused' && storage !== 'paused';
  if (trusted && !automatic) status = 'paused';
  return {
    native, nativeState, devices, currentID, current, storage, available, stored, known,
    status, trusted, automatic,
    canTrust: native && known && available && !trusted && !['corrupt', 'unknown', 'unavailable'].includes(status),
    canForget: native && (stored || !!currentID || ['corrupt', 'unavailable', 'revoked', 'expired'].includes(status))
  };
}

function desktopTrustGuidance(model) {
  const labels = {
    browser: '普通浏览器不能读取或注册桌面凭据。请在 RouteNest 桌面应用中管理此设备。',
    corrupt: '本机凭据存储已损坏。RouteNest 不会静默重建设备身份；请忘记此设备后使用密码重新信任。',
    unavailable: '操作系统的受保护凭据存储不可用。自动登录已安全停用，请使用密码登录。',
    revoked: '此设备已被撤销。请使用密码登录；仅在确认设备安全后重新信任。',
    expired: '此设备凭据已过期。请使用密码重新信任此设备。',
    missing: '缺少本机桌面凭据。请使用密码登录，并按需信任此设备。',
    paused: '自动登录已暂停。密码登录不会自动恢复；请在此明确开启。',
    unknown: '无法验证设备状态。请刷新后重试；密码登录仍可使用。',
    trusted: '设备名称和操作系统仅用于识别，不作为认证证明。'
  };
  return t(labels[model.status] || labels.unknown);
}

function setDesktopStatusBadge(id, label, success, warning) {
  const overview = _('overview' + id.replace(/^desktop/, ''));
  if (overview) overview.textContent = t(label);
  const element = _(id);
  if (!element) return;
  element.textContent = t(label);
  element.classList.toggle('is-success', success);
  element.classList.toggle('is-warning', warning);
}

function appendTrustedDeviceDate(host, label, value, empty) {
  const line = document.createElement('div');
  line.className = 'access-row-meta';
  const prefix = document.createElement('span');
  prefix.textContent = t(label) + ' ';
  line.appendChild(prefix);
  const date = trustedDeviceDate(value);
  const display = document.createElement(date ? 'time' : 'span');
  if (date) {
    display.dateTime = date.toISOString();
    display.textContent = formatDateTime(value);
  } else display.textContent = t(empty);
  line.appendChild(display);
  host.appendChild(line);
}

function renderTrustedDevices() {
  const model = trustedDesktopModel();
  const thisLabels = { revoked: '已撤销', expired: '已过期', corrupt: '凭据已损坏', unavailable: '不可用', missing: '不受信任', unknown: '未知', browser: '仅浏览器' };
  const storageLabels = { stored: '可用', trusted: '可用', paused: '已暂停', missing: '缺少凭据', corrupt: '凭据已损坏', unavailable: '不可用', browser: '仅浏览器', revoked: '已撤销', expired: '已过期' };
  setDesktopStatusBadge('desktopAutoSignInStatus', model.automatic ? '开启' : model.trusted ? '已暂停' : '关闭', model.automatic, model.trusted && !model.automatic);
  setDesktopStatusBadge('desktopThisDeviceStatus', model.trusted ? '受信任' : thisLabels[model.status] || '不受信任', model.trusted, !model.trusted && !['missing', 'browser'].includes(model.status));
  setDesktopStatusBadge('desktopCredentialStorageStatus', storageLabels[model.storage] || '未知', model.storage === 'stored', ['paused', 'missing', 'corrupt', 'unavailable'].includes(model.storage));
  if (_('desktopSignInGuidance')) _('desktopSignInGuidance').textContent = desktopTrustGuidance(model);
  const lastUsed = model.current && trustedDeviceDate(model.current.lastUsedAt);
  if (_('desktopLastUsed')) _('desktopLastUsed').textContent = t('上次成功使用：') + (lastUsed ? formatDateTime(model.current.lastUsedAt) : t('尚未使用'));
  const trustButton = _('trustThisDeviceButton');
  if (trustButton) {
    trustButton.hidden = !model.native || model.trusted;
    trustButton.disabled = _trustedDesktopBusy || !model.canTrust;
    trustButton.setAttribute('aria-describedby', 'desktopSignInGuidance desktopSignInError');
  }
  const toggleButton = _('toggleDesktopAutoSignInButton');
  if (toggleButton) {
    toggleButton.hidden = !model.native || !model.trusted;
    toggleButton.disabled = _trustedDesktopBusy || !model.available || !model.known;
    toggleButton.textContent = t(model.automatic ? '关闭自动登录' : '开启自动登录');
    toggleButton.setAttribute('aria-pressed', String(model.automatic));
    toggleButton.setAttribute('aria-describedby', 'desktopSignInGuidance desktopSignInError');
  }
  const forgetButton = _('forgetThisDeviceButton');
  if (forgetButton) { forgetButton.hidden = !model.canForget; forgetButton.disabled = _trustedDesktopBusy; }
  const revokeAllButton = _('revokeAllTrustedDevicesButton');
  if (revokeAllButton) revokeAllButton.disabled = _trustedDesktopBusy || !model.known || model.devices.length === 0;
  const form = _('desktopSignInForm');
  if (form) form.setAttribute('aria-busy', String(_trustedDesktopBusy));
  const host = _('trustedDevicesList');
  if (!host) return;
  host.setAttribute('role', 'list');
  host.textContent = '';
  if (!model.known || !model.devices.length) {
    const empty = document.createElement('div');
    empty.className = 'access-empty';
    empty.textContent = t(model.known ? '没有受信任的桌面设备' : '无法验证设备状态。请刷新后重试；密码登录仍可使用。');
    host.appendChild(empty);
    return;
  }
  model.devices.forEach(device => {
    const row = document.createElement('div');
    row.className = 'access-list-row';
    row.dataset.deviceId = device.id;
    row.setAttribute('role', 'listitem');
    const info = document.createElement('div');
    info.className = 'access-row-info';
    const heading = document.createElement('div');
    heading.className = 'access-row-title';
    const title = document.createElement('strong');
    const name = device.metadata && typeof device.metadata.name === 'string' ? device.metadata.name : t('未命名设备');
    title.textContent = name || t('未命名设备');
    heading.appendChild(title);
    const addBadge = (text, tone) => {
      const badge = document.createElement('span');
      badge.className = 'access-badge' + (tone ? ' is-' + tone : '');
      badge.textContent = t(text);
      heading.appendChild(badge);
    };
    const current = device.id === model.currentID;
    const lifecycle = trustedDeviceLifecycle(device);
    if (current) addBadge('此设备', model.trusted ? 'success' : 'warning');
    if (lifecycle === 'expired' || current && model.status === 'expired') addBadge('已过期', 'warning');
    else if (lifecycle === 'revoked' || current && model.status === 'revoked') addBadge('已撤销', 'warning');
    else if (lifecycle === 'unknown') addBadge('有效期未知', 'warning');
    else if (device.automaticSignIn !== true || current && !model.automatic) addBadge('自动登录已暂停', 'warning');
    const platform = document.createElement('div');
    platform.className = 'access-row-meta';
    platform.textContent = device.metadata && typeof device.metadata.platform === 'string' && device.metadata.platform || t('未知平台');
    info.appendChild(heading);
    info.appendChild(platform);
    appendTrustedDeviceDate(info, '上次使用', device.lastUsedAt, '尚未使用');
    appendTrustedDeviceDate(info, '创建时间', device.createdAt, '未知');
    appendTrustedDeviceDate(info, '到期', device.expiresAt, '有效期未知');
    row.appendChild(info);
    const actions = document.createElement('div');
    actions.className = 'access-row-actions';
    const revoke = document.createElement('button');
    revoke.type = 'button';
    revoke.className = 'btn btn-sm btn-danger';
    revoke.textContent = t('撤销');
    revoke.setAttribute('aria-label', t('撤销') + ' · ' + title.textContent);
    revoke.disabled = _trustedDesktopBusy || lifecycle === 'revoked';
    revoke.addEventListener('click', () => revokeTrustedDevice(device.id, current));
    actions.appendChild(revoke);
    row.appendChild(actions);
    host.appendChild(row);
  });
}

async function loadTrustedDevices() {
  if (!_('trustedDevicesList') || !adminHasPermission('security.write')) return;
  const request = ++_trustedDesktopLoad;
  if (_trustedDesktopLoadError) { trustedDeviceError(null); _trustedDesktopLoadError = false; }
  const [devices, native] = await Promise.allSettled([
    api('GET', '/auth/trusted-devices'),
    nativeDesktopAvailable() ? nativeDesktopCall('DesktopStatus') : Promise.resolve({ storage: 'browser', automaticSignIn: false })
  ]);
  if (request !== _trustedDesktopLoad) return;
  const response = devices.status === 'fulfilled' ? devices.value : null;
  const payload = response && (response.data || response);
  const known = devices.status === 'fulfilled' && response && response.success !== false && Array.isArray(payload.devices);
  _trustedDesktopState = {
    devices: known ? payload.devices : [],
    devicesKnown: !!known,
    native: native.status === 'fulfilled' && native.value ? native.value : { storage: 'unknown' }
  };
  renderTrustedDevices();
  if (!known) {
    _trustedDesktopLoadError = true;
    trustedDeviceError(devices.status === 'rejected' ? devices.reason : response, '无法验证设备状态。请刷新后重试；密码登录仍可使用。');
  } else if (native.status === 'rejected') {
    _trustedDesktopLoadError = true;
    trustedDeviceError(native.reason);
  }
}

async function runTrustedDeviceAction(action, requirePassword = true) {
  if (_trustedDesktopBusy) return;
  const password = requirePassword ? requireTrustedDevicePassword() : '';
  if (requirePassword && !password) return;
  const focusID = document.activeElement && document.activeElement.id;
  _trustedDesktopBusy = true;
  trustedDeviceError(null);
  renderTrustedDevices();
  try { await action(password); }
  catch (reason) {
    updateTrustedDesktopFromError(reason);
    trustedDeviceError(reason, '操作失败', true);
  }
  finally {
    clearTrustedDevicePassword();
    _trustedDesktopBusy = false;
    renderTrustedDevices();
    const target = [focusID, 'toggleDesktopAutoSignInButton', 'trustThisDeviceButton', 'desktopTrustCurrentPassword']
      .map(id => id && _(id)).find(element => element && !element.hidden && !element.disabled);
    if (target && !(_('desktopSignInError') && _('desktopSignInError').textContent)) target.focus();
  }
}

async function trustThisDesktop() {
  if (_trustedDesktopBusy) return;
  const model = trustedDesktopModel();
  if (!model.canTrust) { trustedDeviceError(desktopTrustGuidance(model), '无法信任此设备', true); return; }
  await runTrustedDeviceAction(async password => {
    await nativeDesktopCall('TrustThisDevice', { currentPassword: password });
    await loadTrustedDevices();
    const updated = trustedDesktopModel();
    if (updated.trusted && updated.automatic) toast(t('此设备已受信任，自动登录已开启'), 'success');
    else trustedDeviceError(desktopTrustGuidance(updated));
  });
}

async function toggleDesktopAutoSignIn() {
  if (_trustedDesktopBusy) return;
  const model = trustedDesktopModel();
  if (!model.trusted || !model.available || !model.known) { trustedDeviceError(desktopTrustGuidance(model), '操作失败', true); return; }
  const enabled = !model.automatic;
  await runTrustedDeviceAction(async password => {
    await nativeDesktopCall('SetDesktopAutoSignIn', { currentPassword: password, enabled });
    await loadTrustedDevices();
    const updated = trustedDesktopModel();
    if (updated.known && updated.trusted && updated.automatic === enabled) toast(t(enabled ? '自动登录已开启' : '自动登录已关闭'), 'success');
    else trustedDeviceError(desktopTrustGuidance(updated));
  });
}

async function forgetThisDesktop() {
  if (!nativeDesktopAvailable()) return;
  const model = trustedDesktopModel();
  const localCleanup = ['corrupt', 'unavailable'].includes(model.storage);
  await runTrustedDeviceAction(async password => {
    if (!(await showConfirm(t(localCleanup ? '删除本机桌面凭据？服务器上的设备记录不会自动撤销。' : '忘记此设备并删除本机桌面凭据？')))) return;
    if (localCleanup) await nativeDesktopCall('RemoveLocalDesktopCredential', { confirmRemoval: true });
    else await nativeDesktopCall('ForgetThisDevice', { currentPassword: password, confirmRemoval: true });
    toast(t(localCleanup ? '本机桌面凭据已删除。请检查设备列表，并按需撤销服务器上的记录。' : '此设备已忘记'), 'success');
    await loadTrustedDevices();
  }, !localCleanup);
}

async function revokeTrustedDevice(deviceID, current) {
  if (!deviceID) return;
  const removeLocal = current && nativeDesktopAvailable();
  await runTrustedDeviceAction(async password => {
    if (!(await showConfirm(t(removeLocal ? '撤销此设备并删除本机桌面凭据？' : '确认撤销此受信任设备？')))) return;
    await api('POST', '/auth/trusted-devices/revoke', { deviceID, currentPassword: password });
    let cleanupFailed = false;
    if (removeLocal) {
      try { await nativeDesktopCall('RemoveLocalDesktopCredential', { confirmRemoval: true }); }
      catch (reason) {
        cleanupFailed = true;
        trustedDeviceError(t('服务器已撤销此设备，但本机凭据删除失败。它已失效，请修复操作系统凭据存储。'));
      }
    }
    if (!cleanupFailed) toast(t('受信任设备已撤销'), 'success');
    await loadTrustedDevices();
  });
}

async function revokeAllTrustedDevices() {
  const removeLocal = nativeDesktopAvailable();
  await runTrustedDeviceAction(async password => {
    if (!(await showConfirm(t(removeLocal ? '撤销所有受信任桌面设备并删除本机桌面凭据？' : '确认撤销所有受信任桌面设备？')))) return;
    await api('POST', '/auth/trusted-devices/revoke-all', { currentPassword: password });
    let cleanupFailed = false;
    if (removeLocal) {
      try { await nativeDesktopCall('RemoveLocalDesktopCredential', { confirmRemoval: true }); }
      catch (reason) {
        cleanupFailed = true;
        trustedDeviceError(t('服务器已撤销全部设备，但本机凭据删除失败。它已失效，请修复操作系统凭据存储。'));
      }
    }
    if (!cleanupFailed) toast(t('所有受信任设备已撤销'), 'success');
    await loadTrustedDevices();
  });
}

async function lockAdmin() {
  if (_desktopSessionEnding) return;
  _desktopSessionEnding = true;
  try {
    if (nativeDesktopAvailable()) await nativeDesktopCall('LockDesktop');
    else await api('POST', '/logout', {});
    clearTrustedDevicePassword();
    if (typeof _nativeAdminCSRF !== 'undefined') _nativeAdminCSRF = '';
    if (typeof renderAdminPrincipal === 'function') renderAdminPrincipal(null);
    _trustedDesktopLoad++;
    _trustedDesktopState = null;
    location.reload();
  } catch (reason) {
    trustedDeviceError(reason, '退出登录失败', true);
    _desktopSessionEnding = false;
  }
}

if (_('desktopTrustCurrentPassword')) _('desktopTrustCurrentPassword').addEventListener('input', event => event.target.removeAttribute('aria-invalid'));

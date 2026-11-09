// Backend-authoritative welcome state for the existing Overview route.
let _onboardingStatus = null;
const ONBOARDING_METRICS_SESSION_KEY = 'routenest_onboarding_metrics_session';

function ensureOnboardingWelcome() {
  if (_('onboardingWelcome')) return _('onboardingWelcome');
  const template = _('onboardingWelcomeTemplate');
  const panel = _('tab-dashboard');
  if (!template || !panel) return null;
  panel.insertBefore(template.content.cloneNode(true), panel.firstChild);
  applyLang();
  return _('onboardingWelcome');
}

function onboardingMetricsPreferred() {
  try { return sessionStorage.getItem(ONBOARDING_METRICS_SESSION_KEY) === '1'; }
  catch (error) { return false; }
}

function showOnboardingWelcome(force) {
  if (force) {
    try { sessionStorage.removeItem(ONBOARDING_METRICS_SESSION_KEY); } catch (error) {}
  }
  const welcome = ensureOnboardingWelcome();
  const panel = _('tab-dashboard');
  if (!welcome || !panel) return;
  const visible = !!force || (!!_onboardingStatus && _onboardingStatus.stage !== 'dashboard' && !onboardingMetricsPreferred());
  panel.classList.toggle('onboarding-active', visible);
  welcome.hidden = !visible;
  if (visible) {
    stopDashPolling();
    requestAnimationFrame(() => _('onboardingTitle').focus({ preventScroll: true }));
  } else if (_activeTabName === 'dashboard') {
    startDashPolling();
  }
}

function showMetricsDashboard() {
  try { sessionStorage.setItem(ONBOARDING_METRICS_SESSION_KEY, '1'); } catch (error) {}
  showOnboardingWelcome(false);
}

function setOnboardingStep(id, complete, available) {
  const step = _(id);
  if (!step) return;
  step.classList.toggle('is-complete', !!complete);
  step.classList.toggle('is-pending', !complete && available !== false);
  const status = step.querySelector('.onboarding-status');
  if (status) status.textContent = t(complete ? '已完成' : '待完成');
  const index = step.querySelector('.onboarding-step-index');
  if (index && complete) index.textContent = '✓';
}

function renderOnboardingStatus(status) {
  _onboardingStatus = status || null;
  ensureOnboardingWelcome();
  if (!status) return;
  setOnboardingStep('onboardingSecurityStep', true, true);
  setOnboardingStep('onboardingProviderStep', status.providerConfigured, true);
  setOnboardingStep('onboardingModelsStep', status.modelsAvailable, status.providerConfigured);
  setOnboardingStep('onboardingKeysStep', status.clientAPIKeyConfigured, status.providerReady);
  setOnboardingStep('onboardingRequestStep', status.successfulGatewayRequest, status.providerReady);
  const completed = [true, status.providerConfigured, status.modelsAvailable, status.clientAPIKeyConfigured, status.successfulGatewayRequest].filter(Boolean).length;
  if (_('onboardingProgressValue')) _('onboardingProgressValue').textContent = completed + '/5';
  const verify = _('onboardingVerifyButton');
  if (verify) verify.hidden = !(status.stage === 'verify_provider' && status.verification && ['custom_provider', 'cline_account', 'opencode_zen'].includes(status.verification.kind));
  const notice = _('onboardingNotice');
  const messages = {
    connect_provider: '连接第一个 Provider 后，RouteNest 会在这里继续显示下一步。',
    verify_provider: 'Provider 已保存，但尚未通过连接测试。',
    first_request: 'Provider 已就绪。配置客户端并发送第一条真实网关请求。'
  };
  if (notice) {
    notice.hidden = !messages[status.stage];
    notice.textContent = t(messages[status.stage] || '');
  }
  showOnboardingWelcome(false);
}

async function refreshOnboardingStatus() {
  try {
    const envelope = await api('GET', '/onboarding/status');
    renderOnboardingStatus(envelope.data || {});
  } catch (error) {
    const notice = ensureOnboardingWelcome() && _('onboardingNotice');
    if (notice) { notice.hidden = false; notice.textContent = t('无法加载设置进度: ') + error.message; }
  }
}

async function testOnboardingProvider() {
  const verification = _onboardingStatus && _onboardingStatus.verification;
  const button = _('onboardingVerifyButton');
  if (!verification || !['custom_provider', 'cline_account', 'opencode_zen'].includes(verification.kind)) return;
  button.disabled = true;
  try {
    let result = {};
    if (verification.kind === 'custom_provider') {
      if (!verification.id) return;
      const envelope = await api('POST', '/providers/test', { id: verification.id });
      result = envelope.data || {};
      if (!result.ok) throw new Error(result.error || t('连接测试失败'));
    } else if (verification.kind === 'cline_account') {
      if (!verification.id) return;
      const envelope = await api('POST', '/accounts/test', { accountId: verification.id });
      result = ((envelope.data || {}).results || [])[0] || {};
      if (!result.ok) throw new Error(result.error || t('连接测试失败'));
    } else {
      await api('POST', '/opencode/models/sync', {});
    }
    toast(t('Provider 连接测试成功'), 'success');
    await refreshOnboardingStatus();
  } catch (error) {
    const notice = _('onboardingNotice');
    if (notice) { notice.hidden = false; notice.textContent = t('连接测试失败: ') + error.message; }
  } finally { button.disabled = false; }
}

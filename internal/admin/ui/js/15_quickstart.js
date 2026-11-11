// ========== Quick Start：从当前运行实例生成可执行的客户端示例 ==========
function quickStartGatewayBase() {
  if (typeof nativeDesktopAvailable === 'function' && nativeDesktopAvailable()) return gatewayV1URL().replace(/\/v1\/?$/, '');
  try { return location.origin.replace(/\/$/, ''); } catch (e) { return 'http://127.0.0.1:3457'; }
}

function quickStartSuggestedModel() {
  // Check if user has manually selected a model from the dropdown
  const selectElement = _('quickstartModelSelect');
  if (selectElement && selectElement.value) {
    return selectElement.value;
  }

  // Fallback to configured default model
  const configured = _('settingDefModel');
  if (configured && configured.dataset && configured.dataset.configLoaded === '1' && configured.value) return configured.value;

  // Fallback to first available enabled model
  const available = (_cachedModels || []).find(model => model && !model.disabled && model.id);
  return available ? available.id : '<model-id>';
}

let _quickStartStatus = { providers: null, keys: null };

function quickStartAvailableModels() {
  const seen = new Set();
  return (_cachedModels || [])
    .filter(model => model && !model.disabled && model.id && !seen.has(model.id) && seen.add(model.id))
    .sort((a, b) => String(a.id).localeCompare(String(b.id)));
}

function populateQuickStartModelSelect() {
  const select = _('quickstartModelSelect');
  if (!select) return;
  const models = quickStartAvailableModels();
  const configured = _('settingDefModel');
  const preferred = select.value || (configured && configured.value) || (models[0] && models[0].id) || '';
  select.textContent = '';
  if (!models.length) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = t('暂无可用模型');
    select.appendChild(option);
  } else {
    models.forEach(model => {
      const option = document.createElement('option');
      option.value = model.id;
      option.textContent = model.name && model.name !== model.id ? model.name + ' · ' + model.id : model.id;
      select.appendChild(option);
    });
    select.value = models.some(model => model.id === preferred) ? preferred : models[0].id;
  }
  if (!select.dataset.bound) {
    select.addEventListener('change', renderQuickStartExamples);
    select.dataset.bound = '1';
  }
}

function renderQuickStartExamples() {
  const base = quickStartGatewayBase();
  const model = quickStartSuggestedModel();
  const apiKey = '<your-api-key>';
  const body = JSON.stringify({ model: model, messages: [{ role: 'user', content: 'hi' }] });
  const examples = {
    quickstartClaudeCode: 'export ANTHROPIC_BASE_URL="' + base + '"\n' +
      'export ANTHROPIC_API_KEY="' + apiKey + '"\n' +
      'claude --model ' + model,
    quickstartClineCode: 'API Provider: Anthropic\n' +
      'Base URL:  ' + base + '\n' +
      'API Key:   ' + apiKey + '\n' +
      'Model:     ' + model,
    quickstartOpenAICode: 'Base URL: ' + base + '/v1\n' +
      'API Key:  ' + apiKey + '\n' +
      'Model:    ' + model,
    quickstartCurlCode: 'curl -s ' + base + '/v1/chat/completions \\\n' +
      '  -H "x-api-key: ' + apiKey + '" -H "Content-Type: application/json" \\\n' +
      "  -d '" + body + "'",
  };
  Object.entries(examples).forEach(([id, value]) => {
    const element = _(id);
    if (element) element.textContent = value;
    const card = element && typeof element.closest === 'function' ? element.closest('.quickstart-card') : null;
    if (card) {
      let copyBtn = card.querySelector('.copy-btn');
      if (!copyBtn) {
        copyBtn = document.createElement('button');
        copyBtn.type = 'button';
        copyBtn.className = 'btn btn-sm copy-btn';
        copyBtn.setAttribute('aria-label', t('复制到剪贴板'));
        card.appendChild(copyBtn);
      }
      copyBtn.textContent = t('复制');
      copyBtn.onclick = () => {
        copyText(element ? element.textContent : '').then(() => {
          copyBtn.textContent = t('已复制');
          copyBtn.classList.add('copied');
          setTimeout(() => { copyBtn.textContent = t('复制'); copyBtn.classList.remove('copied'); }, 2000);
        }).catch(() => {
          copyBtn.textContent = t('复制失败');
          setTimeout(() => { copyBtn.textContent = t('复制'); }, 2000);
        });
      };
    }
  });
}

async function refreshQuickStartStatus() {
  if (typeof api !== 'function') return;
  const requests = [api('GET', '/provider-instances')];
  if (typeof adminHasPermission !== 'function' || adminHasPermission('credential.write')) requests.push(api('GET', '/keys'));
  const results = await Promise.allSettled(requests);
  const providers = results[0];
  if (providers.status === 'fulfilled') {
    const instances = providers.value && providers.value.data && providers.value.data.instances;
    _quickStartStatus.providers = Array.isArray(instances) && instances.length > 0;
  }
  const keys = results[1];
  if (keys && keys.status === 'fulfilled') {
    const values = keys.value && keys.value.data && keys.value.data.keys;
    _quickStartStatus.keys = Array.isArray(values) && values.length > 0;
  }
  updateQuickStartProgress();
}

async function loadQuickStart() {
  // Render immediately, then refine the model and readiness state in parallel.
  renderQuickStartExamples();
  const configPromise = typeof adminHasPermission === 'function' && adminHasPermission('provider.write') ? loadConfig() : Promise.resolve();
  await Promise.all([loadModels(), configPromise, refreshQuickStartStatus()]);
  populateQuickStartModelSelect();
  renderQuickStartExamples();
  updateQuickStartProgress();
}

function updateQuickStartProgress() {
  updateStepStatus('step-provider', _quickStartStatus.providers);
  updateStepStatus('step-api-key', _quickStartStatus.keys);
  updateStepStatus('step-configure', _quickStartStatus.providers === true && quickStartSuggestedModel() !== '<model-id>');
}

function updateStepStatus(stepId, completed) {
  const step = _(stepId);
  if (!step || completed === null) return;
  const dot = step.querySelector('.step-dot');
  const statusText = step.querySelector('.step-status-text');
  const statusCheck = step.querySelector('.step-status-check');
  if (!dot) return;
  dot.classList.toggle('step-complete', completed);
  dot.classList.toggle('step-incomplete', !completed);
  if (statusText) statusText.classList.toggle('hidden', completed);
  if (statusCheck) statusCheck.classList.toggle('hidden', !completed);
}

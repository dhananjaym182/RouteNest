// ========== Quick Start：从当前运行实例生成可执行的客户端示例 ==========
function quickStartGatewayBase() {
  try { return location.origin.replace(/\/$/, ''); } catch (e) { return 'http://127.0.0.1:3457'; }
}

function quickStartSuggestedModel() {
  const configured = _('settingDefModel');
  if (configured && configured.dataset && configured.dataset.configLoaded === '1' && configured.value) return configured.value;
  const available = (_cachedModels || []).find(model => model && !model.disabled && model.id);
  return available ? available.id : '<model-id>';
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
  Object.entries(examples).forEach(([id, value]) => { const element = _(id); if (element) element.textContent = value; });
}

async function loadQuickStart() {
  // Replace static examples immediately; network-backed model/config reads can
  // refine the suggestion without briefly showing a fictional default.
  renderQuickStartExamples();
  await loadModels();
  if (typeof adminHasPermission === 'function' && adminHasPermission('provider.write')) await loadConfig();
  renderQuickStartExamples();
}

// ========== 内建上游配置：OpenCode Zen 与 Cline 出口 ==========
// OpenCode Zen 配置在 Providers 页的原生配置抽屉；默认（全局）出口代理在
// 「全局出口代理」页（#network，ui/html/pages/network.html）。两者共用此文件。
// 单来源入口（OpenCode Zen）。页面上的同步按钮统一走 syncAllModels / syncModelGroup。
async function syncOcModels() {
  try {
    const d = await api('POST', '/opencode/models/sync');
    const res = d.data || {};
    _modelCatalogApplied = ++_modelCatalogRequest;
    notifyModelSyncResult(res, 'OpenCode Zen');
    await loadModels();
    if (res.changed) {
      toast(t('OpenCode Zen 模型列表已更新'), 'success');
    } else {
      toast(t('模型无变化'), 'info');
    }
  } catch (e) {
    toast(t('模型同步失败') + ': ' + (e.message || ''), 'error');
  }
}

// ========== OpenCode Zen 免费模型配置 ==========
let _ocConfigLoadPromise = null;
let _ocProxyBaseline = '';
let _clineProxyBaseline = '';
async function loadOcConfig() {
  if (_ocConfigLoadPromise) return _ocConfigLoadPromise;
  const controls = Array.from(document.querySelectorAll('[onchange="saveOcConfig()"], [onclick="saveOcConfig(this)"]'));
  const disabled = controls.map(el => el.disabled);
  const panel = _('providerNativeOpenCode');
  controls.forEach(el => { el.disabled = true; });
  panel.setAttribute('aria-busy', 'true');
  _ocConfigLoadPromise = loadOcConfigSnapshot();
  try { return await _ocConfigLoadPromise; }
  finally {
    _ocConfigLoadPromise = null;
    controls.forEach((el, i) => { el.disabled = disabled[i]; });
    panel.removeAttribute('aria-busy');
  }
}

async function loadOcConfigSnapshot() {
  try {
    const d = await api('GET', '/opencode/config');
    const c = d.data;
    _('ocEnabled').value = String(!!c.enabled);
    if (c.key) _('ocKey').value = c.key;
    if (c.baseURL) _('ocBaseURL').value = c.baseURL;
    _('ocMaxConcurrency').value = c.maxConcurrency;
    _('ocRetries').value = c.retries;
    _('ocFailover').value = String(!!c.failover);
    _('ocFailoverCount').value = c.failoverCount;
    _('ocFailoverMinutes').value = c.failoverMinutes;
    const cp = c.compaction || {};
    _('ocCompactAuto').value = String(!!cp.auto);
    _('ocCompactBuffer').value = cp.buffer != null ? cp.buffer : 20000;
    _('ocCompactKeepTokens').value = cp.keepTokens != null ? cp.keepTokens : 8000;
    _('ocCompactMaxSummary').value = cp.maxSummary != null ? cp.maxSummary : 4096;
    _('ocProxyStrategy').value = c.proxyStrategy || 'round_robin';
    _('ocProxies').value = (c.proxies || []).join('\n');
    _ocProxyBaseline = _('ocProxies').value;
    // 运行状态与出口冷却
    const rt = c.runtime || {};
    let status;
    if (!c.enabled) status = '<span class="mut">⏸ ' + t('已停用') + '</span>';
    else if (rt.failoverActive) status = '<span class="red-txt">🔴 ' + t('故障转移中（OpenCode Zen 暂不可用，请求走 Cline 池）') + '</span>';
    else status = '<span class="green-txt">🟢 ' + t('正常') + '</span>' +
      '<span class="ml-8">' + t('已同步模型') + ': ' + (c.syncedModels || 0) + '</span>';
    _('ocRuntimeStatus').innerHTML = status;
    const cds = c.proxyCooldowns || {};
    const entries = Object.entries(cds);
    _('ocProxyCooldowns').textContent = entries.length
      ? entries.map(([p, until]) => maskProxyForDisplay(p) + ' → ' + until).join('; ')
      : t('无冷却');
    await checkModelDelistings();
  } catch (e) { toast(t('加载失败') + ': ' + e.message, 'error'); }
}

function maskProxyForDisplay(p) {
  return p.replace(/\/\/([^@/]+)@/, '//***@');
}

async function saveOcConfig(btn) {
  const numOr = (id, def) => { const v = parseInt(_(id).value, 10); return isNaN(v) ? def : v; };
  const payload = {
    enabled: _('ocEnabled').value === 'true',
    key: _('ocKey').value.trim(),
    baseURL: _('ocBaseURL').value.trim(),
    maxConcurrency: numOr('ocMaxConcurrency', 8),
    retries: numOr('ocRetries', 3),
    failover: _('ocFailover').value === 'true',
    failoverCount: numOr('ocFailoverCount', 3),
    failoverMinutes: numOr('ocFailoverMinutes', 5),
    proxyStrategy: _('ocProxyStrategy').value,
    proxies: _('ocProxies').value.split('\n').map(s => s.trim()).filter(Boolean),
    compaction: {
      auto: _('ocCompactAuto').value === 'true',
      buffer: numOr('ocCompactBuffer', 20000),
      keepTokens: numOr('ocCompactKeepTokens', 8000),
      maxSummary: numOr('ocCompactMaxSummary', 4096),
    },
  };
  // GET masks proxy credentials. Omit unchanged proxy lists so unrelated
  // edits preserve the saved credentials instead of writing the mask back.
  if (_('ocProxies').value === _ocProxyBaseline) delete payload.proxies;
  if (payload.proxies && payload.proxies.some(p => /:\/\/\*\*\*@/.test(p))) {
    toast(t('修改代理列表时，请重新输入包含完整凭据的地址。'), 'error');
    return;
  }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/opencode/config/update', payload);
    showSaveResult('ocSaveResult', true, t('OpenCode Zen 配置已保存'));
    await loadOcConfig();
    loadGlobal(false);
  } catch (e) {
    toast(t('保存失败: ') + (e.message || ''), 'error');
  }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

async function loadClineProxyConfig() {
  try {
    const d = await api('GET', '/routenest/config');
    const c = d.data;
    _('clineProxyStrategy').value = c.proxyStrategy || 'round_robin';
    _('clineProxies').value = (c.proxies || []).join('\n');
    _clineProxyBaseline = _('clineProxies').value;
  } catch (e) { toast(t('加载失败') + ': ' + e.message, 'error'); }
}

async function saveClineProxyConfig(btn) {
  const payload = {
    proxyStrategy: _('clineProxyStrategy').value,
    proxies: _('clineProxies').value.split('\n').map(s => s.trim()).filter(Boolean),
  };
  if (_('clineProxies').value === _clineProxyBaseline) delete payload.proxies;
  if (payload.proxies && payload.proxies.some(p => /:\/\/\*\*\*@/.test(p))) {
    toast(t('修改代理列表时，请重新输入包含完整凭据的地址。'), 'error');
    return;
  }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/routenest/config/update', payload);
    showSaveResult('clineProxySaveResult', true, t('Cline 代理配置已保存'));
    await loadClineProxyConfig();
    loadGlobal(false);
  } catch (e) {
    toast(t('保存失败: ') + (e.message || ''), 'error');
  }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

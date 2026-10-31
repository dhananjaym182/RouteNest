// ========== §8.3 登录连接：设备码登录 / 粘贴 token / 列表·测试·删除 ==========
let _connFlows = [];
let _connList = [];
let _loginSessionId = '';
let _loginPollTimer = null;

// 流程 key → 用户可读名（未知 key 回落原样显示）
const _flowLabels = {
  kilocode: 'Kilo Code', github: 'GitHub Copilot', qwen: 'Qwen Chat',
  'kimi-coding': 'Kimi for Coding', 'codebuddy-cn': 'CodeBuddy CN（腾讯）',
  'codebuddy-intl': 'CodeBuddy Intl', workbuddy: 'WorkBuddy',
  qoder: 'Qoder', 'qoder-cn': 'Qoder CN',
};
function flowLabel(p) { return t(_flowLabels[p] || p); }

async function loadConnections() {
  try {
    const d = await api('GET', '/connections');
    const r = d.data || {};
    _connFlows = r.flows || [];
    _connList = r.connections || [];
  } catch (e) {
    _connFlows = []; _connList = [];
  }
  renderConnectionProviders();
  renderConnectionsList();
  // 导入弹窗的 Provider 下拉随连接列表刷新（流程 / 已有连接分组可能变化）
  const imp = _('connImportModal');
  if (imp && imp.style.display === 'flex') connImportBuildSelect();
  const ed = _('providerEditor');
  if (ed && ed.classList.contains('open')) renderConnPick();
  refreshProvDetailIfOpen();
}

// Wave-5: OAuth providers render as tiles grouped by connection state, so
// login starts from the provider itself instead of a dropdown. Connected
// providers keep an "Add Account" action for multi-account round-robin.
function connsForProvider(provider) {
  return _connList.filter(c => c.provider === provider);
}

function flowIconText(provider) {
  return flowLabel(provider).trim().slice(0, 2).toUpperCase();
}

function renderConnectionProviders() {
  const box = _('connProviderGroups');
  if (!box) return;
  if (!_connFlows.length) { box.innerHTML = ''; return; }

  const byProv = {};
  _connList.forEach(c => { const k = c.provider || ''; (byProv[k] = byProv[k] || []).push(c); });
  const connected = [], unconnected = [];
  _connFlows.slice().sort((a, b) => flowLabel(a).localeCompare(flowLabel(b))).forEach(p => {
    (byProv[p] && byProv[p].length ? connected : unconnected).push(p);
  });
  connected.sort((a, b) =>
    (byProv[b] || []).length - (byProv[a] || []).length || flowLabel(a).localeCompare(flowLabel(b)));

  const tile = (p) => {
    const list = byProv[p] || [];
    const active = list.filter(c => c.status === 'active').length;
    const reauth = list.length - active;
    const has = list.length > 0;
    let state;
    if (!has) {
      state = '<span class="conn-tile-state"><span class="status-dot disabled"></span>' + t('未连接') + '</span>';
    } else {
      state = '<span class="conn-tile-state"><span class="status-dot active"></span>' + active + ' ' + t('个账号');
      if (reauth) state += ' · <span class="warn-txt">' + reauth + ' ' + t('需重新登录') + '</span>';
      state += '</span>';
    }
    const action = '<button class="btn btn-sm' + (has ? '' : ' btn-primary') + '" onclick="startProviderLogin(\'' + escAttr(p) + '\', this)">' +
      t(has ? '添加账号' : '连接') + '</button>';
    return '<div class="conn-provider-tile' + (has ? ' connected' : '') + '">' +
      '<div class="conn-tile-main">' +
        '<div class="conn-tile-icon">' + esc(flowIconText(p)) + '</div>' +
        '<div class="conn-tile-body">' +
          '<div class="conn-tile-name">' + esc(flowLabel(p)) + '</div>' +
          '<div class="conn-tile-sub mono">' + esc(p) + '</div>' +
        '</div>' + state +
      '</div>' +
      '<div class="conn-tile-foot">' +
        '<span class="conn-tile-meta">' + (has ? list.length + ' ' + t('个连接') : t('设备码登录')) + '</span>' +
        '<div class="conn-tile-actions">' + action + '</div>' +
      '</div>' +
    '</div>';
  };
  const group = (title, list) => !list.length ? '' :
    '<div class="conn-provider-group">' +
      '<div class="conn-provider-group-title">' + title + ' <span class="conn-provider-count">' + list.length + '</span></div>' +
      '<div class="conn-provider-grid">' + list.map(tile).join('') + '</div>' +
    '</div>';
  box.innerHTML = group(t('已连接'), connected) + group(t('未连接'), unconnected);
}

// Token 到期倒计时：<1h 红（紧急）、<24h 琥珀、其余灰
function connExpiryHtml(c) {
  const ms = Number(c.expiresAt) || 0;
  if (!ms) return '';
  const left = ms - Date.now();
  if (left <= 0) return '<div class="conn-expiry bad">' + t('Token 已过期') + '</div>';
  const s = Math.floor(left / 1000);
  let text;
  if (s < 3600) text = t('剩 ') + Math.floor(s / 60) + 'm';
  else if (s < 86400) text = t('剩 ') + Math.floor(s / 3600) + 'h' + Math.floor((s % 3600) / 60) + 'm';
  else text = t('剩 ') + Math.floor(s / 86400) + 'd';
  const tone = s < 3600 ? 'bad' : (s < 86400 ? 'warn' : '');
  return '<div class="conn-expiry ' + tone + '" title="' + escAttr(new Date(ms).toLocaleString(LC())) + '">' + t('Token 到期 ') + text + '</div>';
}

// Wave-4: connection rows live on the merged Accounts page (账号与连接),
// grouped by provider service, each with its own usage + per-model stats.
function renderConnectionsList() {
  const box = _('connectionsList');
  if (!box) return;
  if (!_connList.length) { box.innerHTML = listEmptyState(ICON_LOGINS, t('暂无登录连接') + ' · ' + t('用设备码登录或导入已有 Token')); return; }
  const sorted = _connList.slice().sort((a, b) =>
    String(a.provider).localeCompare(String(b.provider)) || String(a.email || '').localeCompare(String(b.email || '')));
  let lastProv = null;
  let altIdx = 0;
  const rows = sorted.map(c => {
    let groupRow = '';
    if (c.provider !== lastProv) {
      lastProv = c.provider;
      groupRow = '<tr class="conn-group-row"><td colspan="6">' + esc(flowLabel(c.provider)) +
        ' <span class="mono grp-count">' + esc(c.provider) + '</span></td></tr>';
    }
    const stats = Object.values(c.modelStats || {}).sort((x, y) => y.totalTokens - x.totalTokens);
    const statRows = stats.length
      ? '<tr class="row-alt"><td colspan="6" class="pl-32 note-sm txt-strong">' + t('按模型统计') + ' · ' + stats.length + ' ' + t('个模型') + '</td></tr>' +
        stats.map(st =>
          '<tr class="row-alt">' +
            '<td class="mono pl-32">' + esc(st.modelId) +
              ' <span class="model-tag tag-xs' + (st.cost === 'free' ? ' free' : '') + '">' + esc(st.cost || 'paid') + '</span></td>' +
            '<td></td>' +
            '<td>' + formatNumber(st.usageCount) + '</td>' +
            '<td>' + formatTokenCount(st.promptTokens) + '</td>' +
            '<td>' + formatTokenCount(st.totalTokens) + '</td>' +
            '<td>' + formatTokenCount(st.cachedTokens) + '</td>' +
          '</tr>').join('')
      : '<tr class="row-alt"><td colspan="6" class="pl-32 hint-sm">' + t('暂无数据') + '</td></tr>';
    return groupRow + '<tr' + (altIdx++ % 2 ? ' class="alt"' : '') + '>' +
      '<td><button class="btn btn-sm btn-icon" onclick="toggleConnStats(\'' + escAttr(c.id) + '\', this)" title="' + t('展开') + '" aria-label="' + t('展开') + '">'+uiIcon('caretRight')+'</button></td>' +
      '<td class="hint-xs">' + esc(c.authType === 'device_code' ? t('设备码登录') : c.authType === 'import_token' ? t('导入 Token') : (c.authType || '')) + '</td>' +
      '<td class="note-sm">' + esc(c.email || (c.providerSpecificData && c.providerSpecificData.orgId ? 'org ' + c.providerSpecificData.orgId : '—')) + '</td>' +
      '<td>' + (c.status === 'active'
        ? '<span class="status active"><span class="status-dot active"></span>' + t('已登录') + '</span>'
        : '<span class="status disabled"><span class="status-dot disabled"></span>' + t('需重新登录') + '</span>') + connExpiryHtml(c) + '</td>' +
      '<td class="note-sm">' + (c.usageCount
        ? formatNumber(c.usageCount) + ' ' + t('请求') + ' · ' + formatTokenCount(c.totalTokens || 0) + ' token'
        : '<span class="mut">—</span>') + '</td>' +
      '<td><button class="btn btn-sm" onclick=\'testConnection("' + c.id + '", this)\'>' + t('测试') + '</button> ' +
          '<span class="conn-row-test-out"></span> ' +
          '<button class="btn btn-sm btn-danger" onclick=\'deleteConnection("' + c.id + '")\' title="' + t('删除') + '" aria-label="' + t('删除') + '">'+uiIcon('close')+'</button></td>' +
      '</tr>' +
      '<tr id="connStatsRow-' + escAttr(c.id) + '" style="display:none"><td colspan="6" class="stats-embed">' +
        '<table class="model-subtable">' +
          '<tr><th class="pl-32 th-left">' + t('模型') + '</th><th></th><th class="th-left">' + t('请求') + '</th><th class="th-left">' + t('输入') + '</th><th class="th-left">' + t('总量') + '</th><th class="th-left">' + t('缓存') + '</th></tr>' +
          statRows +
        '</table></td></tr>';
  }).join('');
  box.innerHTML = '<table class="action-table"><thead><tr><th></th><th>' + t('方式') + '</th><th>' + t('账号') + '</th><th>' + t('状态') + '</th><th>' + t('用量') + '</th><th>' + t('操作') + '</th></tr></thead><tbody>' + rows + '</tbody></table>';
}

function toggleConnStats(id, btn) {
  const row = _('connStatsRow-' + id);
  if (!row) return;
  const hidden = row.style.display === 'none';
  row.style.display = hidden ? '' : 'none';
  btn.innerHTML = hidden ? uiIcon('caretDown') : uiIcon('caretRight');
  btn.title = hidden ? t('收起') : t('展开');
}

function startProviderLogin(provider, btn) {
  if (!provider) return;
  const title = _('connLoginTitle');
  if (title) title.textContent = t('设备码登录') + ' · ' + flowLabel(provider);
  _('connLoginCode').textContent = '';
  const link = _('connLoginUrl');
  link.textContent = ''; link.removeAttribute('href');
  _('connLoginCode').parentElement.style.display = '';
  _('connLoginStatus').innerHTML = '<span class="loading"></span> ' + t('发起中...');
  _('connLoginModal').style.display = 'flex';
  beginDeviceLogin(btn, provider);
}

function closeDeviceLogin() {
  if (_loginPollTimer) { clearInterval(_loginPollTimer); _loginPollTimer = null; }
  _loginSessionId = '';
  const m = _('connLoginModal');
  if (m) m.style.display = 'none';
}

async function beginDeviceLogin(btn, provider) {
  provider = provider || 'kilocode';
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('发起中...'); }
  try {
    const d = await api('POST', '/connections/login/start', { provider: provider });
    const r = d.data || {};
    if (r.error) {
      _('connLoginStatus').innerHTML = renderTestResultHTML(false, r.error);
      return;
    }
    _loginSessionId = r.sessionId;
    _('connLoginCode').textContent = r.userCode || '';
    // Qoder identifies the device by nonce+verifier: the user just opens the
    // link and approves in the browser, there is no code to type.
    const browserOnly = provider === 'qoder' || provider === 'qoder-cn';
    const hint = _('connLoginHint');
    if (hint) {
      hint.textContent = browserOnly
        ? t('在浏览器中打开下面的链接并完成授权，然后保持本窗口打开。')
        : t('在浏览器中打开下面的地址并输入此代码，然后保持本窗口打开。');
    }
    const codeRow = _('connLoginCode') ? _('connLoginCode').parentElement : null;
    if (codeRow) codeRow.style.display = browserOnly ? 'none' : '';
    const a = _('connLoginUrl');
    a.href = r.authUrl || '#'; a.textContent = r.authUrl || '';
    _('connLoginStatus').innerHTML = '<span class="loading"></span> ' + t('等待授权...');
    if (_loginPollTimer) clearInterval(_loginPollTimer);
    _loginPollTimer = setInterval(pollDeviceLogin, 2000);
  } catch (e) {
    _('connLoginStatus').innerHTML = renderTestResultHTML(false, e.message);
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

async function pollDeviceLogin() {
  if (!_loginSessionId) return;
  try {
    const d = await api('GET', '/connections/login/status?sessionId=' + encodeURIComponent(_loginSessionId));
    const r = d.data || {};
    if (!r.done) return;
    clearInterval(_loginPollTimer); _loginPollTimer = null;
    if (r.success) {
      const email = r.email || (r.connection && r.connection.email) || '';
      toast(t('登录成功') + (email ? ': ' + email : ''), 'success');
      closeDeviceLogin();
      loadConnections();
    } else {
      _('connLoginStatus').innerHTML = renderTestResultHTML(false, r.error || t('登录失败'));
    }
  } catch (e) { /* 网络抖动下轮询继续 */ }
}

function copyDeviceCode() {
  const code = (_('connLoginCode') && _('connLoginCode').textContent) || '';
  if (!code) return;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(code); }
    else {
      const ta = document.createElement('textarea'); ta.value = code; document.body.appendChild(ta);
      ta.select(); document.execCommand('copy'); document.body.removeChild(ta);
    }
    toast(t('代码已复制'), 'info');
  } catch (e) {}
}

// ===== 导入 Token 弹窗：Provider 下拉（流程 / 已有连接 / Provider 列表）+ 手动 / 批量 =====
let _connImpProvFetch = null;
let _connBatchBusy = false;
let _connBatchGen = 0;

// 分组去重：同一个 key 只出现在第一个命中的分组里（case-sensitive）
function connImpProviderGroups() {
  const seen = new Set();
  const take = keys => {
    const out = [];
    keys.forEach(k => { if (!k || seen.has(k)) return; seen.add(k); out.push(k); });
    return out;
  };
  const byLabel = (a, b) => flowLabel(a).localeCompare(flowLabel(b));
  const flowKeys = take(_connFlows.filter(Boolean).slice().sort(byLabel));
  const connKeys = take(Array.from(new Set(_connList.map(c => c.provider).filter(Boolean))).sort(byLabel));
  const provKeys = take(Object.keys(_provCache)
    .map(id => (_provCache[id] && _provCache[id].name) || '')
    .filter(Boolean)
    .sort((a, b) => a.localeCompare(b)));
  return [
    { label: t('已登录流程'), keys: flowKeys, labelOf: flowLabel },
    { label: t('已有连接'), keys: connKeys, labelOf: flowLabel },
    { label: t('Provider 列表'), keys: provKeys, labelOf: k => k },
  ].filter(g => g.keys.length);
}

function connImpOptionsHTML(groups, selected) {
  let html = '';
  groups.forEach(g => {
    html += '<optgroup label="' + escAttr(g.label) + '">';
    g.keys.forEach(k => {
      html += '<option value="' + escAttr(k) + '"' + (k === selected ? ' selected' : '') + '>' +
        esc(g.labelOf(k)) + '</option>';
    });
    html += '</optgroup>';
  });
  html += '<option value="__custom__"' + (selected === '__custom__' ? ' selected' : '') + '>' + esc(t('自定义…')) + '</option>';
  return html;
}

// 每次打开弹窗都重建，避免下拉为空或停留在过期数据上
function connImportBuildSelect() {
  const selM = _('connImpProvider');
  if (!selM) return;
  const groups = connImpProviderGroups();
  const valid = v => v === '__custom__' || (!!v && groups.some(g => g.keys.indexOf(v) >= 0));
  const selB = _('connBatchProvider');
  const curM = selM.value || '';
  const mSel = valid(curM) ? curM : (groups.length ? '' : '__custom__');
  const curB = selB ? (selB.value || '') : '';
  const bSel = valid(curB) ? curB : mSel;
  selM.innerHTML = connImpOptionsHTML(groups, mSel);
  if (selB) selB.innerHTML = connImpOptionsHTML(groups, bSel);
  connImportProviderChange();
  connBatchProviderChange(true);
  const panel = _('connImpPanelBatch');
  if (panel && panel.classList.contains('active')) connBatchPreview();
}

// 手动 / 批量两个下拉保持同一个 provider key；自定义输入各自独立
function connImportProviderChange() {
  const sel = _('connImpProvider'), box = _('connImpProviderCustom');
  if (!sel || !box) return;
  const custom = sel.value === '__custom__';
  box.hidden = !custom;
  const other = _('connBatchProvider');
  if (other && other.value !== sel.value) {
    other.value = sel.value;
    const ob = _('connBatchProviderCustom');
    if (ob) ob.hidden = !custom;
  }
  if (custom && document.activeElement === sel) { try { box.focus(); } catch (e) {} }
}

function connBatchProviderChange(fromBuild) {
  const sel = _('connBatchProvider'), box = _('connBatchProviderCustom');
  if (!sel || !box) return;
  const custom = sel.value === '__custom__';
  box.hidden = !custom;
  const other = _('connImpProvider');
  if (other && other.value !== sel.value) {
    other.value = sel.value;
    const ob = _('connImpProviderCustom');
    if (ob) ob.hidden = !custom;
  }
  if (custom && !fromBuild && document.activeElement === sel) { try { box.focus(); } catch (e) {} }
  if (!fromBuild) connBatchPreview();
}

// 弹窗内的 tab 切换（不复用页面级 importTabs 的事件绑定）
function connImportTab(name) {
  const modal = _('connImportModal');
  if (!modal) return;
  name = name === 'batch' ? 'batch' : 'manual';
  modal.querySelectorAll('.conn-imp-tabs .tab').forEach(el => {
    const on = el.dataset.connTab === name;
    el.classList.toggle('active', on);
    el.setAttribute('aria-selected', on ? 'true' : 'false');
    el.tabIndex = on ? 0 : -1;
  });
  modal.querySelectorAll('[data-conn-panel]').forEach(el => {
    el.classList.toggle('active', el.dataset.connPanel === name);
  });
  const mf = _('connImpManualFoot'), bf = _('connImpBatchFoot');
  if (mf) mf.hidden = name !== 'manual';
  if (bf) bf.hidden = name !== 'batch';
  if (name === 'batch') { connBatchPreview(); return; }
  if (modal.style.display === 'flex') { try { _('connImpToken').focus(); } catch (e) {} }
}

function connImportTabKey(e) {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
  e.preventDefault();
  const to = e.key === 'ArrowLeft' ? 'manual' : 'batch';
  connImportTab(to);
  const next = _(to === 'manual' ? 'connImpTabManual' : 'connImpTabBatch');
  if (next) next.focus();
}

function connImportToggleToken(btn) {
  const inp = _('connImpToken');
  if (!inp) return;
  const show = inp.type === 'password';
  inp.type = show ? 'text' : 'password';
  if (btn) {
    btn.textContent = show ? t('隐藏') : t('显示');
    btn.setAttribute('aria-pressed', show ? 'true' : 'false');
  }
}

// 解析 select / 自定义输入得到最终 provider key
function connImpResolvedProvider(selId, customId) {
  const sel = _(selId), box = _(customId);
  const v = sel ? (sel.value || '') : '';
  if (v === '__custom__') return box ? (box.value || '').trim() : '';
  return v.trim();
}

function connImpShowResult(ok, msg) {
  const el = _('connImpResult');
  if (!el) return;
  el.innerHTML = '<div class="save-result ' + (ok ? 'ok' : 'err') + '">' +
    uiIcon(ok ? 'check' : 'close') + '<span>' + esc(msg) + '</span></div>';
}

async function showConnectionImport() {
  const modal = _('connImportModal');
  if (!modal) return;
  modal.style.display = 'flex';
  connImportTab('manual');
  connImportBuildSelect(); // 先用现有缓存填充，避免下拉空白
  // 流程 / 连接可能还没加载过（例如从 Provider 抽屉直接打开）
  if (!_connFlows.length) { await loadConnections(); }
  // Provider 下拉数据源：仅在缓存为空时补一次请求
  if (!Object.keys(_provCache).length) {
    if (!_connImpProvFetch) {
      _connImpProvFetch = api('GET', '/providers')
        .then(d => { ((d.data && d.data.providers) || []).forEach(p => { if (p && p.id) _provCache[p.id] = p; }); })
        .catch(() => {})
        .then(() => { _connImpProvFetch = null; });
    }
    await _connImpProvFetch;
  }
  connImportBuildSelect();
}

function hideConnectionImport() {
  const modal = _('connImportModal');
  if (modal) modal.style.display = 'none';
  _connBatchBusy = false;
  _connBatchGen++;
  ['connImpToken', 'connImpEmail', 'connImpOrg', 'connBatchInput', 'connImpProviderCustom', 'connBatchProviderCustom']
    .forEach(id => { const el = _(id); if (el) el.value = ''; });
  ['connImpResult', 'connBatchResult', 'connBatchPreview', 'connBatchFileName']
    .forEach(id => { const el = _(id); if (el) el.innerHTML = ''; });
  const selM = _('connImpProvider'), selB = _('connBatchProvider');
  if (selM) selM.value = '';
  if (selB) selB.value = '';
  const file = _('connBatchFile'); if (file) file.value = '';
  const tok = _('connImpToken');
  if (tok) tok.type = 'password';
  const reveal = _('connImpReveal');
  if (reveal) { reveal.textContent = t('显示'); reveal.setAttribute('aria-pressed', 'false'); }
  connImportTab('manual');
}

async function submitConnectionImport(btn) {
  const provider = connImpResolvedProvider('connImpProvider', 'connImpProviderCustom');
  const token = ((_('connImpToken') || {}).value || '').trim();
  if (!provider) {
    connImpShowResult(false, t('请选择服务'));
    toast(t('请选择服务'), 'error');
    try { _('connImpProvider').focus(); } catch (e) {}
    return;
  }
  if (!token) {
    connImpShowResult(false, t('请填写 Access Token'));
    toast(t('请填写 Access Token'), 'error');
    try { _('connImpToken').focus(); } catch (e) {}
    return;
  }
  const body = {
    provider: provider,
    accessToken: token,
    email: ((_('connImpEmail') || {}).value || '').trim(),
    orgId: ((_('connImpOrg') || {}).value || '').trim(),
  };
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    await api('POST', '/connections/import', body);
    connImpShowResult(true, t('连接已保存') + ' · ' + provider);
    toast(t('连接已保存'), 'success');
    _('connImpToken').value = '';
    loadConnections();
  } catch (e) {
    connImpShowResult(false, t('保存失败: ') + e.message);
    toast(t('保存失败: ') + e.message, 'error');
  }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}

// ===== 批量导入：宽松解析 → 预览 → 逐项结果 =====
function connImpNormalizeItem(raw, fallback) {
  if (raw == null) return { provider: fallback, accessToken: '', email: '', orgId: '' };
  if (typeof raw === 'string' || typeof raw === 'number') {
    return { provider: fallback, accessToken: String(raw).trim(), email: '', orgId: '' };
  }
  if (typeof raw !== 'object') return { provider: fallback, accessToken: '', email: '', orgId: '' };
  return {
    provider: String(raw.provider || '').trim() || fallback,
    accessToken: String(raw.accessToken || raw.access_token || raw.token || '').trim(),
    email: String(raw.email || '').trim(),
    orgId: String(raw.orgId || raw.org_id || '').trim(),
  };
}

// 纯文本：每行一条；`provider,token[,email]`（逗号或制表符分隔），空行与 # 注释跳过
function connImpParseText(text, fallback) {
  const out = [];
  String(text || '').split(/\r?\n/).forEach(line => {
    const s = line.trim();
    if (!s || s.charAt(0) === '#') return;
    const parts = (s.indexOf('\t') >= 0 ? s.split('\t') : s.split(',')).map(x => x.trim());
    if (parts.length <= 1) out.push({ provider: fallback, accessToken: parts[0] || '', email: '', orgId: '' });
    else out.push({ provider: parts[0], accessToken: parts[1], email: parts[2] || '', orgId: '' });
  });
  return out;
}

// 解析顺序：JSON 数组 → {items|tokens} → 单对象 → 纯文本
function parseConnBatch(text, fallback) {
  const raw = String(text || '').trim();
  const result = { items: [], total: 0, duplicates: 0, invalid: [] };
  if (!raw) return result;
  let list;
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) list = parsed;
    else if (parsed && typeof parsed === 'object') {
      if (Array.isArray(parsed.items)) list = parsed.items;
      else if (Array.isArray(parsed.tokens)) list = parsed.tokens;
      else list = [parsed];
    } else if (typeof parsed === 'string' || typeof parsed === 'number') list = [parsed];
    else list = [];
  } catch (e) {
    list = connImpParseText(raw, fallback);
  }
  const seen = new Set();
  list.forEach((entry, i) => {
    const item = connImpNormalizeItem(entry, fallback);
    result.total++;
    if (!item.accessToken) {
      result.invalid.push({ row: i + 1, provider: item.provider, reason: t('缺少 Token') });
      return;
    }
    if (!item.provider) {
      result.invalid.push({ row: i + 1, provider: '', reason: t('缺少服务') });
      return;
    }
    const key = item.provider + '\n' + item.accessToken;
    if (seen.has(key)) { result.duplicates++; return; }
    seen.add(key);
    result.items.push(item);
  });
  return result;
}

function connBatchPreview() {
  const box = _('connBatchPreview');
  if (!box) return;
  const ta = _('connBatchInput');
  const fallback = connImpResolvedProvider('connBatchProvider', 'connBatchProviderCustom');
  const parsed = parseConnBatch(ta ? ta.value : '', fallback);
  box.classList.remove('err');
  if (!parsed.total) {
    box.innerHTML = esc(t('粘贴或选择文件后自动解析'));
    return;
  }
  let html = t('解析到 ') + parsed.total + t(' · 去重后 ') + parsed.items.length;
  if (parsed.duplicates) html += t(' · 跳过重复 ') + parsed.duplicates;
  if (parsed.invalid.length) {
    html += t(' · 无效 ') + parsed.invalid.length;
    html += '<span class="conn-imp-invalid">' +
      parsed.invalid.slice(0, 4).map(x => esc(t('行 ') + x.row + ' · ' + x.reason)).join(' · ') +
      (parsed.invalid.length > 4 ? ' …' : '') + '</span>';
  }
  box.innerHTML = html;
}

function connBatchPickFile() {
  const f = _('connBatchFile');
  if (f) f.click();
}

async function connBatchFileChosen(event) {
  const input = event && event.target;
  const file = input && input.files && input.files[0];
  if (!file) return;
  const box = _('connBatchPreview');
  const nameEl = _('connBatchFileName');
  if (file.size > 1024 * 1024) {
    if (input) input.value = '';
    if (nameEl) nameEl.textContent = '';
    if (box) { box.classList.add('err'); box.innerHTML = esc(t('文件不能超过 1 MB')); }
    toast(t('文件不能超过 1 MB'), 'error');
    return;
  }
  try {
    const text = await file.text();
    const ta = _('connBatchInput');
    if (ta) ta.value = text; // 只回填，交给用户确认后再导入
    if (nameEl) nameEl.textContent = t('已选择 ') + file.name;
    connBatchPreview();
  } catch (e) {
    if (box) { box.classList.add('err'); box.innerHTML = esc(t('读取文件失败: ') + e.message); }
  } finally {
    if (input) input.value = ''; // 允许重复选择同一个文件
  }
}

function connBatchRenderResult(r) {
  const box = _('connBatchResult');
  if (!box) return;
  const imported = Number(r.imported) || 0;
  const failed = Number(r.failed) || 0;
  const fails = (r.results || []).filter(x => x && x.ok === false);
  let html = '<div class="conn-imp-sum ' + (failed ? (imported ? 'warn' : 'bad') : 'ok') + '">' +
    uiIcon(failed ? 'close' : 'check') +
    '<span>' + t('成功 ') + imported + t(' · 失败 ') + failed + '</span></div>';
  if (fails.length) {
    html += '<ul class="conn-imp-fails">' + fails.map(x => {
      const n = Number(x.index);
      return '<li><span class="mono conn-imp-idx">#' + (Number.isFinite(n) ? n + 1 : '?') + '</span>' +
        (x.provider ? '<span class="mono">' + esc(x.provider) + '</span>' : '') +
        '<span class="conn-imp-err">' + esc(x.error || '') + '</span></li>';
    }).join('') + '</ul>';
  }
  box.innerHTML = html;
}

async function submitConnBatch(btn) {
  if (_connBatchBusy) return; // 防重复提交
  const ta = _('connBatchInput');
  const resBox = _('connBatchResult');
  const raw = ta ? ta.value.trim() : '';
  if (!raw) {
    if (resBox) resBox.innerHTML = '<div class="save-result err">' + uiIcon('close') +
      '<span>' + esc(t('请先粘贴数据或选择文件')) + '</span></div>';
    toast(t('请先粘贴数据或选择文件'), 'error');
    return;
  }
  const fallback = connImpResolvedProvider('connBatchProvider', 'connBatchProviderCustom');
  const parsed = parseConnBatch(raw, fallback);
  if (!parsed.items.length) {
    connBatchPreview();
    if (resBox) resBox.innerHTML = '<div class="save-result err">' + uiIcon('close') +
      '<span>' + esc(t('没有可导入的条目：请检查数据格式')) + '</span></div>';
    toast(t('没有可导入的条目：请检查数据格式'), 'error');
    return;
  }
  _connBatchBusy = true;
  const gen = _connBatchGen;
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('导入中...'); }
  if (resBox) resBox.innerHTML = '';
  try {
    const d = await api('POST', '/connections/import-batch', { items: parsed.items });
    if (gen !== _connBatchGen) return; // 弹窗已关闭，丢弃迟到的响应
    const r = (d && d.data) || {};
    const imported = Number(r.imported) || 0;
    const failed = Number(r.failed) || 0;
    connBatchRenderResult(r);
    toast(t('导入完成') + ' · ' + t('成功 ') + imported + t(' · 失败 ') + failed,
      failed ? (imported ? 'info' : 'error') : 'success');
    if (imported > 0) loadConnections();
    if (!failed) {
      if (ta) ta.value = '';
      const nameEl = _('connBatchFileName'); if (nameEl) nameEl.textContent = '';
      const file = _('connBatchFile'); if (file) file.value = '';
      connBatchPreview();
    }
  } catch (e) {
    if (gen === _connBatchGen && resBox) resBox.innerHTML = '<div class="save-result err">' + uiIcon('close') +
      '<span>' + esc(t('导入失败: ') + e.message) + '</span></div>';
    if (gen === _connBatchGen) toast(t('导入失败: ') + e.message, 'error');
  }
  finally {
    _connBatchBusy = false;
    if (btn) { btn.disabled = false; btn.innerHTML = orig; }
  }
}

async function testConnection(id, cell) {
  const out = cell && cell.closest ? cell.closest('td').querySelector('.conn-row-test-out') : null;
  const orig = cell ? cell.innerHTML : '';
  if (cell) { cell.disabled = true; cell.innerHTML = '<span class="loading"></span> ' + t('测试中...'); }
  if (out) out.innerHTML = '';
  try {
    const d = await api('POST', '/connections/test', { id });
    const r = d.data || {};
    if (out) out.innerHTML = renderTestResultHTML(r.ok, r.ok ? (t('可用') + ' · HTTP ' + r.status) : (r.error || t('失败')));
    loadConnections();
  } catch (e) {
    if (out) out.innerHTML = renderTestResultHTML(false, friendlyTestError(e));
  } finally {
    if (cell) { cell.disabled = false; cell.innerHTML = orig; }
  }
}

async function deleteConnection(id) {
  if (!(await showConfirm(t('确定删除该连接？绑定它的 Provider 将不再注入 token。')))) return;
  try {
    await api('POST', '/connections/delete', { id });
    toast(t('已删除'), 'success');
    loadConnections();
  } catch (e) { toast(t('删除失败: ') + e.message, 'error'); }
}

// Wave-4: connection pool picker in the provider drawer (checkbox list grouped
// by provider service). Replaces the old single-select provConnection dropdown.
let _provConnSel = [];

function initConnPick(ids) { _provConnSel = (ids || []).slice(); renderConnPick(); }

function toggleConnPick(id, on) {
  const i = _provConnSel.indexOf(id);
  if (on && i < 0) _provConnSel.push(id);
  if (!on && i >= 0) _provConnSel.splice(i, 1);
  const cnt = _('provConnCount');
  if (cnt) cnt.textContent = ' · ' + (_provConnSel.length ? _provConnSel.length + t('已选') : t('未选择'));
  refreshSaveBar(); // 委托监听先于本函数执行，需在此补一次脏检查
}

function renderConnPick() {
  const box = _('provConnPick');
  if (!box) return;
  const cnt = _('provConnCount');
  if (cnt) cnt.textContent = ' · ' + (_provConnSel.length ? _provConnSel.length + t('已选') : t('未选择'));
  if (!_connList.length) {
    box.innerHTML = '<span class="conn-pick-empty">' + t('暂无登录连接') + ' · <a href="javascript:void(0)" onclick="showConnectionImport()" class="lnk-a">' + t('导入 Token') + '</a></span>';
    return;
  }
  const byProv = {};
  _connList.forEach(c => { (byProv[c.provider] = byProv[c.provider] || []).push(c); });
  box.innerHTML = Object.keys(byProv).sort().map(prov =>
    '<div class="conn-pick-group">' + esc(flowLabel(prov)) + '</div>' +
    byProv[prov].map(c => {
      const on = _provConnSel.includes(c.id);
      const who = c.email || (c.providerSpecificData && c.providerSpecificData.orgId ? 'org ' + c.providerSpecificData.orgId : c.id);
      return '<label class="conn-pick-item' + (on ? ' on' : '') + '">' +
        '<input type="checkbox"' + (on ? ' checked' : '') + ' onchange="toggleConnPick(\'' + escAttr(c.id) + '\', this.checked)"> ' +
        '<span class="mono note-sm">' + esc(who) + '</span>' +
        (c.status !== 'active' ? ' <span class="conn-pick-warn">' + t('需重新登录') + '</span>' : '') +
        (c.usageCount ? ' <span class="conn-pick-usage">' + formatNumber(c.usageCount) + ' ' + t('请求') + '</span>' : '') +
        '</label>';
    }).join('')).join('');
}

async function saveHeaders(btn) {
  const tbody = _('headersTableBody');
  const rows = tbody.querySelectorAll('tr');
  const headers = {};
  let hasEmpty = false;
  rows.forEach(tr => {
    const keyInput = tr.querySelector('.header-key');
    const valInput = tr.querySelector('.header-val');
    if (keyInput && valInput) {
      const k = keyInput.value.trim();
      const v = valInput.value.trim();
      if (k) { headers[k] = v; }
      else if (v) { hasEmpty = true; }
    }
  });
  if (hasEmpty) { toast(t('存在有值无键的行，已忽略'), 'info'); }
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('保存中...'); }
  try {
    const d = await api('POST', '/config/update', { headers });
    toast(t('请求头已保存'), 'success');
    showSaveResult('headerSaveResult', true, t('已保存 ') + Object.keys(d.data.headers).length + t(' 个请求头'));
    loadConfig();
  } catch (e) { toast(t('保存失败: ') + e.message, 'error'); }
  finally { if (btn) { btn.disabled = false; btn.innerHTML = orig; } }
}


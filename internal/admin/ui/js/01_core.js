// ===== core: language, escaping, formatting, icons, toasts =====
let LANG = 'en';
const LC = () => LANG === 'zh' ? 'zh-CN' : 'en-US';
function detectLang(){
  try { const m = document.cookie.match(/cline_admin_lang=(zh|en)/); if (m) return m[1]; } catch(e){}
  try { const l = localStorage.getItem('cline_admin_lang'); if (l==='en'||l==='zh') return l; } catch(e){}
  return 'en';
}
function t(s){ if (LANG==='en' && s && I18N[s]) return I18N[s]; return s; }
// Server-built messages (Go) that embed Chinese hints and dynamic values cannot
// be exact dictionary keys. Translate their known fragments in English mode;
// anything unmatched passes through untouched.
const SERVER_MSG_RULES = [
  [/（opencode\.ai (?:当前)?网络不可达，可在管理页「(?:上游服务 → opencode|Providers → OpenCode Zen →) 出口代理」配置代理(?:后重试)?）/g,
   ' (opencode.ai is unreachable from this network. Configure a proxy under Providers → OpenCode Zen → egress proxy, then retry.)'],
  [/（未找到已注册的 opencode 会话：免费层只服务上游已登记的 session；请在管理页填入 sessionIds，或在本机用 opencode\/wu 正常对话一次以生成）/g,
   ' (No registered opencode session found. The free tier only serves sessions registered upstream. Enter sessionIds in the admin page, or chat once with opencode/wu on this machine to create one.)'],
  [/（opencode 免费层出口 IP 限流 ~([^，）]+)，切换代理节点后可立即重试）/g,
   ' (The opencode free tier is rate-limiting this egress IP for about $1. Switch proxy nodes to retry immediately.)'],
  [/模型归属 (.+?) 同步链，不跨源改层级/g, 'Model belongs to the $1 sync chain; its tier is not changed across sources'],
  [/用户手动设置过层级/g, 'Tier was set manually by the user'],
  [/管理页「导入账号」→ OAuth 登录/g, 'Admin page: Import Account → OAuth login'],
];
function tx(s) {
  if (LANG !== 'en' || typeof s !== 'string' || !/[\u4e00-\u9fff]/.test(s)) return s;
  let out = s;
  for (const [re, rep] of SERVER_MSG_RULES) out = out.replace(re, rep);
  return out;
}
// Only retain unambiguous English → Chinese fallbacks. Static DOM nodes keep
// their own source key below, so collisions such as "Providers" cannot restore
// the wrong Chinese label after an English session.
const I18N_REV = Object.keys(I18N).reduce((acc, k) => {
  const value = I18N[k];
  if (!Object.prototype.hasOwnProperty.call(acc, value)) acc[value] = k;
  else acc[value] = null;
  return acc;
}, {});
// Idempotent text lookup, used by applyLang() for text nodes, titles,
// placeholders and aria-labels. A DOM string may already be a Chinese source
// key, an unambiguous English value, or unrestricted rendered content (API
// data, user input, an English value that several keys share). Only the first
// two forms map back to a key; everything else passes through untouched.
// applyLang() used to cache each node's previous text in a WeakMap; a second
// applyLang() call landing on the same node (tab switches, async renders) could
// leave that second snapshot in place, which surfaced as stray glyphs inside
// Chinese headings. A stateless lookup cannot drift that way.
function i18nSourceKey(value) {
  if (!value) return value;
  if (Object.prototype.hasOwnProperty.call(I18N, value)) return value;
  if (Object.prototype.hasOwnProperty.call(I18N_REV, value) && I18N_REV[value]) return I18N_REV[value];
  return value;
}
// renderedText returns what `source` should read as in the current language.
function renderedText(source) {
  if (LANG !== 'en') return source;
  return Object.prototype.hasOwnProperty.call(I18N, source) ? I18N[source] : source;
}
function applyLang(){
  if (LANG === 'en') {
    document.title = 'RouteNest Admin';
  } else {
    document.title = 'RouteNest 管理面板';
  }
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const nodes = [];
  while (walker.nextNode()) nodes.push(walker.currentNode);
  for (const n of nodes){
    const parentTag = n.parentElement && n.parentElement.tagName;
    if (parentTag === 'SCRIPT' || parentTag === 'STYLE' || parentTag === 'NOSCRIPT') continue;
    const txt = n.nodeValue;
    if (!txt) continue;
    const trimmed = txt.trim();
    if (!trimmed) continue;
    const rendered = renderedText(i18nSourceKey(trimmed));
    // Preserve the node's own surrounding whitespace (indentation inside
    // inline text) and only swap the meaningful span.
    if (trimmed !== rendered) n.nodeValue = txt.replace(trimmed, rendered);
  }
  // Wave-5: placeholder/aria-label carry user-visible copy too, so they follow
  // the same exact-match dictionary as text nodes and [title].
  document.querySelectorAll('[title],[placeholder],[aria-label]').forEach(el=>{
    for (const attr of ['title','placeholder','aria-label']) {
      const current = (el.getAttribute(attr) || '').trim();
      if (!current) continue;
      const rendered = renderedText(i18nSourceKey(current));
      if (current !== rendered) el.setAttribute(attr, rendered);
    }
  });
  document.querySelectorAll('[data-lang-opt]').forEach(btn => {
    const active = btn.dataset.langOpt === LANG;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-pressed', active ? 'true' : 'false');
  });
  if (typeof syncNavCollapseBtn === 'function') syncNavCollapseBtn(); // 侧栏按钮 title 随语言更新
  if (typeof updateShellContext === 'function' && typeof _activeTabName !== 'undefined') updateShellContext(_activeTabName || getActiveTab());
  if (typeof setGatewayReachability === 'function' && typeof _gatewayReachable !== 'undefined') setGatewayReachability(_gatewayReachable);
  if (typeof updateAdminAuthPageTitle === 'function') updateAdminAuthPageTitle();
}
function setLang(l){
  LANG = l;
  try { document.cookie = 'cline_admin_lang='+l+';path=/admin;max-age=31536000'; } catch(e){}
  try { localStorage.setItem('cline_admin_lang', l); } catch(e){}
  document.documentElement.lang = (l==='en')?'en':'zh-CN';
  // 即时切换：静态文本 + title 属性翻译 + 重新渲染动态数据，不整页刷新
  applyLang();
  if (typeof _adminPrincipal !== 'undefined' && _adminPrincipal) {
    loadStats();
    loadAccounts();
    loadRequestLogs(true);
    if (_('modelsList')) loadModels();
    if (_('settingDefModel')) loadConfig();
    if (typeof renderGlobal === 'function' && typeof _globalData !== 'undefined' && _globalData) renderGlobal();
    renderAdminPrincipal(_adminPrincipal);
    if (typeof _activeTabName !== 'undefined' && _activeTabName === 'security' && adminHasPermission('security.write')) {
      loadAdminSessions(); loadAdminUsers(); if (typeof loadTrustedDevices === 'function') loadTrustedDevices(); loadSSOProviders();
    }
  }
  // No toast: the pressed segment in the language control already confirms the switch.
}
LANG = detectLang();
document.documentElement.lang = (LANG==='zh')?'zh-CN':'en';
// ===== /i18n =====
const API = '/admin/api';

const _ = id => document.getElementById(id);
const esc = s => { const d=document.createElement('div'); d.textContent=s||''; return d.innerHTML; };
// 属性上下文转义：esc 不处理引号，注入到 onclick / value 等属性时需额外转义引号
const escAttr = s => esc(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
// A value passed as a JS string argument inside a double-quoted inline event-handler attribute.
// JSON quoting makes it a valid JS literal; escAttr then protects the HTML attribute around it.
const jsArg = value => escAttr(JSON.stringify(String(value == null ? '' : value)));

// §9.3 tier-signal provenance labels, shared by the provider import preview
// and the per-row badges on the Models page.
const _tierSignals = {
  'pricing-zero': '定价为 0', 'flag-free': '上游 free 标志', 'suffix-free': 'id 后缀 free', 'name-token-free': 'id 含 free 段', 'allowlist': '免费标签/白名单',
  'provider-free': '整源免费', 'display-name-free': '模型名含 free', 'pricing-paid': '上游定价非零', 'paid-default': '无免费信号 → 付费',
};

// Merge ids into a picker's checked-order array, skipping ids already present
// (selection order equals attempt priority).
function pickerMergeIds(order, ids) {
  for (const id of ids) if (order.indexOf(id) === -1) order.push(id);
}

const ACTIVE_TAB_KEY = 'cline_admin_tab';

function getActiveTab(){
  try { const h = location.hash.replace(/^#/,''); if (h) return h; } catch(e){}
  try { const v = sessionStorage.getItem(ACTIVE_TAB_KEY); if (v) return v; } catch(e){}
  return 'dashboard';
}
function setActiveTab(name){
  try { sessionStorage.setItem(ACTIVE_TAB_KEY, name); } catch(e){}
}
const formatNumber = n => new Intl.NumberFormat(LC()).format(n || 0);
const formatTokenCount = n => {
  const value = Number(n) || 0;
  if (value < 1000) return String(value);
  const units = [['B', 1e9], ['M', 1e6], ['K', 1e3]];
  for (const [unit, size] of units) {
    if (value >= size) return (value / size).toFixed(1).replace(/\.0$/, '') + unit;
  }
  return String(value);
};

// 行内 SVG 图标助手：与页面表头图标（ui/html）同风格（stroke=currentColor / 2px / 圆角端点）
const UI_ICONS = {
  bolt: '<path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z"/>',
  reset: '<polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>',
  close: '<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>',
  check: '<polyline points="20 6 9 17 4 12"/>',
  chart: '<rect x="3" y="12" width="4" height="8" rx="1"/><rect x="10" y="8" width="4" height="12" rx="1"/><rect x="17" y="4" width="4" height="16" rx="1"/>',
  caretRight: '<polyline points="9 6 15 12 9 18"/>',
  caretDown: '<polyline points="6 9 12 15 18 9"/>',
  hourglass: '<path d="M5 22h14"/><path d="M5 2h14"/><path d="M17 22v-4.172a2 2 0 0 0-.586-1.414L12 12l-4.414-4.414A2 2 0 0 1 7 6.172V2"/><path d="M7 2v4.172a2 2 0 0 0 .586 1.414L12 12l4.414 4.414A2 2 0 0 1 17 17.828V22"/>',
  arrowDown: '<line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/>',
  copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  edit: '<path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z"/>',
  pause: '<rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/>',
  play: '<polygon points="5 3 19 12 5 21 5 3"/>'
};
function uiIcon(name, cls) {
  const d = UI_ICONS[name] || '';
  return '<svg class="ui-ic' + (cls ? ' ' + cls : '') + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + d + '</svg>';
}

// Toast：堆叠队列（最多 3 条，错峰自动消失；软底色+状态色描边）
// 兼容旧签名 toast(msg, type)，type 缺省按 info 处理
let _toastSeq = 0;
function toast(msg, tone) {
  const stack = _('toastStack');
  if (!stack) return;
  if (!tone) tone = 'info';
  const item = document.createElement('div');
  item.className = 'toast ' + tone;
  item.innerHTML = uiIcon(tone === 'success' ? 'check' : (tone === 'error' ? 'close' : 'bolt')) + '<span class="toast-msg"></span>';
  item.querySelector('.toast-msg').textContent = tx(msg);
  stack.appendChild(item);
  while (stack.children.length > 3) stack.removeChild(stack.firstChild);
  requestAnimationFrame(() => item.classList.add('show'));
  setTimeout(() => {
    item.classList.remove('show');
    setTimeout(() => { if (item.parentNode) item.parentNode.removeChild(item); }, 300);
  }, 3500 + (_toastSeq++ % 3) * 400);
}

// 顶部居中的通知用于短时反馈；错误是用户必须处理的状态，改用常驻在右下角
// 的提醒里，直到用户关闭（Toast 3 秒后消失，错误信息不应跟着消失）。
function toastError(msg) {
  const host = _('toastStack');
  if (!host) { toast(msg, 'error'); return; }
  let box = _('stickyAlerts');
  if (!box) {
    box = document.createElement('div');
    box.id = 'stickyAlerts';
    box.className = 'sticky-alerts';
    box.setAttribute('role', 'alert');
    host.parentNode.insertBefore(box, host);
  }
  const item = document.createElement('div');
  item.className = 'sticky-alert';
  item.innerHTML = uiIcon('bolt', 'sticky-alert-icon') + '<span class="sticky-alert-msg"></span>' +
    '<button type="button" class="sticky-alert-close" aria-label="' + escAttr(t('关闭')) + '">' + uiIcon('close') + '</button>';
  item.querySelector('.sticky-alert-msg').textContent = tx(String(msg || ''));
  const close = item.querySelector('.sticky-alert-close');
  close.title = t('关闭');
  close.addEventListener('click', () => item.remove());
  box.appendChild(item);
  while (box.children.length > 3) box.removeChild(box.firstChild);
}

// 行内保存反馈统一：「✓ 已保存 / ✗ 失败」，4s 后自动清除
function showSaveResult(id, ok, msg) {
  const el = _(id);
  if (!el) return;
  el.innerHTML = '<span class="save-result ' + (ok ? 'ok' : 'err') + '">' +
    uiIcon(ok ? 'check' : 'close') + esc(msg || (ok ? t('已保存') : t('保存失败'))) + '</span>';
  clearTimeout(el._saveTimer);
  el._saveTimer = setTimeout(() => { el.innerHTML = ''; }, 4000);
}

// 格式化冷却倒计时：返回紧凑的 "3.2h" 格式
function formatCooldown(isoTime) {
  const until = new Date(isoTime);
  const diff = until - new Date();
  if (diff <= 0) return t('即将恢复');
  const hours = diff / 3600000;
  if (hours < 1) return Math.ceil(diff / 60000) + 'm';
  return hours.toFixed(1) + 'h';
}

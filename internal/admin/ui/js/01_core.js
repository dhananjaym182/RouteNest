// ===== core: language, escaping, formatting, icons, toasts =====
let LANG = 'zh';
const LC = () => LANG === 'en' ? 'en-US' : 'zh-CN';
function detectLang(){
  try { const m = document.cookie.match(/cline_admin_lang=(zh|en)/); if (m) return m[1]; } catch(e){}
  try { const l = localStorage.getItem('cline_admin_lang'); if (l==='en'||l==='zh') return l; } catch(e){}
  return (navigator.language||'').toLowerCase().startsWith('zh') ? 'zh' : 'en';
}
function t(s){ if (LANG==='en' && s && I18N[s]) return I18N[s]; return s; }
// Only retain unambiguous English → Chinese fallbacks. Static DOM nodes keep
// their own source key below, so collisions such as "Providers" cannot restore
// the wrong Chinese label after an English session.
const I18N_REV = Object.keys(I18N).reduce((acc, k) => {
  const value = I18N[k];
  if (!Object.prototype.hasOwnProperty.call(acc, value)) acc[value] = k;
  else acc[value] = null;
  return acc;
}, {});
const _i18nTextSources = new WeakMap();
const _i18nAttrSources = new WeakMap();
function i18nSourceKey(value) {
  if (!value) return value;
  if (Object.prototype.hasOwnProperty.call(I18N, value)) return value;
  return I18N_REV[value] || value;
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
    let source = _i18nTextSources.get(n);
    if (!source || trimmed !== source.rendered) {
      source = { raw: txt, key: i18nSourceKey(trimmed), rendered: trimmed };
      _i18nTextSources.set(n, source);
    }
    const rendered = LANG === 'en' ? (I18N[source.key] || source.key) : source.key;
    if (trimmed !== rendered) n.nodeValue = source.raw.replace(source.raw.trim(), rendered);
    source.rendered = rendered;
  }
  // Wave-5: placeholder/aria-label carry user-visible copy too, so they follow
  // the same exact-match dictionary as text nodes and [title].
  document.querySelectorAll('[title],[placeholder],[aria-label]').forEach(el=>{
    let sources = _i18nAttrSources.get(el);
    if (!sources) { sources = {}; _i18nAttrSources.set(el, sources); }
    for (const attr of ['title','placeholder','aria-label']) {
      const current = (el.getAttribute(attr) || '').trim();
      if (!current) continue;
      let source = sources[attr];
      if (!source || current !== source.rendered) {
        source = { key: i18nSourceKey(current), rendered: current };
        sources[attr] = source;
      }
      const rendered = LANG === 'en' ? (I18N[source.key] || source.key) : source.key;
      if (current !== rendered) el.setAttribute(attr, rendered);
      source.rendered = rendered;
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
}
function setLang(l){
  LANG = l;
  try { document.cookie = 'cline_admin_lang='+l+';path=/admin;max-age=31536000'; } catch(e){}
  try { localStorage.setItem('cline_admin_lang', l); } catch(e){}
  document.documentElement.lang = (l==='en')?'en':'zh-CN';
  // 即时切换：静态文本 + title 属性翻译 + 重新渲染动态数据，不整页刷新
  applyLang();
  loadStats();
  loadAccounts();
  loadRequestLogs(true);
  if (_('modelsList')) loadModels();
  if (_('settingDefModel')) loadConfig();
  if (typeof renderGlobal === 'function' && typeof _globalData !== 'undefined' && _globalData) renderGlobal();
  toast(l === 'en' ? 'Language switched to English' : '语言已切换为中文', 'success');
}
LANG = detectLang();
document.documentElement.lang = (LANG==='en')?'en':'zh-CN';
// ===== /i18n =====
const API = '/admin/api';

const _ = id => document.getElementById(id);
const esc = s => { const d=document.createElement('div'); d.textContent=s||''; return d.innerHTML; };
// 属性上下文转义：esc 不处理引号，注入到 onclick / value 等属性时需额外转义引号
const escAttr = s => esc(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');

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
  item.querySelector('.toast-msg').textContent = msg;
  stack.appendChild(item);
  while (stack.children.length > 3) stack.removeChild(stack.firstChild);
  requestAnimationFrame(() => item.classList.add('show'));
  setTimeout(() => {
    item.classList.remove('show');
    setTimeout(() => { if (item.parentNode) item.parentNode.removeChild(item); }, 300);
  }, 3500 + (_toastSeq++ % 3) * 400);
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

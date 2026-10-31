// ========== API 请求 ==========
async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const res = await fetch(API + path, opts);
  if (res.status === 401) {
    showLogin();
    throw new Error(t('需要登录'));
  }
  const data = await res.json();
  if (!data.success && data.error) throw new Error(data.error);
  return data;
}

// ========== 后台登录 ==========
function showLogin() {
  const ov = _('loginOverlay');
  if (ov && ov.style.display !== 'flex') {
    ov.style.display = 'flex';
    setTimeout(() => _('loginPassword').focus(), 50);
  }
}

async function submitLogin() {
  const pwd = _('loginPassword').value;
  if (!pwd) return;
  _('loginError').textContent = '';
  try {
    const res = await fetch(API + '/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pwd })
    });
    const data = await res.json();
    if (res.ok && data.success) {
      location.reload();
    } else {
      _('loginError').textContent = data.error || t('登录失败');
      _('loginPassword').value = '';
    }
  } catch (e) { _('loginError').textContent = t('网络错误，请重试'); }
}

// 用系统默认浏览器打开外部链接（桌面 WebView 内导航不会跳外部浏览器，需走后端）
async function openExternal(url) {
  try {
    await api('GET', '/open-external?url=' + encodeURIComponent(url));
    toast(t('已在系统浏览器中打开'), 'success');
  } catch (e) { toast(t('打开失败: ') + e.message, 'error'); }
}

// ========== 主题（浅色 / 深色） ==========
const THEME_KEY = 'cline_admin_theme';
function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  document.querySelectorAll('#themeSwitch button').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.themeOpt === theme);
  });
}
function setTheme(theme) {
  if (theme !== 'light' && theme !== 'dark') return;
  try { localStorage.setItem(THEME_KEY, theme); } catch (e) {}
  applyTheme(theme);
}
applyTheme(document.documentElement.dataset.theme || 'light');

// ========== 抽屉（右侧滑出编辑器） ==========
const _openDrawers = [];
function openDrawer(name) {
  const ov = _(name + 'Overlay'), dr = _(name);
  if (!dr) return;
  if (ov) ov.classList.add('open');
  dr.classList.add('open');
  if (!_openDrawers.includes(name)) _openDrawers.push(name);
  refreshSaveBar();
}
const _drawerGuards = {};
function setDrawerGuard(name, fn) { if (fn) _drawerGuards[name] = fn; else delete _drawerGuards[name]; }
async function closeDrawer(name) {
  const g = _drawerGuards[name];
  if (g) {
    let ok = true;
    try { ok = await g(); } catch (e) { ok = true; }
    if (!ok) return;
  }
  const ov = _(name + 'Overlay'), dr = _(name);
  if (ov) ov.classList.remove('open');
  if (dr) dr.classList.remove('open');
  const i = _openDrawers.indexOf(name);
  if (i >= 0) _openDrawers.splice(i, 1);
  refreshSaveBar();
}
// ESC closes the topmost drawer, but only when no modal layer is open on top
// of it (modal Esc handling lives in 06_models and runs on the same event).
const _ESC_MODAL_IDS = ['confirmModal','diffModal','modelMetaEditor','provPickerModal','comboModelPicker','presetPickerModal','importPreviewModal','connLoginModal','connImportModal','comboTestModal','modelSyncOverlay','logDetailModal'];
function _modalLayerOpen() {
  return _ESC_MODAL_IDS.some(id => { const el = _(id); return el && el.style.display === 'flex'; });
}
document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && _openDrawers.length && !_modalLayerOpen()) closeDrawer(_openDrawers[_openDrawers.length - 1]);
});

// ========== 未保存更改：悬浮保存条 + 变更预览弹窗 ==========
var _verCheckDone = false; // 「关于」页更新检查只自动跑一次（checkForUpdate 在 03_settings 定义）
// 每个面（provider 抽屉 / 路由页调优卡）注册一个 ctx；同一时刻只展示优先级最高且脏的那个。
const _saveBarCtxs = {};
const _SAVE_BAR_ORDER = ['provider', 'tuning'];
function setSaveBarCtx(surface, ctx) {
  if (ctx) _saveBarCtxs[surface] = ctx; else delete _saveBarCtxs[surface];
  refreshSaveBar();
}
function _currentSaveBarCtx() {
  if (_modalLayerOpen()) return null; // 弹窗（含变更预览/确认）盖住时收起保存条
  for (const k of _SAVE_BAR_ORDER) {
    const c = _saveBarCtxs[k];
    if (c && c.host() && c.dirty()) return c;
  }
  return null;
}
function refreshSaveBar() {
  const bar = _('saveDiscardBar');
  if (!bar) return;
  const ctx = _currentSaveBarCtx();
  bar.classList.toggle('show', !!ctx);
  const diffBtn = _('saveBarDiff');
  if (diffBtn) diffBtn.hidden = !(ctx && typeof ctx.diff === 'function');
  // 有未保存更改时，关闭/刷新页面前提示（浏览器统一展示原生确认文案）
  // 这里不看 host：脏数据藏在别的子页或收起的抽屉里时同样要提醒，避免静默丢失
  const anyDirty = Object.keys(_saveBarCtxs).some(k => {
    const c = _saveBarCtxs[k];
    return !!c && c.dirty();
  });
  if (anyDirty) window.onbeforeunload = e => { e.preventDefault(); return ''; };
  else window.onbeforeunload = null;
}
function discardSaveBar() { const c = _currentSaveBarCtx(); if (c) c.discard(); }
function saveViaBar() { const c = _currentSaveBarCtx(); if (c) c.save(); }
async function diffViaBar() {
  const c = _currentSaveBarCtx();
  if (!c || typeof c.diff !== 'function') return;
  let changes = [];
  try { changes = (await c.diff()) || []; } catch (e) { toast(t('无法生成差异预览'), 'error'); return; }
  showDiffModal(changes);
}
// 表单任意输入都会冒泡到这里，统一做脏检查（事件委托，兼容动态生成的行）
document.addEventListener('input', refreshSaveBar, true);
document.addEventListener('change', refreshSaveBar, true);

function fmtDiffVal(v) {
  if (v === undefined || v === null) return '';
  if (Array.isArray(v)) v = v.map(x => (x && typeof x === 'object') ? JSON.stringify(x) : String(x)).join(', ');
  else if (typeof v === 'object') v = JSON.stringify(v);
  else v = String(v);
  if (v.length > 90) v = v.slice(0, 90) + '…';
  return v;
}
// 两个同构浅对象（值可为标量/数组）→ [{label, from, to}]；值应已经 fmtDiffVal 字符串化
function diffFlat(base, cur) {
  const out = [];
  const keys = Object.keys(base);
  Object.keys(cur).forEach(k => { if (keys.indexOf(k) === -1) keys.push(k); });
  for (const k of keys) {
    const a = base[k] === undefined ? '' : base[k];
    const b = cur[k] === undefined ? '' : cur[k];
    if (String(a) === String(b)) continue;
    out.push({ label: k, from: String(a), to: String(b) });
  }
  return out;
}
const SENSITIVE_LABEL_RE = /(key|token|secret|password|authorization)/i;
let _diffResolve = null;
function showDiffModal(changes) {
  return new Promise(resolve => {
    const list = _('diffList');
    if (list) list.textContent = '';
    (changes || []).forEach(ch => {
      const sens = SENSITIVE_LABEL_RE.test(ch.label);
      const row = document.createElement('div'); row.className = 'diff-row';
      const lab = document.createElement('span'); lab.className = 'diff-label'; lab.textContent = ch.label;
      const from = document.createElement('span'); from.className = 'diff-from';
      const to = document.createElement('span'); to.className = 'diff-to';
      if (sens) {
        from.textContent = ch.from ? '••••' : '';
        to.textContent = ch.to ? '••••' : '';
        if (ch.from || ch.to) from.title = to.title = t('敏感值已隐藏');
      } else {
        from.textContent = ch.from;
        to.textContent = ch.to;
      }
      const arrow = document.createElement('span'); arrow.className = 'diff-arrow'; arrow.textContent = '→';
      row.appendChild(lab); row.appendChild(from); row.appendChild(arrow); row.appendChild(to);
      if (list) list.appendChild(row);
    });
    const m = _('diffModal');
    if (m) m.style.display = 'flex';
    _diffResolve = resolve;
  });
}
function resolveDiff(val) {
  const m = _('diffModal');
  if (m) m.style.display = 'none';
  if (_diffResolve) { const r = _diffResolve; _diffResolve = null; r(!!val); }
}

// ========== 侧栏收起 ==========
const NAV_COLLAPSE_KEY = 'cline_admin_nav';
// 轨道提示单例元素引用：必须在 syncNavCollapseBtn() 首次顶层调用之前初始化，
// 否则 _navTipHide 读取 TDZ 中的 let 会抛错并中断后续脚本（悬停监听器将不会注册）。
let _navRailTipEl = null;
function syncNavCollapseBtn() {
  const collapsed = document.documentElement.dataset.nav === 'collapsed';
  const label = collapsed ? t('展开侧栏') : t('收起侧栏');
  // 头部按钮（仅窄屏可见）与边缘悬浮钮（仅桌面可见）共用文案；图标翻转由 CSS 完成，
  // 不再重写 innerHTML
  document.querySelectorAll('.nav-collapse-btn,.nav-edge-toggle').forEach(btn => {
    btn.title = label; btn.setAttribute('aria-label', label);
  });
  _navTipHide(); // 状态切换后旧提示文案即失效
}
function toggleNav() {
  const collapsed = document.documentElement.dataset.nav === 'collapsed';
  if (collapsed) delete document.documentElement.dataset.nav;
  else document.documentElement.dataset.nav = 'collapsed';
  try { localStorage.setItem(NAV_COLLAPSE_KEY, collapsed ? 'expanded' : 'collapsed'); } catch (e) {}
  syncNavCollapseBtn();
}
syncNavCollapseBtn();

// ========== 收起态轨道提示（控制台 nav-tooltip 等价物） ==========
// 单例固定元素；仅 data-nav=collapsed 且宽屏时显示，文案取 .nav-label 文本节点
// （applyLang 已保证语言正确），因此无需额外的提示词条映射。
function _navTipHide() { if (_navRailTipEl) _navRailTipEl.classList.remove('show'); }
function _navTipShow(text, centerY) {
  if (window.innerWidth <= 760) return;
  if (!_navRailTipEl) {
    _navRailTipEl = document.createElement('div');
    _navRailTipEl.className = 'nav-rail-tip';
    document.body.appendChild(_navRailTipEl);
  }
  const tip = _navRailTipEl;
  tip.textContent = text;
  tip.style.display = 'block';
  const h = tip.getBoundingClientRect().height;
  tip.style.left = '72px';
  tip.style.top = Math.max(6, Math.min(centerY - h / 2, window.innerHeight - h - 6)) + 'px';
  tip.classList.add('show');
  tip.style.display = '';
}
(function initNavRailTip() {
  const nav = document.querySelector('.sidebar-nav');
  if (!nav) return;
  const collapsed = () => document.documentElement.dataset.nav === 'collapsed';
  const itemOf = e => {
    const it = e.target && e.target.closest ? e.target.closest('.nav-item') : null;
    return it && nav.contains(it) ? it : null;
  };
  const showForItem = e => {
    if (!collapsed()) return;
    const it = itemOf(e);
    if (!it) return;
    const lab = it.querySelector('.nav-label');
    if (!lab || !lab.textContent) return;
    const r = it.getBoundingClientRect();
    _navTipShow(lab.textContent, r.top + r.height / 2);
  };
  nav.addEventListener('mouseover', showForItem);
  nav.addEventListener('mouseout', e => { if (itemOf(e)) _navTipHide(); });
  nav.addEventListener('click', _navTipHide);
  nav.addEventListener('scroll', _navTipHide);
  const edge = document.querySelector('.nav-edge-toggle');
  if (edge) {
    const show = () => {
      const label = document.documentElement.dataset.nav === 'collapsed' ? t('展开侧栏') : t('收起侧栏');
      _navTipShow(label + ' · Ctrl+B', 56 + 12);
    };
    edge.addEventListener('mouseenter', show);
    edge.addEventListener('mouseleave', _navTipHide);
    edge.addEventListener('focus', show);
    edge.addEventListener('blur', _navTipHide);
  }
})();

// Ctrl/Cmd+B 收起/展开侧栏（输入控件聚焦时不拦截）
document.addEventListener('keydown', e => {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === 'b' || e.key === 'B')) {
    const evTarget = e.target;
    if (evTarget && (evTarget.tagName === 'INPUT' || evTarget.tagName === 'TEXTAREA' || evTarget.isContentEditable)) return;
    e.preventDefault();
    toggleNav();
  }
});


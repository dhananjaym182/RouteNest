// ========== 导航 ==========
// Legacy source-oriented hashes remain aliases. New navigation is organised by
// the global provider-system concepts and writes canonical hashes back to the URL.
const TAB_ALIASES = {
  global: 'providers',
  accounts: 'credentials',
  import: 'credentials',
  upstreams: 'network',
};
function normalizeTabName(name) {
  return TAB_ALIASES[name] || name;
}

function primaryNavTab(name) {
  return normalizeTabName(name);
}

let _activeTabName = '';

function updateShellContext(name) {
  name = normalizeTabName(name || 'dashboard');
  const nav = document.querySelector('.nav-item[data-tab="' + primaryNavTab(name) + '"]');
  const title = nav && nav.querySelector('.nav-label');
  const section = nav && nav.closest('.nav-section');
  const sectionLabel = section && section.querySelector('.nav-section-label');
  const titleEl = _('currentPageTitle');
  const sectionEl = _('currentNavSection');
  if (titleEl) titleEl.textContent = title ? title.textContent.trim() : t('概览');
  if (sectionEl) sectionEl.textContent = sectionLabel ? sectionLabel.textContent.trim() : t('工作区');
}

function toggleLoginImport(forceOpen) {
  const body = _('loginImportBody'), btn = _('loginImportToggle');
  if (!body || !btn) return;
  const open = (forceOpen === undefined) ? body.style.display === 'none' : !!forceOpen;
  body.style.display = open ? '' : 'none';
  btn.innerHTML = open ? uiIcon('caretDown') : uiIcon('caretRight');
  const label = open ? '收起' : '展开';
  btn.title = label;
  btn.setAttribute('aria-label', label);
  btn.setAttribute('aria-expanded', open ? 'true' : 'false');
  applyLang();
}

function toggleModelUsage(forceOpen) {
  const body = _('modelUsageBody'), btn = _('modelUsageToggle');
  if (!body || !btn) return;
  const open = (forceOpen === undefined) ? body.style.display === 'none' : !!forceOpen;
  body.style.display = open ? '' : 'none';
  btn.innerHTML = open ? uiIcon('caretDown') : uiIcon('caretRight');
  const label = open ? '收起' : '展开';
  btn.title = label;
  btn.setAttribute('aria-label', label);
  btn.setAttribute('aria-expanded', open ? 'true' : 'false');
  applyLang();
}

function showAccountLogin(subtab) {
  applyTab('credentials');
  switchPageSubTab('accounts', 'pool', true);
  toggleLoginImport(true);
  if (subtab) {
    const tab = document.querySelector('#importTabs .tab[data-tab="' + subtab + '"]');
    if (tab) tab.click();
  }
  const sec = _('loginImportSection');
  if (sec) setTimeout(() => sec.scrollIntoView({ behavior: 'smooth', block: 'start' }), 50);
}

function applyTab(name){
  const fromImport = (name === 'import');
  const fromAccounts = (name === 'accounts');
  name = normalizeTabName(name);
  // 非法/未知标签页（如手改 URL）回退到 dashboard，避免空白页
  if (!name || !_('tab-' + name)) name = 'dashboard';
  document.querySelectorAll('.nav-item').forEach(e => {
    const on = e.dataset.tab === primaryNavTab(name);
    e.classList.toggle('active', on);
    if (on) e.setAttribute('aria-current', 'page'); else e.removeAttribute('aria-current');
  });
  document.querySelectorAll('.tab-panel').forEach(e => e.style.display = 'none');
  const panel = _('tab-' + name);
  if (panel) {
    panel.style.display = 'block';
    panel.classList.remove('enter');
    void panel.offsetWidth; // 强制重排以重放入场动画
    panel.classList.add('enter');
  }
  try { history.replaceState(null, '', '#' + name); } catch(e){}
  _activeTabName = name;
  setActiveTab(name);
  updateShellContext(name);
  if (typeof closeMobileNav === 'function') closeMobileNav();
  if (name !== 'dashboard' && typeof stopDashPolling === 'function') stopDashPolling();
  if (name !== 'logs' && typeof stopLogPolling === 'function') stopLogPolling();
  if (name === 'dashboard') { applyLang(); loadStats(); loadAccounts(); loadDashboardOverview(); if (typeof loadDashboardActivity === 'function') loadDashboardActivity(); startDashPolling(); }
  else if (name === 'credentials') { loadGlobal(); loadAccounts(); loadConnections(); loadStats(); }
  if (fromImport) { switchPageSubTab('accounts', 'pool', true); toggleLoginImport(true); }
  else if (fromAccounts) switchPageSubTab('accounts', 'pool', true);
  else if (name === 'logs') { loadRequestLogs(true); startLogPolling(); }
  else if (name === 'routing') { loadModels().then(() => { loadConfig(); }); }
  else if (name === 'models') { loadGlobal(); loadModels().then(() => { loadConfig(); }); }
  else if (name === 'combos') { loadCombos(); }
  else if (name === 'network') { loadGlobal(); loadOcConfig(); loadClineProxyConfig(); loadZenHeaders(); loadStats(); }
  else if (name === 'providers') { loadGlobal(); loadProviders(); loadConnections(); }
  else if (name === 'health') { loadGlobal(); }
  else if (name === 'security') { loadKeys(); loadConfig(); }
  else if (name === 'quota') { loadQuota(true); }
  else if (name === 'about') { if (typeof checkForUpdate === 'function' && !_verCheckDone) checkForUpdate(false); }
  refreshSaveBar();
}

function switchTab(name) { applyTab(name); }

// Open a settings area at a specific subtab instead of restoring whichever
// subtab happened to be active on the user's last visit.
function showPageSubTab(page, sub) {
  if (page === 'routing' && sub === 'models') { applyTab('models'); return; }
  applyTab(page);
  switchPageSubTab(page, sub);
}

// 顶栏一键刷新：重新加载当前页数据（对齐 CLIProxyAPI 面板的 header refresh）
function refreshAll(btn) {
  if (!_activeTabName) return;
  if (btn) {
    btn.classList.add('spinning');
    setTimeout(() => btn.classList.remove('spinning'), 800);
  }
  applyTab(_activeTabName);
}

document.querySelectorAll('.nav-item').forEach(el => {
  el.addEventListener('click', () => {
    applyTab(el.dataset.tab);
  });
});

// 从 URL/SessionStorage 恢复上次打开的标签页（统一在底部初始化时执行）

// ========== 侧栏排序（拖拽） ==========
// 排序持久化按布局版本隔离：ui/html/shell.html 侧栏重新分组后 bump NAV_LAYOUT_VERSION，
// 旧版本（含最早的裸数组格式）保存的顺序直接作废——否则旧顺序会在新分组上
// 逐条重演，把重排效果抵消掉（表现为「改了布局但侧栏看起来没变」）。
const NAV_ORDER_KEY = 'cline_admin_nav_order';
const NAV_LAYOUT_VERSION = '5';
function applyNavOrder() {
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(NAV_ORDER_KEY) || 'null'); } catch(e) {}
  if (!saved || saved.v !== NAV_LAYOUT_VERSION || !Array.isArray(saved.order) || !saved.order.length) {
    try { localStorage.removeItem(NAV_ORDER_KEY); } catch(e) {} // 清掉旧版本/裸数组遗留
    return;
  }
  const order = saved.order;
  const nav = document.querySelector('.nav-section').parentElement;
  const items = {};
  nav.querySelectorAll('.nav-section .nav-item').forEach(el => { items[el.dataset.tab] = el; });
  // 重排：按保存的顺序把条目移回其分区末尾，未知 tab 忽略（固定项不参与排序）
  order.forEach(tab => {
    const el = items[tab];
    if (!el) return;
    const sec = el.closest('.nav-section');
    if (!sec) return;
    sec.appendChild(el);
  });
}
function saveNavOrder() {
  const order = Array.from(document.querySelectorAll('.nav-section .nav-item')).map(el => el.dataset.tab);
  try { localStorage.setItem(NAV_ORDER_KEY, JSON.stringify({ v: NAV_LAYOUT_VERSION, order: order })); } catch(e) {}
}
function initNavDrag() {
  let dragged = null;
  // 固定项（.nav-pinned，如「快速开始」）不参与拖拽排序
  document.querySelectorAll('.nav-section .nav-item').forEach(el => {
    el.draggable = true;
    el.addEventListener('dragstart', e => {
      if (document.documentElement.dataset.nav === 'collapsed') { e.preventDefault(); return; } // 轨道图标态拖拽目标靠猜，禁用
      dragged = el; el.classList.add('dragging'); try { e.dataTransfer.setData('text/plain', el.dataset.tab); } catch(err){} e.dataTransfer.effectAllowed = 'move';
    });
    el.addEventListener('dragend', () => { el.classList.remove('dragging'); document.querySelectorAll('.nav-item').forEach(x => x.classList.remove('drop-target')); dragged = null; });
    el.addEventListener('dragover', e => {
      e.preventDefault();
      if (!dragged || dragged === el) return;
      e.dataTransfer.dropEffect = 'move';
      document.querySelectorAll('.nav-item').forEach(x => x.classList.remove('drop-target'));
      el.classList.add('drop-target');
    });
    el.addEventListener('dragleave', () => el.classList.remove('drop-target'));
    el.addEventListener('drop', e => {
      e.preventDefault();
      if (!dragged || dragged === el) return;
      const sec = el.closest('.nav-section');
      const rect = el.getBoundingClientRect();
      const after = (e.clientY - rect.top) > rect.height / 2;
      sec.insertBefore(dragged, after ? el.nextSibling : el);
      dragged.classList.remove('dragging');
      document.querySelectorAll('.nav-item').forEach(x => x.classList.remove('drop-target'));
      saveNavOrder();
    });
  });
}
initNavDrag();
applyNavOrder();

// ========== 页内子导航（设置页顶部导航栏，划分功能子页） ==========
const SUBTAB_KEY_PREFIX = 'cline_admin_subtab_';
function switchPageSubTab(page, sub, skipSave) {
  if (page === 'routing' && sub === 'models') { applyTab('models'); return; }
  const nav = document.querySelector('.page-subtabs[data-page="' + page + '"]');
  if (!nav) return;
  nav.querySelectorAll('.subtab').forEach(e => {
    const active = e.dataset.sub === sub;
    e.classList.toggle('active', active);
    e.setAttribute('aria-selected', active ? 'true' : 'false');
    e.tabIndex = active ? 0 : -1;
  });
  const panel = nav.closest('.tab-panel');
  if (panel) {
    panel.querySelectorAll(':scope > .page-subpage').forEach(g => {
      const active = g.dataset.subpage === sub;
      g.style.display = active ? '' : 'none';
      g.setAttribute('aria-hidden', active ? 'false' : 'true');
    });
  }
  if (!skipSave) {
    try { localStorage.setItem(SUBTAB_KEY_PREFIX + page, sub); } catch(e) {}
    onPageSubTabShown(page, sub);
    // 子页切换后立即刷新悬浮保存条：调优卡的保存条只在其子页显示，切走即收起
    refreshSaveBar();
  }
}

// 子页显示时的按需加载钩子（用户点击切换时触发；首次进入由 applyTab 处理）
function onPageSubTabShown(page, sub) {}
document.querySelectorAll('.page-subtabs').forEach(nav => {
  nav.setAttribute('role', 'tablist');
  nav.querySelectorAll('.subtab').forEach(el => {
    el.setAttribute('role', 'tab');
    el.setAttribute('aria-selected', el.classList.contains('active') ? 'true' : 'false');
    el.tabIndex = el.classList.contains('active') ? 0 : -1;
    el.addEventListener('click', () => switchPageSubTab(nav.dataset.page, el.dataset.sub));
    el.addEventListener('keydown', event => {
      const tabs = Array.from(nav.querySelectorAll('.subtab'));
      const index = tabs.indexOf(el);
      let next = -1;
      if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
      else if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length;
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = tabs.length - 1;
      else if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); el.click(); return; }
      if (next >= 0) { event.preventDefault(); tabs[next].focus(); tabs[next].click(); }
    });
  });
  // 恢复上次选中的子页
  let saved = null;
  try { saved = localStorage.getItem(SUBTAB_KEY_PREFIX + nav.dataset.page); } catch(e) {}
  if (saved && saved !== nav.querySelector('.subtab.active')?.dataset.sub && nav.querySelector('.subtab[data-sub="' + saved + '"]')) {
    switchPageSubTab(nav.dataset.page, saved, true);
  }
});

// 导入子标签
document.querySelectorAll('#importTabs .tab').forEach(el => {
  el.addEventListener('click', () => {
    document.querySelectorAll('#importTabs .tab').forEach(e => e.classList.remove('active'));
    el.classList.add('active');
    document.querySelectorAll('#import-oauth,#import-token,#import-batch').forEach(e => e.classList.remove('active'));
    _('import-' + el.dataset.tab).classList.add('active');
  });
});

// Shared model-sync notices and source-scoped cleanup actions.
const _modelSyncSeen = new Map();
const _modelSyncGroups = new Map();
let _modelSyncRemovalTargets = [];
let _modelSyncRemoving = false;
let _modelSyncRemovalError = '';
let _modelDelistedSeen = new Set();
let _providerSyncManagedIDs = new Set();
let _providerSyncManagedNames = new Set();
const MODEL_SYNC_SEEN_KEY = 'routenest_admin_model_sync_seen_v1';
try {
  const saved = JSON.parse(localStorage.getItem(MODEL_SYNC_SEEN_KEY) || '{}');
  if (Array.isArray(saved.delisted)) _modelDelistedSeen = new Set(saved.delisted.filter(key => typeof key === 'string'));
  if (Array.isArray(saved.managedProviderIDs)) _providerSyncManagedIDs = new Set(saved.managedProviderIDs.filter(id => typeof id === 'string'));
  if (Array.isArray(saved.managedProviderNames)) _providerSyncManagedNames = new Set(saved.managedProviderNames.filter(name => typeof name === 'string'));
  if (Array.isArray(saved.sync)) saved.sync.forEach(entry => {
    if (Array.isArray(entry) && typeof entry[0] === 'string' && entry[1] && typeof entry[1].signature === 'string') {
      _modelSyncSeen.set(entry[0], entry[1]);
    }
  });
} catch (e) {}

function persistModelSyncSeen() {
  try {
    localStorage.setItem(MODEL_SYNC_SEEN_KEY, JSON.stringify({
      delisted: Array.from(_modelDelistedSeen), sync: Array.from(_modelSyncSeen),
      managedProviderIDs: Array.from(_providerSyncManagedIDs), managedProviderNames: Array.from(_providerSyncManagedNames),
    }));
  } catch (e) {}
}
let _modelCatalogRequest = 0;
let _modelCatalogApplied = 0;
let _modelCatalogLatest = {};
let _modelDelistingCheck = null;
let _modelProviderStatusKnown = false;
let _modelProviderStatusRead = null;

// Observe unmerged catalogue rows: removal belongs to a source/provider even
// when another source still offers the same model ID. Recovery resets the mark
// so a later delisting warns again; ordinary refreshes do not repeat notices.
function observeModelDelistings(models) {
  const next = new Set(), removed = new Map();
  // Provider sync state is authoritative when it supplies delistedModels. Keep
  // those keys separate from the legacy logical-model projection, which can be
  // active while another provider's same-ID offering is delisted.
  _modelDelistedSeen.forEach(key => {
    try {
      const parts = JSON.parse(key);
      // Startup prewarms the catalogue before provider status may arrive.
      // Only an authoritative provider observation can reset provider keys.
      if (parts[0] === 'provider-sync') next.add(key);
    } catch (e) {}
  });
  (models || []).forEach(m => {
    if (!m.delisted) return;
    const source = modelSource(m);
    const provider = source === 'provider' ? (m.provider || 'Provider') : '';
    if (source === 'provider' && _providerSyncManagedNames.has(provider)) return;
    const key = JSON.stringify([source, provider, m.id]);
    next.add(key);
    if (_modelDelistedSeen.has(key)) return;
    const label = provider
      ? ((provider === 'Cline' || provider === 'OpenCode Zen') ? 'Provider: ' + provider : provider)
      : (MODEL_SOURCE_LABEL[source] || t('用户自定义'));
    const groupKey = JSON.stringify([source, provider]);
    if (!removed.has(groupKey)) removed.set(groupKey, { label: label, ids: [] });
    removed.get(groupKey).ids.push(m.id);
  });
  _modelDelistedSeen = next;
  persistModelSyncSeen();
  removed.forEach((group, key) => showModelSyncModal({ removed: group.ids }, group.label, key));
}

// Provider recheck state is provider-scoped, so it remains accurate when two
// providers expose the same model ID. New servers return delistedModels; older
// servers omit it and continue through the legacy catalogue observation below.
function observeProviderSyncDelistings(providers, sync) {
  _modelProviderStatusKnown = true;
  const active = new Set(), managedIDs = new Set(), managedNames = new Set(), removed = [];
  const providerIDs = new Set((providers || []).map(p => String(p.id || p.name || 'Provider')));
  const nameCounts = new Map();
  (providers || []).forEach(p => {
    const name = p.name || p.id || 'Provider';
    nameCounts.set(name, (nameCounts.get(name) || 0) + 1);
  });
  (providers || []).forEach(p => {
    const st = (sync || {})[p.id] || {};
    const provider = p.name || p.id || 'Provider';
    const providerID = String(p.id || provider);
    if (!Array.isArray(st.delistedModels)) {
      // An unavailable/older status read cannot revoke a saved acknowledgement.
      if (_providerSyncManagedIDs.has(providerID)) {
        managedIDs.add(providerID); managedNames.add(provider);
        _modelDelistedSeen.forEach(key => {
          try { const parts = JSON.parse(key); if (parts[0] === 'provider-sync' && parts[1] === providerID) active.add(key); } catch (e) {}
        });
      }
      return;
    }
    const label = (nameCounts.get(provider) || 0) > 1 ? provider + ' (' + providerID + ')' : provider;
    managedIDs.add(providerID);
    managedNames.add(provider);
    const fresh = [];
    st.delistedModels.forEach(id => {
      const key = JSON.stringify(['provider-sync', providerID, id]);
      active.add(key);
      if (_modelDelistedSeen.has(key)) return;
      _modelDelistedSeen.add(key);
      // Upgrade a uniquely named provider's legacy catalogue acknowledgement
      // to its stable instance ID when the authoritative sync state arrives.
      if (nameCounts.get(provider) === 1 && _modelDelistedSeen.has(JSON.stringify(['provider', provider, id]))) return;
      fresh.push(id);
    });
    if (fresh.length) removed.push({ provider: label, ids: fresh, key: 'provider:' + providerID });
  });
  Array.from(_modelDelistedSeen).forEach(key => {
    try {
      const parts = JSON.parse(key);
      if (parts[0] === 'provider-sync' && !active.has(key) && (managedIDs.has(parts[1]) || !providerIDs.has(parts[1]))) _modelDelistedSeen.delete(key);
    } catch (e) {}
  });
  _providerSyncManagedIDs = managedIDs;
  _providerSyncManagedNames = managedNames;
  persistModelSyncSeen();
  removed.forEach(group => showModelSyncModal({ removed: group.ids }, group.provider, group.key));
}

function readProviderModelSync() {
  if (!_modelProviderStatusRead) {
    _modelProviderStatusRead = Promise.all([
      api('GET', '/providers'), api('GET', '/providers/sync-status'),
    ]).then(([providers, sync]) => observeProviderSyncDelistings(
      (providers.data && providers.data.providers) || [],
      (sync.data && sync.data.providers) || {},
    )).finally(() => { _modelProviderStatusRead = null; });
  }
  return _modelProviderStatusRead;
}

async function readModelCatalog() {
  const request = ++_modelCatalogRequest;
  // Resolve current provider identities before interpreting legacy catalogue
  // names. A rename must not turn an acknowledged instance notice into a new one.
  const providerStatus = _modelProviderStatusKnown ? Promise.resolve() : readProviderModelSync().catch(() => {});
  const d = await api('GET', '/models');
  await providerStatus;
  // A slower, older read must not undo a newer delisting/recovery observation.
  if (request > _modelCatalogApplied) {
    _modelCatalogApplied = request;
    _modelCatalogLatest = d.data || {};
    notifyModelSyncResult(_modelCatalogLatest.lastSync, 'Cline');
    observeModelDelistings(_modelCatalogLatest.models || []);
  }
  return _modelCatalogLatest;
}

function checkModelDelistings() {
  if (document.hidden) return Promise.resolve();
  if (!_modelDelistingCheck) {
    // These are local read models only. Do not redraw tables or config forms,
    // trigger upstream discovery, or interrupt edits on a timer.
    _modelDelistingCheck = Promise.all([
      readModelCatalog(),
      readProviderModelSync(),
    ]).catch(() => {}).finally(() => { _modelDelistingCheck = null; });
  }
  return _modelDelistingCheck;
}

function notifyModelSyncResult(res, source) {
  if (!res || !res.changed || res.error) return;
  const signature = JSON.stringify([res.syncedAt, res.added || [], res.removed || []]);
  const at = Date.parse(res.syncedAt) || 0;
  const previous = _modelSyncSeen.get(source);
  if (previous && (previous.signature === signature || previous.at > at)) return;
  _modelSyncSeen.set(source, { signature: signature, at: at });
  // The manual sync result can arrive before its catalogue refresh. Remember
  // these removals now so dismissing the popup during that read stays dismissed.
  const kind = source === 'OpenCode Zen' ? 'zen' : 'remote';
  const removed = (res.removed || []).filter(id => !_modelDelistedSeen.has(JSON.stringify([kind, '', id])));
  (res.removed || []).forEach(id => _modelDelistedSeen.add(JSON.stringify([kind, '', id])));
  persistModelSyncSeen();
  if ((res.added || []).length || removed.length) showModelSyncModal({ ...res, removed }, source);
}

async function loadModels() {
  try {
    const d = await readModelCatalog();
    const models = d.models || [];
    _cachedModels = models;
    renderModelList();
    const ls = d.lastSync || {};
    if (_('modelSyncTime')) {
      _('modelSyncTime').innerHTML = formatDateTime(ls.syncedAt);
    }
  } catch (e) { _('modelsList').textContent = t('加载失败'); }
}

async function syncModels() {
  const btn = _('syncModelsBtn');
  const orig = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span> ' + t('同步中...'); }
  try {
    const d = await api('POST', '/models/sync');
    const res = d.data || {};
    _modelCatalogApplied = ++_modelCatalogRequest;
    notifyModelSyncResult(res, 'Cline');
    await loadModels();
    if (res.changed) {
      toast(t('模型列表已更新'), 'success');
    } else {
      toast(t('模型无变化'), 'info');
    }
    if (_('settingDefModel')) await loadConfig();
  } catch (e) {
    toast(t('模型同步失败') + ': ' + (e.message || ''), 'error');
  }
  if (btn) { btn.disabled = false; btn.innerHTML = orig; }
}

function showModelSyncModal(res, source, groupKey) {
  const ov = _('modelSyncOverlay');
  if (!ov) return;
  if (ov.style.display !== 'flex') { _modelSyncGroups.clear(); _modelSyncRemovalError = ''; }
  const label = source || 'Cline';
  const key = groupKey || JSON.stringify([label === 'OpenCode Zen' ? 'zen' : 'remote', '']);
  if (!_modelSyncGroups.has(key)) _modelSyncGroups.set(key, { label: label, added: new Set(), removed: new Set() });
  const group = _modelSyncGroups.get(key);
  (res.added || []).forEach(id => group.added.add(id));
  (res.removed || []).forEach(id => group.removed.add(id));
  ov.style.display = 'flex';
  renderModelSyncModal();
}

function modelSyncRemovalRef(key, id) {
  if (key.startsWith('provider:')) return { id, source: 'provider', providerId: key.slice(9) };
  try {
    const [source, provider] = JSON.parse(key);
    if (source === 'remote' || source === 'zen') return { id, source };
    if (source === 'provider') return { id, source, provider };
  } catch (e) {}
  return null;
}

function modelSyncCanRemove() {
  return typeof adminHasPermission !== 'function' || adminHasPermission('provider.write');
}

function renderModelSyncModal() {
  const box = _('modelSyncModal');
  if (!box) return;
  _modelSyncRemovalTargets = [];
  let rowSequence = 0;
  let body = '';
  _modelSyncGroups.forEach((changes, key) => {
    const add = Array.from(changes.added).map(m => '<div class="p3-thin">+ <span class="mono">' + esc(m) + '</span></div>').join('');
    const rem = Array.from(changes.removed).map(m => {
      const target = modelSyncCanRemove() ? modelSyncRemovalRef(key, m) : null;
      const index = _modelSyncRemovalTargets.length;
      const rowID = 'modelSyncRemoved' + rowSequence++;
      if (target) _modelSyncRemovalTargets.push(target);
      return '<div class="model-sync-item"><span id="' + rowID + '" class="mono">' + esc(m) + '</span>' +
        (target ? '<button class="btn btn-sm" data-model-remove="' + index + '" aria-describedby="' + rowID + '" onclick="removeDelistedModels([' + index + '])"' + (_modelSyncRemoving ? ' disabled' : '') + '>' + t('移除') + '</button>' : '') + '</div>';
    }).join('');
    if (!add && !rem) return;
    body += '<div class="txt-strong mt-10 mb-6">' + esc(changes.label) + '</div>';
    if (add) body += '<div class="save-result ok mb-6">' + t('新增模型') + '</div>' + add;
    if (rem) body += '<div class="hint-sm mb-6">' + t('已从上游官方列表移除，可在此手动清理') + '</div>' + rem;
  });
  if (!body) body = '<div class="hint-mut">' + t('模型无变化') + '</div>';
  box.innerHTML =
    '<h2 id="modelSyncTitle" class="sync-title">' + t('模型同步') + '</h2>' +
    '<div class="sync-body">' + body + '</div>' +
    (_modelSyncRemovalError ? '<div class="model-sync-error" role="alert">' + esc(_modelSyncRemovalError) + '</div>' : '') +
    '<div class="modal-foot">' +
    (_modelSyncRemovalTargets.length ? '<button class="btn btn-danger" onclick="removeDelistedModels()"' + (_modelSyncRemoving ? ' disabled' : '') + '>' + t(_modelSyncRemoving ? '移除中...' : '移除全部已下架') + '</button>' : '') +
    '<button class="btn btn-primary" onclick="closeModelSyncModal()"' + (_modelSyncRemoving ? ' disabled' : '') + '>OK</button></div>';
}

function sameModelSyncRemoval(a, b) {
  if (a.id !== b.id || a.source !== b.source) return false;
  if (a.source !== 'provider') return true;
  return a.providerId ? a.providerId === b.providerId : a.provider === b.provider;
}

function modelRemovalBatches(targets) {
  const encoder = new TextEncoder(), batches = [];
  const overhead = encoder.encode(JSON.stringify({ models: [] })).length;
  let batch = [], bytes = overhead;
  targets.forEach(target => {
    const size = encoder.encode(JSON.stringify(target)).length;
    if (batch.length && (batch.length >= 100 || bytes + size + 1 > 120 * 1024)) {
      batches.push(batch); batch = []; bytes = overhead;
    }
    bytes += size + (batch.length ? 1 : 0);
    batch.push(target);
  });
  if (batch.length) batches.push(batch);
  return batches;
}

async function removeDelistedModels(indices) {
  if (_modelSyncRemoving || !modelSyncCanRemove()) return;
  const targets = indices ? indices.map(index => _modelSyncRemovalTargets[index]).filter(Boolean) : _modelSyncRemovalTargets.slice();
  if (!targets.length) return;
  _modelSyncRemoving = true;
  _modelSyncRemovalError = '';
  renderModelSyncModal();
  try {
    const result = { removed: [], skipped: [], errors: [] };
    for (const batch of modelRemovalBatches(targets)) {
      try {
        const response = await api('POST', '/models/delisted/remove', { models: batch });
        const data = response.data || {};
        if (!Array.isArray(data.removed) || !Array.isArray(data.skipped)) throw new Error(t('移除结果无效，请刷新后重试'));
        result.removed.push(...data.removed); result.skipped.push(...data.skipped);
        result.errors.push(...(data.errors || []));
      } catch (e) {
        result.errors.push({ error: e.message || t('删除失败: ') });
        break;
      }
    }
    const completed = result.removed.concat(result.skipped);
    _modelSyncGroups.forEach((group, key) => {
      group.removed.forEach(id => {
        const target = modelSyncRemovalRef(key, id);
        if (target && completed.some(item => sameModelSyncRemoval(target, item))) group.removed.delete(id);
      });
      if (!group.added.size && !group.removed.size) _modelSyncGroups.delete(key);
    });
    _modelSyncRemovalError = Array.from(new Set((result.errors || []).map(item => item.error).filter(Boolean))).join('; ');
    if (result.removed.length) toast(t('已移除 ') + result.removed.length, 'success');
    if (result.skipped.length) toast(t('模型已恢复或已移除，无需清理'), 'info');
    // Read the saved state after cleanup; the endpoint owns declarations,
    // offering state and routing references, not a sequence of UI writes.
    if (completed.length) {
      await Promise.allSettled([loadModels(), loadProviders(), loadGlobal(false), loadCombos()]);
      await loadConfig({ preserveEdits: true });
      if (typeof reconcileRemovedModelDrafts === 'function') reconcileRemovedModelDrafts(completed, result.removed);
    }
  } catch (e) {
    _modelSyncRemovalError = e.message || t('删除失败: ');
  } finally {
    _modelSyncRemoving = false;
    if (!_modelSyncGroups.size && !_modelSyncRemovalError) closeModelSyncModal();
    else renderModelSyncModal();
  }
}

function closeModelSyncModal() {
  if (_modelSyncRemoving) return;
  const ov = _('modelSyncOverlay');
  if (ov) ov.style.display = 'none';
  _modelSyncGroups.clear();
  _modelSyncRemovalTargets = [];
  _modelSyncRemovalError = '';
  persistModelSyncSeen();
}

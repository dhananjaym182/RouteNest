// Provider actions use instance IDs; display-name groups may require a chooser.
let _modelProviderAction = null;
let _modelProviderActionLoading = false;

function modelGroupProviders(key) {
  const data = _globalData || {}, instances = data.instances || [];
  if (key === 'src:cline') return instances.filter(p => p.id === 'cline');
  if (key === 'src:zen') return instances.filter(p => p.id === 'opencode');
  if (key.startsWith('src:prov:')) {
    const name = key.slice(9).trim().toLowerCase();
    return instances.filter(p => p.id !== 'cline' && p.id !== 'opencode' && String(p.name || '').trim().toLowerCase() === name);
  }
  if (key === 'src:custom') {
    const ids = new Set(_cachedModels.filter(m => m.custom).map(m => m.id));
    const owners = new Set((data.offerings || []).filter(o => ids.has(o.modelId)).map(o => o.providerId));
    if (_cachedModels.some(m => m.custom && (m.source === 'custom' || m.source === 'remote' || !m.source))) owners.add('cline');
    return instances.filter(p => owners.has(p.id));
  }
  return [];
}

function modelProviderRemovalChoices(instance, providers) {
  const source = instance.id === 'cline' ? 'remote' : instance.id === 'opencode' ? 'zen' : 'provider';
  const rows = new Map();
  const add = (id, qualifiedId) => {
    if (!id) return;
    const existing = rows.get(id);
    rows.set(id, { id, source, providerId: instance.id, qualifiedId: qualifiedId || existing && existing.qualifiedId || '' });
  };
  ((_globalData && _globalData.offerings) || []).forEach(o => {
    if (o.providerId === instance.id) add(o.modelId, o.qualifiedId);
  });
  if (source === 'provider') {
    const provider = providers.find(p => p.id === instance.id);
    (provider && provider.modelIds || []).forEach(id => add(id));
  } else {
    _cachedModels.forEach(m => {
      const owned = source === 'zen' ? m.source === 'zen' || m.source === 'seed' :
        m.source === 'remote' || m.source === 'custom' || !m.source && m.provider !== 'opencode';
      if (owned) add(m.id);
    });
  }
  return Array.from(rows.values()).sort((a, b) => a.id.localeCompare(b.id));
}

async function openModelProviderAction(button, mode) {
  if (!modelSyncCanRemove() || _modelProviderAction || _modelProviderActionLoading || button && button.disabled || !['edit', 'remove'].includes(mode)) return;
  const head = button && button.closest('.model-group-head');
  if (!head) return;
  const key = head.dataset.key;
  const capturedIDs = JSON.parse(head.dataset.providerIds || '[]');
  const navigation = typeof _tabNavigationGeneration === 'undefined' ? 0 : _tabNavigationGeneration;
  _modelProviderActionLoading = true;
  button.disabled = true;
  try {
    // Refresh local declarations before choosing a provider or offering. A stale
    // display name must never select the first same-name instance implicitly.
    const [instances, offerings, providers, catalog] = await Promise.all([
      api('GET', '/provider-instances'), api('GET', '/model-offerings'), api('GET', '/providers'),
      mode === 'remove' ? readModelCatalog() : Promise.resolve(null),
    ]);
    if (!modelSyncCanRemove() || typeof _activeTabName !== 'undefined' && _activeTabName !== 'models' ||
        navigation !== (typeof _tabNavigationGeneration === 'undefined' ? 0 : _tabNavigationGeneration)) return;
    _globalData = { ...(_globalData || {}), instances: instances.data.instances || [], offerings: offerings.data.offerings || [] };
    if (catalog) _cachedModels = catalog.models || [];
    const choices = capturedIDs.length ? _globalData.instances.filter(p => capturedIDs.includes(p.id)) : modelGroupProviders(key);
    if (!choices.length) throw new Error(t('未找到 Provider 实例'));
    if (mode === 'edit' && choices.length === 1) {
      setupProviderInstance(choices[0].id);
      return;
    }
    _modelProviderAction = {
      key, mode, instances: choices, providers: providers.data.providers || [],
      instance: null, models: [], selected: new Set(), busy: false, error: '', returnFocus: button,
    };
    const select = _('modelProviderActionProvider');
    select.innerHTML = '<option value="">' + esc(t('选择 Provider 实例')) + '</option>' + choices.map(p =>
      '<option value="' + escAttr(p.id) + '">' + esc(p.name || p.id) + ' · ' + esc(p.id) + '</option>').join('');
    select.value = '';
    _('modelProviderActionProviderField').hidden = choices.length === 1;
    _('modelProviderActionSearchRow').hidden = mode !== 'remove';
    _('modelProviderActionList').hidden = mode !== 'remove';
    _('modelProviderActionNote').textContent = mode === 'remove' ? t('仅移除此 Provider 的模型；相关组合和路由引用也会清理。') : t('选择要编辑的 Provider 实例。');
    _('modelProviderActionSearch').value = '';
    _('modelProviderActionModal').style.display = 'flex';
    selectModelProviderAction(choices.length === 1 ? choices[0].id : '');
    if (typeof refreshSaveBar === 'function') refreshSaveBar();
    (choices.length > 1 ? select : _('modelProviderActionSearch')).focus();
  } catch (e) {
    toast(t('加载失败') + ': ' + e.message, 'error');
  } finally {
    _modelProviderActionLoading = false;
    button.disabled = false;
  }
}

function selectModelProviderAction(id) {
  const state = _modelProviderAction;
  if (!state || state.busy) return;
  state.instance = state.instances.find(p => p.id === id) || null;
  _('modelProviderActionProvider').value = state.instance ? state.instance.id : '';
  state.models = state.instance ? modelProviderRemovalChoices(state.instance, state.providers) : [];
  state.selected.clear();
  state.error = '';
  _('modelProviderActionSearch').value = '';
  _('modelProviderActionTitle').textContent = t(state.mode === 'edit' ? '编辑 Provider' : '移除模型') +
    (state.instance ? ' · ' + (state.instance.name || state.instance.id) : '');
  renderModelProviderActionList();
}

function visibleModelProviderRemovals() {
  const query = _('modelProviderActionSearch').value.trim().toLowerCase();
  return (_modelProviderAction && _modelProviderAction.models || []).filter(row => !query ||
    (row.id + ' ' + row.qualifiedId).toLowerCase().includes(query));
}

function renderModelProviderActionList() {
  const state = _modelProviderAction;
  if (!state) return;
  const rows = visibleModelProviderRemovals();
  _('modelProviderActionList').innerHTML = rows.map(row =>
    '<div class="model-provider-removal-row"><label class="model-provider-removal-choice">' +
    '<input type="checkbox" data-model-id="' + escAttr(row.id) + '" onchange="toggleModelProviderRemoval(this.dataset.modelId, this.checked)"' +
      (state.selected.has(row.id) ? ' checked' : '') + (state.busy ? ' disabled' : '') + '>' +
    '<span class="model-provider-removal-identity"><code title="' + escAttr(row.id) + '">' + esc(row.id) + '</code>' +
      (row.qualifiedId ? '<code class="hint-mut" title="' + escAttr(row.qualifiedId) + '">' + esc(row.qualifiedId) + '</code>' : '') + '</span></label>' +
    '<button type="button" class="btn btn-sm btn-danger" data-model-id="' + escAttr(row.id) + '" onclick="removeModelProviderOfferings([this.dataset.modelId])" aria-label="' + escAttr(t('移除') + ': ' + row.id) + '"' +
      (state.busy ? ' disabled' : '') + '>' + esc(t('移除')) + '</button></div>'
  ).join('') || '<div class="system-empty">' + esc(t(!state.instance ? '选择 Provider 实例' : state.models.length ? '无匹配模型' : '暂无模型')) + '</div>';
  updateModelProviderActionControls();
}

function updateModelProviderActionControls() {
  const state = _modelProviderAction;
  if (!state) return;
  _('modelProviderActionCount').textContent = state.mode === 'remove' ?
    t('已选择 ') + state.selected.size + ' / ' + state.models.length : '';
  _('modelProviderActionError').textContent = state.error;
  _('modelProviderActionError').hidden = !state.error;
  _('modelProviderActionProvider').disabled = state.busy;
  _('modelProviderActionSearch').disabled = state.busy;
  _('modelProviderActionSelectAll').disabled = state.busy || !visibleModelProviderRemovals().length;
  _('modelProviderActionClear').disabled = state.busy || !state.selected.size;
  _('modelProviderActionCancel').disabled = state.busy;
  const submit = _('modelProviderActionSubmit');
  submit.disabled = state.busy || !state.instance || state.mode === 'remove' && !state.selected.size;
  submit.textContent = t(state.busy ? '移除中...' : state.mode === 'edit' ? '编辑 Provider' : '移除所选模型');
  submit.classList.toggle('btn-danger', state.mode === 'remove');
}

function toggleModelProviderRemoval(id, checked) {
  const state = _modelProviderAction;
  if (!state || state.busy || !state.models.some(row => row.id === id)) return;
  if (checked) state.selected.add(id); else state.selected.delete(id);
  updateModelProviderActionControls();
}

function selectAllModelProviderRemovals() {
  if (!_modelProviderAction || _modelProviderAction.busy) return;
  visibleModelProviderRemovals().forEach(row => _modelProviderAction.selected.add(row.id));
  renderModelProviderActionList();
}

function clearModelProviderRemovals() {
  if (!_modelProviderAction || _modelProviderAction.busy) return;
  _modelProviderAction.selected.clear();
  renderModelProviderActionList();
}

async function submitModelProviderAction() {
  const state = _modelProviderAction;
  if (!state || state.busy || !state.instance || !modelSyncCanRemove()) return;
  if (state.mode === 'edit') {
    const id = state.instance.id;
    closeModelProviderAction();
    setupProviderInstance(id);
  } else await removeModelProviderOfferings(Array.from(state.selected));
}

async function removeModelProviderOfferings(ids) {
  const state = _modelProviderAction;
  if (!state || state.busy || !state.instance || !modelSyncCanRemove()) return;
  const selected = new Set(ids), targets = state.models.filter(row => selected.has(row.id)).map(({ id, source, providerId }) => ({ id, source, providerId }));
  if (!targets.length) return;
  state.busy = true;
  state.error = '';
  renderModelProviderActionList();
  const result = { removed: [], skipped: [], errors: [] };
  try {
    const confirmation = showConfirm(t('移除所选模型及其路由引用？') + ' · ' + (state.instance.name || state.instance.id) + ' (' + targets.length + ')');
    const cancel = _('confirmModal').querySelector('button:not(.btn-danger)');
    if (cancel) cancel.focus();
    const confirmed = await confirmation;
    if (!confirmed) return;
    for (const batch of modelRemovalBatches(targets)) {
      try {
        const response = await api('POST', '/models/remove', { models: batch });
        const data = response.data || {};
        if (!Array.isArray(data.removed) || !Array.isArray(data.skipped) || !Array.isArray(data.errors)) throw new Error(t('移除结果无效，请刷新后重试'));
        result.removed.push(...data.removed); result.skipped.push(...data.skipped); result.errors.push(...data.errors);
      } catch (e) {
        result.errors.push({ error: e.message });
        break;
      }
    }
    const completed = result.removed.concat(result.skipped);
    removeCompletedModelSyncNotices(completed);
    state.models = state.models.filter(row => !completed.some(ref => sameModelSyncRemoval(row, ref)));
    completed.forEach(ref => state.selected.delete(ref.id));
    state.error = Array.from(new Set(result.errors.map(item => item.error).filter(Boolean))).join('; ');
    if (result.removed.length) toast(t('已移除 ') + result.removed.length, 'success');
    if (result.skipped.length) toast(t('模型已移除，无需重复清理'), 'info');
    // A strict multi-store operation can commit an earlier cleanup stage before
    // failing. Refresh after errors as well, and keep failed refs for retry.
    await Promise.allSettled([loadModels(), loadProviders(), loadGlobal(false), loadCombos()]);
    await loadConfig({ preserveEdits: true });
    if (typeof reconcileRemovedModelDrafts === 'function') reconcileRemovedModelDrafts(completed, result.removed);
  } catch (e) {
    state.error = e.message || t('删除失败: ');
  } finally {
    state.busy = false;
    if (!state.models.length && !state.error) closeModelProviderAction();
    else {
      renderModelProviderActionList();
      _('modelProviderActionSearch').focus();
    }
  }
}

function closeModelProviderAction() {
  const state = _modelProviderAction;
  if (state && state.busy) return;
  _('modelProviderActionModal').style.display = 'none';
  _modelProviderAction = null;
  if (typeof refreshSaveBar === 'function') refreshSaveBar();
  if (state && state.returnFocus && state.returnFocus.isConnected) state.returnFocus.focus();
  else if (state) {
    const head = Array.from(document.querySelectorAll('#modelsList .model-group-head')).find(el => el.dataset.key === state.key);
    const button = head && head.querySelector('[data-model-provider-action="' + state.mode + '"]');
    const fallback = button || _('modelSearch');
    if (fallback) fallback.focus();
  }
}

document.addEventListener('keydown', e => {
  if (e.key !== 'Tab' || !_modelProviderAction || _('modelProviderActionModal').style.display !== 'flex') return;
  const modal = _('confirmModal').style.display === 'flex' ? _('confirmModal') : _('modelProviderActionModal');
  const controls = Array.from(modal.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled)')).filter(el => el.getClientRects().length);
  if (!controls.length) { e.preventDefault(); return; }
  const index = controls.indexOf(document.activeElement);
  if (index === -1 || e.shiftKey && index === 0 || !e.shiftKey && index === controls.length - 1) {
    e.preventDefault();
    (e.shiftKey ? controls[controls.length - 1] : controls[0]).focus();
  }
});

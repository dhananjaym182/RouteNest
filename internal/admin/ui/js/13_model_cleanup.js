// Removal invalidates only the affected provider's in-flight discovery reads.
const _providerModelRemovalEpochs = new Map();
function providerModelRemovalEpoch(providerID) {
  return _providerModelRemovalEpochs.get(String(providerID || '').trim()) || 0;
}

// Called after refreshing saved models, provider declarations, offerings and
// config. Reconcile the removed references without refilling unrelated drafts.
function reconcileRemovedModelDrafts(completedRefs, confirmedRemovedRefs) {
  const text = value => String(value == null ? '' : value).trim();
  const nameKey = value => text(value).toLowerCase();
  const sourceKey = value => {
    const source = nameKey(value);
    if (source === 'remote' || source === 'cline') return 'remote';
    if (source === 'zen' || source === 'seed' || source === 'opencode') return 'zen';
    return source;
  };
  const models = typeof _cachedModels !== 'undefined' && Array.isArray(_cachedModels) ? _cachedModels : [];
  const global = typeof _globalData !== 'undefined' && _globalData ? _globalData : {};
  const offerings = Array.isArray(global.offerings) ? global.offerings : [];
  const providers = new Map();
  if (typeof _provCache !== 'undefined') {
    Object.keys(_provCache).forEach(id => {
      const provider = _provCache[id];
      if (provider) providers.set(text(provider.id || id), provider);
    });
  }
  (global.instances || []).forEach(provider => {
    const id = text(provider.id);
    if (id && !providers.has(id)) providers.set(id, provider);
  });
  const customProvider = id => id !== 'cline' && id !== 'opencode';
  const providerNames = new Map();
  providers.forEach((provider, id) => {
    if (!customProvider(id) || !text(provider.name)) return;
    const name = nameKey(provider.name);
    if (!providerNames.has(name)) providerNames.set(name, []);
    providerNames.get(name).push(id);
  });
  const normalize = input => {
    if (!input || !text(input.id)) return null;
    const source = sourceKey(input.source);
    let providerId = text(input.providerId), provider = text(input.provider);
    if (source === 'remote' || source === 'zen') {
      providerId = source === 'remote' ? 'cline' : 'opencode';
      provider = source === 'remote' ? 'Cline' : 'OpenCode Zen';
    } else if (source === 'provider') {
      if (!providerId) {
        const matches = providerNames.get(nameKey(provider)) || [];
        if (matches.length !== 1) return null;
        providerId = matches[0];
      }
      if (!customProvider(providerId)) return null;
      const current = providers.get(providerId);
      if (current && text(current.name)) provider = text(current.name);
    } else return null;
    return { id: text(input.id), source, providerId, provider };
  };
  const refKey = ref => JSON.stringify([ref.source, ref.providerId, ref.id]);
  const poolSource = model => sourceKey(model.source) ||
    (model.providerId === 'opencode' || nameKey(model.provider) === 'opencode' ? 'zen' : 'remote');
  const poolByID = new Map(), offeringByID = new Map(), declaredByID = new Map();
  const append = (index, id, row) => {
    if (!id) return;
    if (!index.has(id)) index.set(id, []);
    index.get(id).push(row);
  };
  models.forEach(model => append(poolByID, text(model.id || model.ID), model));
  offerings.forEach(offering => append(offeringByID, text(offering.modelId), offering));
  providers.forEach((provider, providerId) => {
    if (!customProvider(providerId) || !Array.isArray(provider.modelIds)) return;
    provider.modelIds.forEach(id => append(declaredByID, text(id), { providerId, provider }));
  });
  const providerOffers = (provider, id) => Array.isArray(provider.modelIds) && provider.modelIds.some(model => text(model) === id);
  const targetPresent = ref => {
    if (ref.source === 'provider') {
      const provider = providers.get(ref.providerId);
      // ModelIDs are authoritative even for disabled providers. A legacy pool
      // row may belong to another same-name provider, so it cannot override them.
      if (provider && Array.isArray(provider.modelIds)) return providerOffers(provider, ref.id);
      return (offeringByID.get(ref.id) || []).some(offering => text(offering.providerId) === ref.providerId);
    }
    return (poolByID.get(ref.id) || []).some(model => poolSource(model) === ref.source) ||
      (offeringByID.get(ref.id) || []).some(offering => text(offering.providerId) === ref.providerId);
  };
  const confirmed = new Map();
  (Array.isArray(confirmedRemovedRefs) ? confirmedRemovedRefs : []).forEach(input => {
    const ref = normalize(input);
    if (ref) confirmed.set(refKey(ref), ref);
  });
  const removed = new Map(confirmed);
  (Array.isArray(completedRefs) ? completedRefs : []).forEach(input => {
    const ref = normalize(input);
    // Skipped targets can be recovered or manually protected. Only an absent
    // target is an idempotent retry whose stale local references need pruning.
    if (ref && (confirmed.has(refKey(ref)) || !targetPresent(ref))) removed.set(refKey(ref), ref);
  });
  if (!removed.size) return;

  const liveByID = new Map();
  const liveProviders = id => {
    if (liveByID.has(id)) return liveByID.get(id);
    const live = new Map(), rows = offeringByID.get(id) || [];
    rows.forEach(offering => {
      const providerId = text(offering.providerId), provider = providers.get(providerId);
      if (!customProvider(providerId) || !providerId) return;
      if (provider && Array.isArray(provider.modelIds) && !providerOffers(provider, id)) return;
      const enabled = offering.providerEnabled === undefined ? !!provider && provider.enabled !== false : offering.providerEnabled === true;
      if (enabled && !offering.delisted) live.set(providerId, provider || {});
    });
    (declaredByID.get(id) || []).forEach(({ providerId, provider }) => {
      if (provider.enabled === false) return;
      const offering = rows.find(row => text(row.providerId) === providerId);
      if (offering) {
        if (offering.delisted || offering.providerEnabled === false) return;
      } else {
        const sync = typeof _provSync !== 'undefined' ? _provSync[providerId] : null;
        if (sync && Array.isArray(sync.delistedModels)) {
          if (sync.delistedModels.indexOf(id) !== -1) return;
        } else {
          const owned = (poolByID.get(id) || []).filter(model => poolSource(model) === 'provider' &&
            (text(model.providerId) === providerId || !model.providerId && nameKey(model.provider) === nameKey(provider.name)));
          if (owned.length && owned.every(model => model.delisted)) return;
        }
      }
      live.set(providerId, provider);
    });
    liveByID.set(id, live);
    return live;
  };
  const rules = [], affectedProviders = new Set(), removedByProvider = new Map(), globallyRemoved = new Set();
  removed.forEach(ref => {
    const pool = poolByID.get(ref.id) || [], live = liveProviders(ref.id);
    // Match server cleanup: disabled persisted rows are still declared, and
    // custom rows survive delisting. Provider pins require an enabled offering.
    const globalLive = pool.some(model => model.custom || !model.delisted) || live.size > 0 ||
      (offeringByID.get(ref.id) || []).some(offering => !customProvider(text(offering.providerId)) && (offering.custom || !offering.delisted));
    const sourceLive = ref.source === 'provider' ? live.has(ref.providerId) :
      pool.some(model => poolSource(model) === ref.source && (model.custom || !model.delisted)) ||
      (offeringByID.get(ref.id) || []).some(offering => text(offering.providerId) === ref.providerId && (offering.custom || !offering.delisted));
    const nameLive = ref.provider && Array.from(live.values()).some(provider => nameKey(provider.name) === nameKey(ref.provider));
    rules.push({ ...ref, removeGlobal: !globalLive, removeSource: !sourceLive, removeName: !nameLive });
    affectedProviders.add(ref.providerId);
    if (!globalLive) globallyRemoved.add(ref.id);
    if (!sourceLive) {
      if (!removedByProvider.has(ref.providerId)) removedByProvider.set(ref.providerId, new Set());
      removedByProvider.get(ref.providerId).add(ref.id);
    }
  });
  affectedProviders.forEach(id => _providerModelRemovalEpochs.set(id, providerModelRemovalEpoch(id) + 1));

  const bareModel = value => {
    const model = text(value), at = model.lastIndexOf('@');
    return at > 0 ? { id: model.slice(0, at), pin: text(model.slice(at + 1)) } : { id: model, pin: '' };
  };
  const matches = (model, pin) => {
    const target = bareModel(model), explicitPin = text(pin) || target.pin;
    return rules.some(ref => target.id === ref.id && (ref.removeGlobal || ref.removeSource &&
      (explicitPin === ref.providerId || ref.removeName && ref.provider && nameKey(explicitPin) === nameKey(ref.provider))));
  };
  const entryMatches = entry => {
    const source = sourceKey(entry.source);
    const pin = source === 'remote' ? 'cline' : source === 'zen' ? 'opencode' : entry.providerId || entry.provider;
    return matches(entry.model || entry.id || entry.ID, pin);
  };
  const filterIDs = (ids, removedIDs) => ids.filter(id => !removedIDs.has(text(id)));
  const pruneCommaList = (value, shouldRemove) => {
    const ids = String(value || '').split(',');
    if (!ids.some(id => shouldRemove(text(id)))) return value;
    return ids.map(text).filter(id => id && !shouldRemove(id)).join(', ');
  };
  let formChanged = false;
  const editingProviderId = typeof _editingProviderId !== 'undefined' ? text(_editingProviderId) : '';
  const editorRemoved = removedByProvider.get(editingProviderId);
  if (editorRemoved) {
    const field = _('provModels');
    if (field) {
      const next = pruneCommaList(field.value, id => editorRemoved.has(id));
      if (next !== field.value) { field.value = next; formChanged = true; }
    }
    if (typeof _provBaseData !== 'undefined' && _provBaseData && Array.isArray(_provBaseData.modelIds)) {
      _provBaseData = { ..._provBaseData, modelIds: filterIDs(_provBaseData.modelIds, editorRemoved) };
    }
    if (typeof _provBaseJson !== 'undefined' && _provBaseJson) {
      try {
        const baseline = JSON.parse(_provBaseJson);
        if (baseline && typeof baseline.models === 'string') {
          baseline.models = pruneCommaList(baseline.models, id => editorRemoved.has(id));
          _provBaseJson = JSON.stringify(baseline);
        }
      } catch (e) {}
    }
    if (typeof _provPickerModels !== 'undefined') _provPickerModels = filterIDs(_provPickerModels, editorRemoved);
    if (typeof _provPickerOrder !== 'undefined') _provPickerOrder = filterIDs(_provPickerOrder, editorRemoved);
    editorRemoved.forEach(id => {
      if (typeof _provPickerTiers !== 'undefined') delete _provPickerTiers[id];
      if (typeof _provPickerExisting !== 'undefined') delete _provPickerExisting[id];
    });
    if (typeof _provPickerStats !== 'undefined') _provPickerStats = null;
    if (formChanged && typeof renderProvModelChips === 'function') renderProvModelChips();
    if (typeof renderProvPickerList === 'function') renderProvPickerList();
  }

  if (typeof _importPreviewCache !== 'undefined') {
    Object.keys(_importPreviewCache).forEach(key => {
      const hit = _importPreviewCache[key], batch = hit && hit.batch;
      const providerId = batch && text(batch.providerId) || (key.indexOf('id:') === 0 ? key.slice(3) : '');
      if (!affectedProviders.has(providerId)) return;
      if (typeof invalidateImportPreviewCache === 'function') invalidateImportPreviewCache(key);
      else delete _importPreviewCache[key];
    });
  }
  if (typeof _importBatch !== 'undefined' && _importBatch && affectedProviders.has(text(_importBatch.providerId))) {
    _importBatch = null;
    if (_('importPreviewModal') && typeof closeImportPreview === 'function') closeImportPreview();
  }

  let comboChanged = false;
  if (typeof _comboEntries !== 'undefined') {
    const next = _comboEntries.filter(entry => !entryMatches(entry));
    comboChanged = next.length !== _comboEntries.length;
    if (comboChanged) _comboEntries = next;
  }
  if (typeof _comboCatalog !== 'undefined') {
    const next = _comboCatalog.filter(model => !entryMatches(model));
    comboChanged = comboChanged || next.length !== _comboCatalog.length;
    _comboCatalog = next;
  }
  if (typeof _pickerOrder !== 'undefined') _pickerOrder = _pickerOrder.filter(id => !globallyRemoved.has(bareModel(id).id));
  if (comboChanged && typeof renderComboEntries === 'function') renderComboEntries();
  if (typeof renderComboPickerList === 'function' && _('comboPickerList')) renderComboPickerList();
  if (typeof _comboTestCopy !== 'undefined' && _comboTestCopy && Array.isArray(_comboTestCopy.entries)) {
    const entries = _comboTestCopy.entries, next = entries.filter(entry => !entryMatches(entry));
    if (next.length !== entries.length) {
      if (typeof _comboTestResults !== 'undefined') {
        const keptResults = [];
        let resultIndex = 0;
        entries.forEach(entry => {
          const result = _comboTestResults[resultIndex];
          if (entry.model && !entry.disabled && result && result.model === entry.model) {
            resultIndex++;
            if (!entryMatches(entry)) keptResults.push(result);
          }
        });
        _comboTestResults = keptResults;
      }
      _comboTestCopy.entries = next;
      if (typeof _comboTestChanged !== 'undefined') _comboTestChanged = true;
      if (typeof renderComboTestModal === 'function') renderComboTestModal();
    }
  }

  let aliasesChanged = false;
  document.querySelectorAll('#tunAliasesEditor .alias-row').forEach(row => {
    const target = row.querySelector('.se-target'), provider = row.querySelector('.se-provider');
    if (target && matches(target.value, provider && provider.value)) { row.remove(); aliasesChanged = true; }
  });
  const pruneAliases = aliases => aliases.filter(alias => !matches(alias.target, alias.provider));
  if (typeof _tunBaseCfg !== 'undefined' && _tunBaseCfg && Array.isArray(_tunBaseCfg.modelAliases)) {
    _tunBaseCfg = { ..._tunBaseCfg, modelAliases: pruneAliases(_tunBaseCfg.modelAliases) };
  }
  if (typeof _tunBaseJson !== 'undefined' && _tunBaseJson) {
    try {
      const baseline = JSON.parse(_tunBaseJson);
      if (baseline && Array.isArray(baseline.modelAliases)) {
        baseline.modelAliases = pruneAliases(baseline.modelAliases);
        _tunBaseJson = JSON.stringify(baseline);
      }
    } catch (e) {}
  }
  if (aliasesChanged && typeof checkAliasDupes === 'function') checkAliasDupes();
  const chain = _('settingModelChain');
  if (chain && chain.dataset.dirty) {
    const next = pruneCommaList(chain.value, id => matches(id, ''));
    if (next !== chain.value) { chain.value = next; formChanged = true; }
  }
  const datalist = _('modelIdList');
  if (datalist && globallyRemoved.size) {
    datalist.querySelectorAll('option').forEach(option => {
      if (globallyRemoved.has(text(option.value))) option.remove();
    });
    // The list now reflects this catalogue revision; avoid restoring pruned
    // options merely because removing a source changed the raw row count.
    datalist.dataset.filled = String(models.length);
  }
  if ((formChanged || aliasesChanged || editorRemoved) && typeof refreshSaveBar === 'function') refreshSaveBar();
}

// Combo selections identify an offering; stored entries retain the raw model ID.
const COMBO_OFFERING_KEY_PREFIX = 'offering:';

function comboRouteSource(source) {
  if (source === 'remote' || source === 'custom') return 'cline';
  if (source === 'seed' || source === 'opencode') return 'zen';
  return source || '';
}

function comboCatalogPin(model) {
  const source = comboRouteSource(model.source) || 'cline';
  return {
    source: source,
    provider: source === 'provider' ? (model.providerId || model.providerName || model.provider || '') : '',
    providerName: source === 'provider' ? (model.providerName || model.provider || '') : '',
  };
}

function comboOfferingKey(model) {
  const pin = comboCatalogPin(model);
  return COMBO_OFFERING_KEY_PREFIX + JSON.stringify([model.id, pin.source, pin.provider]);
}

function comboDecodeOfferingKey(value) {
  if (typeof value !== 'string' || !value.startsWith(COMBO_OFFERING_KEY_PREFIX)) return null;
  try {
    const tuple = JSON.parse(value.slice(COMBO_OFFERING_KEY_PREFIX.length));
    if (!Array.isArray(tuple) || tuple.length !== 3 || !tuple.every(part => typeof part === 'string')) return null;
    if (!['', 'cline', 'zen', 'provider'].includes(tuple[1])) return null;
    return { model: tuple[0], source: tuple[1], provider: tuple[1] === 'provider' ? tuple[2] : '' };
  } catch (e) { return null; }
}

function comboEntryRef(value) {
  if (value && typeof value === 'object') return value;
  return comboDecodeOfferingKey(value) || { model: value || '', source: '', provider: '' };
}

function comboCatalogEntry(value) {
  const entry = comboEntryRef(value), source = comboRouteSource(entry.source);
  let rows = (_comboCatalog || []).filter(model => model.id === entry.model);
  if (source) rows = rows.filter(model => comboCatalogPin(model).source === source);
  if (source === 'provider') {
    const pin = String(entry.provider || '');
    const exact = rows.filter(model => model.providerId && model.providerId === pin);
    if (exact.length) rows = exact;
    else {
      const prefixes = typeof _tunBaseCfg !== 'undefined' && _tunBaseCfg && _tunBaseCfg.providerModelPrefixes || {};
      // Stable identities stay identities even when their offering is absent.
      const idPin = pin === 'cline' || pin === 'opencode' ||
        pin.toLowerCase().startsWith('prov_') && !/[\p{White_Space}\p{Cc}]/u.test(pin) ||
        (_comboCatalog || []).some(model => model.providerId && model.providerId === pin) ||
        Object.values(prefixes).includes(pin);
      rows = idPin ? [] : rows.filter(model =>
        [model.providerName, model.provider].some(name => name && name.toLowerCase() === pin.toLowerCase()));
    }
  }
  const offerings = new Map(rows.map(model => [comboOfferingKey(model), model]));
  return offerings.size === 1 ? offerings.values().next().value : null;
}

function comboEntryKey(value) {
  const entry = comboEntryRef(value), source = comboRouteSource(entry.source);
  const row = source === 'provider' ? comboCatalogEntry(entry) : null;
  const provider = source === 'provider' ? (row && row.providerId || entry.provider || '') : '';
  return COMBO_OFFERING_KEY_PREFIX + JSON.stringify([entry.model || '', source, provider]);
}

function comboSelectionEntry(value) {
  const entry = comboEntryRef(value);
  const row = entry.source ? comboCatalogEntry(entry) : null;
  const pin = row ? comboCatalogPin(row) : entry;
  return { model: entry.model, disabled: false, source: pin.source || '', provider: pin.source === 'provider' ? pin.provider || '' : '' };
}

function comboPinKey(pin) {
  const source = comboRouteSource(pin && pin.source);
  return JSON.stringify([source, source === 'provider' ? (pin.provider || '') : '']);
}

function comboPinLabel(pin) {
  const source = comboRouteSource(pin && pin.source);
  if (!source) return t('自动');
  if (source === 'zen') return 'OpenCode Zen';
  if (source === 'cline') return 'Cline';
  if (pin.providerName) return pin.providerName;
  const row = (_comboCatalog || []).find(model => model.source === 'provider' && model.providerId && model.providerId === pin.provider);
  return row && (row.providerName || row.provider) || pin.provider || t('自定义');
}

function comboSourceName(model) {
  const source = comboRouteSource(model.source);
  if (source === 'provider') return model.providerName || model.provider || t('自定义');
  if (source === 'cline') return 'Cline';
  if (source === 'zen') return 'OpenCode Zen';
  return t('自定义');
}

function comboModelLabel(model) {
  return (model.qualifiedId || model.id) + '  ·  ' + comboSourceName(model) + '  ·  ' + (model.cost === 'free' ? t('免费') : t('付费'));
}

function comboEntryLabel(entry) {
  const model = entry.source ? comboCatalogEntry(entry) : null;
  return model && model.qualifiedId || entry.model;
}

function comboOptgroupKey(model) {
  const source = comboCatalogPin(model).source;
  if (source === 'provider') return 'prov:' + comboCatalogPin(model).provider;
  return source === 'cline' ? 'remote' : source === 'zen' ? 'zen' : 'custom';
}

function comboGroupLabel(model) {
  const name = comboSourceName(model);
  if (model.source !== 'provider' || !model.providerId) return name;
  const sameName = new Set((_comboCatalog || []).filter(row => row.source === 'provider' && comboSourceName(row) === name)
    .map(row => comboCatalogPin(row).provider));
  return sameName.size > 1 ? name + ' (' + model.providerId + ')' : name;
}

function comboGroupSortKey(a, b) {
  const order = key => key === 'remote' ? 0 : key === 'zen' ? 1 : key.startsWith('prov:') ? 2 : 3;
  return order(a) - order(b) || a.localeCompare(b);
}

function buildComboModelOptions(current, chosenMap) {
  const entry = comboEntryRef(current), currentKey = entry.model ? comboEntryKey(entry) : '';
  const cur = entry.model ? comboCatalogEntry(entry) : null;
  let html = '<option value=""' + (!entry.model ? ' selected' : '') + '>' + t('— 选择模型 —') + '</option>';
  // Preserve old automatic and unavailable pins until the operator chooses an offering.
  if (entry.model && (!entry.source || !cur || cur.disabled)) {
    const label = !entry.source ? entry.model + '  ·  ' + t('自动') : cur
      ? comboModelLabel(cur) + '  ·  ' + t('模型已停用')
      : entry.model + '  ·  ' + comboPinLabel(entry) + '  ·  ' + t('目录中未找到');
    html += '<option value="' + escAttr(currentKey) + '" selected>' + esc(label) + '</option>';
  }
  const groups = new Map(), seen = new Set();
  for (const model of _comboCatalog) {
    const key = comboOfferingKey(model);
    if (model.disabled || seen.has(key)) continue;
    seen.add(key);
    const groupKey = comboOptgroupKey(model);
    if (!groups.has(groupKey)) groups.set(groupKey, { label: comboGroupLabel(model), items: [] });
    groups.get(groupKey).items.push(model);
  }
  for (const groupKey of Array.from(groups.keys()).sort(comboGroupSortKey)) {
    const group = groups.get(groupKey);
    html += '<optgroup label="' + escAttr(group.label) + '">';
    group.items.sort((a, b) => a.id.localeCompare(b.id));
    for (const model of group.items) {
      const key = comboOfferingKey(model), used = chosenMap[key] && key !== currentKey;
      html += '<option value="' + escAttr(key) + '"' + (key === currentKey ? ' selected' : '') + (used ? ' disabled' : '') + '>' +
        esc(comboModelLabel(model)) + (used ? '  ·  ' + t('已使用') : '') + '</option>';
    }
    html += '</optgroup>';
  }
  return html;
}

function comboEntryBadges(value) {
  const entry = comboEntryRef(value);
  if (!entry.model) return '<span class="hint-xs">' + t('未选择模型') + '</span>';
  if (!entry.source) return '<span class="model-source-badge">' + t('自动') + '</span>';
  const model = comboCatalogEntry(entry), source = comboRouteSource(entry.source);
  const css = source === 'cline' ? 'remote' : source === 'zen' ? 'zen' : 'provider';
  let html = '<span class="model-source-badge ' + css + '">' + esc(comboPinLabel(model ? comboCatalogPin(model) : entry)) + '</span>';
  if (!model) return html + '<span class="delisted-badge warn-soft">' + t('目录中未找到') + '</span>';
  html += model.cost === 'free'
    ? '<span class="model-tag free tag-xs">' + t('免费') + '</span>'
    : '<span class="model-tag pass tag-xs">' + t('付费') + '</span>';
  if (model.disabled) html += '<span class="delisted-badge warn-soft">' + t('模型已停用') + '</span>';
  if (model.delisted) html += '<span class="delisted-badge">' + t('已下架') + '</span>';
  return html;
}

function comboEntryUpstreams(modelId) {
  const pins = new Map();
  for (const model of _comboCatalog) {
    if (model.id !== modelId) continue;
    const pin = comboCatalogPin(model);
    pins.set(comboPinKey(pin), pin);
  }
  const order = pin => pin.source === 'cline' ? 0 : pin.source === 'zen' ? 1 : 2;
  return Array.from(pins.values()).sort((a, b) => order(a) - order(b) || comboPinLabel(a).localeCompare(comboPinLabel(b)) || a.provider.localeCompare(b.provider));
}

function comboSourceSelectHtml(index, entry) {
  const upstreams = comboEntryUpstreams(entry.model);
  if (upstreams.length < 2 && !entry.source) return '';
  const model = entry.source ? comboCatalogEntry(entry) : null;
  const currentKey = comboPinKey(model ? comboCatalogPin(model) : entry);
  let options = '<option value=""' + (!entry.source ? ' selected' : '') + '>' + t('自动') + '</option>';
  if (entry.source && !upstreams.some(pin => comboPinKey(pin) === currentKey)) {
    options += '<option value="' + escAttr(currentKey) + '" selected>' + esc(comboPinLabel(entry)) + '  ·  ' + t('目录中未找到') + '</option>';
  }
  for (const pin of upstreams) {
    const value = pin.source === 'provider' ? comboPinKey(pin) : pin.source;
    const row = comboCatalogEntry({ model: entry.model, source: pin.source, provider: pin.provider });
    options += '<option value="' + escAttr(value) + '"' + (comboPinKey(pin) === currentKey ? ' selected' : '') + '>' + esc(row ? comboGroupLabel(row) : comboPinLabel(pin)) + '</option>';
  }
  return '<span class="combo-entry-source"><select title="' + t('上游') + '" onchange="onComboEntrySourceChange(' + index + ', this.value)">' + options + '</select></span>';
}

// ===== Routing tab: show which provider and upstream ID each alias resolves to =====
// Offerings come from GET /model-offerings (the same read model the Models page
// uses), so the preview never re-derives routing rules in the browser.
let _aliasPreviewOfferings = null;
let _aliasPreviewAt = 0;
let _aliasPreviewLoading = false;
const ALIAS_PREVIEW_TTL_MS = 30000;
const ALIAS_PREVIEW_MAX_LISTED = 3;

function aliasOfferingPrefix(qualifiedId) {
  const slash = String(qualifiedId || '').indexOf('/');
  return slash > 0 ? String(qualifiedId).slice(0, slash).toLowerCase() : '';
}
// Pure: match an alias target (a model name or a provider-qualified name) and an
// optional provider reference (ID, display name or namespace prefix) against the
// offering list. Returns { kind: 'empty' | 'none' | 'ok', matches: [...] }.
function resolveAliasPreview(target, providerRef, offerings) {
  target = String(target || '').trim();
  if (!target) return { kind: 'empty', matches: [] };
  const wanted = String(providerRef || '').trim().toLowerCase();
  let matches = (offerings || []).filter(o => o.qualifiedId === target || o.modelId === target);
  if (wanted) {
    matches = matches.filter(o => String(o.providerId || '').toLowerCase() === wanted ||
      String(o.providerName || '').toLowerCase() === wanted || aliasOfferingPrefix(o.qualifiedId) === wanted);
  }
  return { kind: matches.length ? 'ok' : 'none', matches: matches };
}
function aliasPreviewText(preview) {
  if (preview.kind === 'empty') return '';
  if (preview.kind === 'none') return t('未找到匹配的模型');
  const listed = preview.matches.slice(0, ALIAS_PREVIEW_MAX_LISTED).map(o =>
    (o.providerName || o.providerId || '') + ' · ' + (o.upstreamModelId || o.modelId));
  const more = preview.matches.length - listed.length;
  return '→ ' + listed.join(' ; ') + (more > 0 ? ' +' + more : '');
}
function refreshAliasPreviews() {
  document.querySelectorAll('#tunAliasesEditor .alias-row').forEach(row => {
    const out = row.querySelector('.se-preview');
    if (!out) return;
    const target = row.querySelector('.se-target'), provider = row.querySelector('.se-provider');
    const preview = resolveAliasPreview(target && target.value, provider && provider.value, _aliasPreviewOfferings);
    // Without loaded offerings there is nothing truthful to say, so stay blank.
    out.textContent = _aliasPreviewOfferings ? aliasPreviewText(preview) : '';
    if (out.classList) out.classList.toggle('se-preview-none', preview.kind === 'none');
  });
}
async function ensureAliasPreviewData() {
  if (_aliasPreviewLoading) return;
  if (_aliasPreviewOfferings && Date.now() - _aliasPreviewAt < ALIAS_PREVIEW_TTL_MS) return;
  _aliasPreviewLoading = true;
  try {
    const d = await api('GET', '/model-offerings');
    _aliasPreviewOfferings = (d.data && d.data.offerings) || [];
    _aliasPreviewAt = Date.now();
  } catch (e) {
    _aliasPreviewOfferings = null;
  } finally {
    _aliasPreviewLoading = false;
  }
  refreshAliasPreviews();
}

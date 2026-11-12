import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { test } from 'node:test';
import vm from 'node:vm';

// Usage & Quotas must show a provider that is refusing requests because one of
// its own limits is used up. A budget block is not a cooldown, so before this
// the page said "No cooldowns" for a provider the router was skipping.

const require = createRequire(import.meta.url);
const { parseDict } = require('./i18n_lib.js');
const source = readFileSync(new URL('../internal/admin/ui/js/17_quota.js', import.meta.url), 'utf8');

// Same escaping as 01_core.js: textContent to innerHTML, then quotes for attributes.
const esc = s => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const escAttr = s => esc(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');

function quotaPage() {
  const elements = new Map([['quotaList', { innerHTML: '' }], ['quotaSummary', { innerHTML: '' }]]);
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    t: key => key,
    esc, escAttr,
    formatNumber: n => Number(n).toLocaleString('en-US'),
    formatTokenCount: n => String(n),
    formatCooldown: iso => `cd:${iso}`,
    listEmptyState: (_icon, text) => `<empty>${text}</empty>`,
    ICON_MODELS: '',
    Date, Math, Number, String,
  });
  vm.runInContext(source, context);
  return {
    render(providers, health) {
      vm.runInContext('_quotaData = null', context);
      context.__data = { providers, health, stats: {} };
      vm.runInContext('_quotaData = __data; renderQuota();', context);
      return { cards: elements.get('quotaList').innerHTML, summary: elements.get('quotaSummary').innerHTML };
    },
    labels: () => vm.runInContext('QUOTA_BLOCK_LABELS', context),
  };
}

const inHours = h => new Date(Date.now() + h * 3600 * 1000).toISOString();
const base = { rpm: 0, daily: 0, tokens: 0, cooldowns: [], limits: {} };
const apinex = { id: 'prov_a', name: 'Apinex', enabled: true };

test('a provider at its daily token limit is shown as blocked with usage and a countdown', () => {
  const { cards } = quotaPage().render([apinex], {
    prov_a: { ...base, tokens: 3034885, limits: { tokens: 3000000, tokenWindow: 'daily' },
      blocks: [{ kind: 'daily_tokens', limit: 3000000, usage: 3034885, resetAt: inHours(21) }] },
  });
  assert.match(cards, /class="quota-blocked" role="status"/);
  assert.match(cards, /已被限额拦截/);
  assert.match(cards, /每日 Token 已达上限/);
  assert.match(cards, /3,034,885 \/ 3,000,000/);
  assert.match(cards, /恢复倒计时 20h/, 'countdown is shown, rounded down to whole minutes');
  assert.match(cards, /组合会改用下一个条目/);
  // Honest alongside it: this provider genuinely has no cooldown.
  assert.match(cards, /无冷却/);
});

test('a per-minute block names the model it applies to', () => {
  const { cards } = quotaPage().render([apinex], {
    prov_a: { ...base, blocks: [{ model: 'free/deepseek-v4.1-flash', kind: 'rpm', limit: 15, usage: 15, resetAt: new Date(Date.now() + 20000).toISOString() }] },
  });
  assert.match(cards, /每分钟请求数已达上限 <span class="mono">free\/deepseek-v4\.1-flash<\/span>/);
  assert.match(cards, /15 \/ 15/);
  assert.match(cards, /恢复倒计时 (1[0-9]|20)s/, 'a rate cap resets in seconds, not hours');
});

test('an unblocked provider shows no blocked panel and does not count as blocked', () => {
  const page = quotaPage().render([apinex], { prov_a: { ...base, blocks: [] } });
  assert.doesNotMatch(page.cards, /quota-blocked/);
  assert.match(page.summary, /<div class="quota-stat-n">0<\/div><div class="quota-stat-l">受限 Provider/);
});

test('the summary counts blocked providers, not blocks', () => {
  const two = { kind: 'daily_requests', limit: 1, usage: 1, resetAt: inHours(1) };
  const page = quotaPage().render(
    [apinex, { id: 'prov_b', name: 'B', enabled: true }, { id: 'prov_c', name: 'C', enabled: true }],
    { prov_a: { ...base, blocks: [two, { ...two, kind: 'daily_tokens' }] }, prov_b: { ...base, blocks: [two] }, prov_c: { ...base, blocks: [] } },
  );
  assert.match(page.summary, /<div class="quota-stat-n">2<\/div><div class="quota-stat-l">受限 Provider/);
});

test('a block and a cooldown are both shown, block first', () => {
  const { cards } = quotaPage().render([apinex], {
    prov_a: { ...base, cooldowns: [{ model: 'm1', until: inHours(1) }],
      blocks: [{ kind: 'daily_requests', limit: 5, usage: 5, resetAt: inHours(2) }] },
  });
  assert.ok(cards.indexOf('quota-blocked') > 0 && cards.indexOf('quota-blocked') < cards.indexOf('quota-cooldowns'));
  assert.match(cards, /quota-cd-row/);
  assert.doesNotMatch(cards, /无冷却/);
});

test('a reset moment that has passed reads as recovering soon', () => {
  const { cards } = quotaPage().render([apinex], {
    prov_a: { ...base, blocks: [{ kind: 'daily_tokens', limit: 1, usage: 1, resetAt: new Date(Date.now() - 5000).toISOString() }] },
  });
  assert.match(cards, /即将恢复/);
  assert.doesNotMatch(cards, /恢复倒计时/);
});

test('a snapshot without blocks (older server) still renders', () => {
  for (const blocks of [undefined, null]) {
    const { cards, summary } = quotaPage().render([apinex], { prov_a: { ...base, blocks } });
    assert.doesNotMatch(cards, /quota-blocked/);
    assert.match(summary, /受限 Provider/);
  }
});

test('model names and unknown kinds are escaped, never injected as markup', () => {
  const { cards } = quotaPage().render([apinex], {
    prov_a: { ...base, blocks: [{ model: '<img src=x onerror=alert(1)>', kind: 'rpm', limit: 1, usage: 1, resetAt: inHours(0.01) },
      { kind: '<script>alert(2)</script>', limit: 1, usage: 1, resetAt: inHours(1) }] },
  });
  assert.doesNotMatch(cards, /<img|<script/);
  assert.match(cards, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.match(cards, /&lt;script&gt;alert\(2\)&lt;\/script&gt;/, 'an unknown kind falls back to its raw (escaped) name');
});

test('every block label has an English translation, and the kinds match the server', () => {
  const dict = parseDict().keys;
  const labels = quotaPage().labels();
  for (const [kind, key] of Object.entries(labels)) {
    assert.ok(dict.has(key), `${kind}: missing dictionary key ${key}`);
    assert.ok(dict.get(key).value.length > 0);
  }
  // internal/budget/backend.go Exhaustion() produces exactly these kinds.
  assert.deepEqual(Object.keys(labels).sort(), ['daily_requests', 'daily_tokens', 'monthly_tokens', 'rpm']);
  const goSource = readFileSync(new URL('../internal/budget/backend.go', import.meta.url), 'utf8');
  for (const kind of ['"rpm"', '"daily_requests"', 'policy.window() + "_tokens"']) {
    assert.ok(goSource.includes(kind), `budget.Exhaustion no longer produces ${kind}`);
  }
});

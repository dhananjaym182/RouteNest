import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { test } from 'node:test';

// Colour tokens are defined once, in 00_base.css (no later layer overrides them). This gate
// resolves them per theme and fails if body text, muted text, accent text or status text drop
// below WCAG AA on any surface, if the text tiers collapse into one flat grey, or if the
// RightStockAI brand palette (share-market-app globals.css / brand.config.ts) drifts.
const uiDir = new URL('../internal/admin/ui/', import.meta.url);
const css = name => readFileSync(new URL('css/' + name, uiDir), 'utf8');
const base = css('00_base.css');

function tokens(selector) {
  const start = base.indexOf(selector + '{');
  assert.ok(start >= 0, 'missing ' + selector);
  const body = base.slice(start + selector.length + 1, base.indexOf('\n}', start));
  const out = {};
  for (const [, name, value] of body.matchAll(/--([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{6})\b/g)) out[name] = value.toUpperCase();
  return out;
}
const lum = hex => {
  const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(c => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((m, n) => n - m); return (x + 0.05) / (y + 0.05); };

const themes = { light: tokens(':root'), dark: tokens('[data-theme="dark"]') };

for (const [name, t] of Object.entries(themes)) {
  test(`${name}: text, muted text, accent and status colours clear AA on every surface`, () => {
    for (const surface of ['bg', 'surface', 'surface2', 'surface3']) {
      for (const ink of ['text', 'text2', 'text3', 'accent', 'green', 'red', 'yellow']) {
        const r = ratio(t[ink], t[surface]);
        assert.ok(r >= 4.5, `${name} ${ink} ${t[ink]} on ${surface} ${t[surface]} = ${r.toFixed(2)}`);
      }
    }
  });

  test(`${name}: text tiers stay visibly distinct (not one flat grey)`, () => {
    const [a, b, c] = ['text', 'text2', 'text3'].map(k => ratio(t[k], t.surface));
    assert.ok(a - b >= 3, `text -> text2 step ${(a - b).toFixed(2)}`);
    assert.ok(b - c >= 2.5, `text2 -> text3 step ${(b - c).toFixed(2)}`);
  });

  test(`${name}: primary button ink is readable on the accent fill`, () => {
    const ink = name === 'dark' ? t.bg : '#FFFFFF'; // --on-button-accent
    assert.ok(ratio(ink, t.accent) >= 4.5, `ink ${ink} on accent ${t.accent} = ${ratio(ink, t.accent).toFixed(2)}`);
  });
}

test('accent, text and surfaces are the RightStockAI brand palette', () => {
  const { light, dark } = themes;
  assert.equal(light.accent, '#673DE6');        // brand-600
  assert.equal(light['accent-hover'], '#471EA7'); // brand-700
  assert.equal(dark.accent, '#9D99FF');         // brand-400
  assert.equal(dark['accent-hover'], '#BCBDFF'); // brand-300
  assert.equal(light.text, '#101828');          // gray-900
  assert.equal(dark.text, '#F9FAFB');           // gray-50
  assert.equal(dark.surface, '#101828');        // gray-900
  assert.equal(dark.surface2, '#1D2939');       // gray-800
  assert.equal(light.bg, '#F7F8FA');            // app page canvas
});

test('no layer re-declares colour tokens, and the retired navy/violet palette is gone', () => {
  const OLD = /#(0b1020|f7f8fc|7968f8|6655e8|5b8cff|182238|2a3854|111a2d|4778e8|182033|7e6ef8|5a47e0)\b/i;
  for (const file of readdirSync(new URL('css/', uiDir)).filter(f => f.endsWith('.css'))) {
    const source = css(file);
    assert.doesNotMatch(source.replace(/\/\*[\s\S]*?\*\//g, ''), OLD, file + ' still hard-codes the retired palette');
    if (file !== '00_base.css') {
      assert.doesNotMatch(source.replace(/\/\*[\s\S]*?\*\//g, ''), /^\s*--(text|text2|text3|accent|accent-hover|green|red|yellow|bg|surface[23]?)\s*:/m, file + ' re-declares a colour token');
    }
  }
  const shell = readFileSync(new URL('js/03_shell.js', uiDir), 'utf8');
  assert.match(shell, /'#0C111D' : '#F7F8FA'/);
});

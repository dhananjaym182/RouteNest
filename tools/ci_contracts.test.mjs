import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { test } from 'node:test';

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8');

// CI failure: the container listens on 0.0.0.0, which the server refuses unless trusted-proxy mode,
// a canonical URL and trusted CIDRs are all configured. Both container starts must pass them.
test('docker smoke test starts every container with the trusted-proxy settings the server requires', () => {
  const script = read('scripts/docker-smoke.sh');
  assert.match(script, /ROUTENEST_ADMIN_CANONICAL_URL="http:\/\/127\.0\.0\.1:\$PORT"/);
  assert.match(script, /ROUTENEST_ADMIN_PROXY_MODE=true/);
  assert.match(script, /ROUTENEST_ADMIN_TRUSTED_PROXY_CIDRS="\$BRIDGE_GW\/32"/);
  assert.match(script, /docker network inspect bridge/, 'the trusted peer is the real bridge gateway, not a wide range');
  assert.doesNotMatch(script, /TRUSTED_PROXY_CIDRS="?(0\.0\.0\.0\/0|172\.16\.0\.0\/12|10\.0\.0\.0\/8)/, 'never trust a shared network');
  const starts = script.match(/docker run -d[^\n]*\\\n(?:[^\n]*\\\n)*[^\n]*IMAGE" >\/dev\/null/g) || [];
  assert.equal(starts.length, 2, 'the script starts the container twice (before and after the rebuild)');
  for (const start of starts) assert.match(start, /"\$\{ADMIN_ENV\[@\]\}"/, 'each start must use the shared admin environment');
});

// CI failure: the focus check used #refreshAllBtn, which the UI hides (display:none) below 900px.
test('navigation focus check does not depend on a control that is hidden on narrow screens', () => {
  const css = read('internal/admin/ui/css/100_ui_optimization.css').replace(/\/\*[\s\S]*?\*\//g, '');
  assert.match(css, /max-width:900px\)\{[^}]*\}?[\s\S]*?#refreshAllBtn\{display:none\}/, 'precondition: the refresh button is hidden on mobile');
  const script = read('tools/admin_navigation_verify.mjs');
  assert.match(script, /\['refreshAllBtn', 'mobileNavBtn'\]/);
  assert.match(script, /getClientRects\(\)\.length > 0/);
  assert.doesNotMatch(script, /page\.locator\('#refreshAllBtn'\)/, 'must not focus the refresh button unconditionally');
});

// CI failure: the Windows race job threw because gcc was not at one hard-coded path.
test('the Windows race job finds the C compiler instead of assuming one path', () => {
  const workflow = read('.github/workflows/test.yml');
  assert.match(workflow, /Get-Command gcc\.exe/);
  assert.match(workflow, /ucrt64\\bin/);
  assert.match(workflow, /gcc\.exe is not on PATH and was not found in/);
});

// CI failures: the i18n gate's HTML scanner stops at '>' inside attributes (so '=>' in a handler is read
// as text), and the Go handler-target test reads 'function (' as a call to an undeclared function.
// Inline handlers therefore only call named functions; any logic lives in the scripts.
test('inline handlers in markup contain no arrow functions or function expressions', () => {
  for (const dir of ['html/pages', 'html/dialogs']) {
    for (const file of readdirSync(new URL('../internal/admin/ui/' + dir + '/', import.meta.url))) {
      const html = read('internal/admin/ui/' + dir + '/' + file);
      for (const match of html.matchAll(/\son[a-z]+="([^"]*)"/g)) {
        assert.doesNotMatch(match[1], /=>|\bfunction\b/, `${dir}/${file}: ${match[1].slice(0, 70)}`);
      }
    }
  }
});

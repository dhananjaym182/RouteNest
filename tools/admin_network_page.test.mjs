// Run with: node --test tools/admin_network_page.test.mjs
// Global Egress Proxy (#network): the page must read as applying to every provider, and its hints
// must match what the egress code does. Static checks on the shipped assets (no browser).
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const ui = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');
const html = ui('html/pages/network.html');
const shell = ui('html/shell.html');
const i18nSource = ui('js/01_i18n.js');
const dictionary = new Function(i18nSource.slice(i18nSource.indexOf('const I18N = {'), i18nSource.indexOf('\n};', i18nSource.indexOf('const I18N = {')) + 3).replace('const I18N =', 'return'))();

const text = fragment => fragment.replace(/<[^>]+>/g, '').trim();
const between = (source, start, end) => source.slice(source.indexOf(start), source.indexOf(end, source.indexOf(start)));

test('sidebar entry, page title and route all use the global name and keep the #network route', () => {
  assert.match(shell, /data-tab="network"[\s\S]{0,400}?class="nav-label">全局出口代理</);
  assert.match(html, /class="large-title">全局出口代理</);
  assert.match(html, /id="tab-network"/);
  assert.doesNotMatch(shell + html, /网络与出口/, 'the old name is gone from the shell and the page');
  assert.equal(dictionary['全局出口代理'], 'Global Egress Proxy');
  assert.ok(!('网络与出口' in dictionary), 'no orphaned entry for the old name');
});

test('the copy says the pool applies to all providers and names its exceptions', () => {
  const subtitle = text(html.match(/class="large-subtitle">([^<]+)</)[1]);
  assert.match(subtitle, /所有 Provider/);
  assert.match(subtitle, /单独覆盖/);

  const desc = text(between(html, '默认出口代理</div>', '<div class="section-body">'));
  assert.match(desc, /所有 Provider 的请求默认使用此代理池/);
  assert.match(desc, /专属代理（或填「-」直连）时不使用/, 'provider override and "-" direct');
  assert.match(desc, /OpenCode Zen 使用自己的代理池/);
  assert.match(desc, /localhost 和内网 IP 地址保持直连/);
  assert.doesNotMatch(desc, /自定义 Provider/, 'the old custom-provider-only wording is gone');
});

test('hints state per-connection rotation, failover, and the hostname-to-private-address caveat', () => {
  const body = text(between(html, 'id="clineProxyStrategy"', 'id="clineProxySaveResult"'));
  assert.match(body, /按新建连接选择，而不是按请求/);
  assert.match(body, /自动改用列表中的下一个/);
  assert.match(body, /30 秒/);
  assert.match(body, /ollama\.lan[\s\S]*填「-」设为直连/);
});

test('every new string has an English entry and the provider-filter note is accurate', () => {
  const strings = [
    html.match(/class="large-subtitle">([^<]+)</)[1],
    ...[...html.matchAll(/<div class="(?:section-desc|field-hint)">([^<]+)<\/div>/g)].map(m => m[1]),
    ...[...ui('html/pages/about.html').matchAll(/<strong>全局出口代理<\/strong><span>([^<]+)<\/span>/g)].map(m => m[1]),
  ].filter(s => /[\u4e00-\u9fff]/.test(s));
  assert.ok(strings.length >= 6);
  for (const s of strings) {
    // The one hint that ends with a link is split into a text node and an anchor; its text-node key is pinned already.
    assert.ok(s in dictionary || s.startsWith('仅决定多个出口代理地址之间的选择'), 'missing English for: ' + s);
  }
  const note = '未设置专属代理的 Provider 使用默认出口代理；当前接口不提供各 Provider 的绑定，下方显示全局清单。';
  assert.ok(ui('js/18_global.js').includes("t('" + note + "')"));
  assert.ok(note in dictionary);
  assert.ok(!ui('js/18_global.js').includes('当前接口未提供 Provider'));
});

test('no message points users to the removed "上游服务 → opencode 出口代理" or "Network & Egress" location', () => {
  for (const file of ['js/01_core.js', '../../zen/zen_sync.go', '../../zen/zen_client.go']) {
    const source = ui(file);
    assert.doesNotMatch(source.replace(/\(\?:上游服务 → opencode\|/g, ''), /上游服务 → opencode 出口代理/, file + ' still names the removed location');
  }
  assert.match(ui('js/01_core.js'), /Providers → OpenCode Zen → egress proxy/);
  assert.match(readFileSync(new URL('../internal/zen/zen_sync.go', import.meta.url), 'utf8'), /Providers → OpenCode Zen → 出口代理/);
});

test('the Providers editor and this page use the same words for inherit and direct', () => {
  const editor = ui('html/dialogs/provider_editor.html');
  assert.match(editor, /空=继承全局；- 表示直连/);
  assert.match(html, /填「-」直连/);
});

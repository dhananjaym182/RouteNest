import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

const ui = name => readFileSync(new URL('../internal/admin/ui/' + name, import.meta.url), 'utf8');
const dictionarySource = ui('js/01_i18n.js');
const dictionaryContext = vm.createContext({});
vm.runInContext(dictionarySource, dictionaryContext);
const dictionary = vm.runInContext('I18N', dictionaryContext);
const escapeHTML = value => String(value == null ? '' : value).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]));
const now = Date.UTC(2026, 10, 11, 12);
const at = minutes => new Date(now + minutes * 60000).toISOString();
const zero = '0001-01-01T00:00:00Z';
const instances = [{ id: 'a', name: 'Alpha', enabled: true }, { id: 'b', name: 'Beta', enabled: true }, { id: 'c', name: 'Disabled', enabled: false }];
const observation = (kind, providerId = 'a', extra = {}) => ({ kind, scope: { providerId }, source: 'status', confidence: 'explicit', ...extra });
const snapshot = extra => ({ instances, state: [], credentials: [], credentialsState: 'ok', fetchedAt: at(0), ...extra });

function createUI({ permission = true, api: apiImpl, lang = 'zh' } = {}) {
  const elements = new Map();
  const field = () => ({
    innerHTML: '', textContent: '', value: '', hidden: false, disabled: false, attributes: {}, classes: new Set(),
    setAttribute(name, value) { this.attributes[name] = value; },
    classList: { toggle() {} },
  });
  for (const id of ['tab-health', 'globalHealthRefresh', 'globalHealthUpdated', 'globalHealthError', 'globalHealthSummary', 'globalState', 'globalHealthRuntime', 'globalHealthCredentialNote', 'globalHealthCredentialIssues', 'globalHealthCredentials', 'globalHealthCredentialCount', 'globalHealthObservationCount', 'globalHealthProviderFilter', 'globalHealthKindFilter']) {
    const element = field();
    element.classList.toggle = (name, active) => active ? element.classes.add(name) : element.classes.delete(name);
    elements.set(id, element);
  }
  const calls = [];
  const fixedDate = class extends Date {
    constructor(...args) { super(...(args.length ? args : [now])); }
    static now() { return now; }
  };
  const context = vm.createContext({
    _: id => elements.get(id) || null,
    t: value => lang === 'en' ? dictionary[value] || value : value,
    esc: escapeHTML, escAttr: escapeHTML, LC: () => lang === 'en' ? 'en-US' : 'zh-CN',
    formatNumber: value => String(value), formatCooldown: () => 'cooldown',
    adminHasPermission: name => name !== 'credential.write' || permission,
    Date: fixedDate,
    api: async (method, path) => {
      calls.push({ method, path });
      return apiImpl ? apiImpl(method, path) : { data: path === '/provider-instances' ? { instances } : path === '/provider-state' ? { observations: [] } : { credentials: [] } };
    },
    renderGlobalInstances() {}, renderGlobalCredentials() {}, renderGlobalOfferings() {}, renderGlobalEgress() {}, renderGlobalSummary() {},
  });
  vm.runInContext(ui('js/18_global.js'), context, { filename: '18_global.js' });
  const run = source => vm.runInContext(source, context);
  return {
    context, elements, calls, run,
    html: id => elements.get(id).innerHTML,
    json: source => JSON.parse(run('JSON.stringify(' + source + ')')),
    load: data => { context.fixture = data; run('_globalHealthData = fixture; renderGlobalHealth(fixture)'); },
  };
}

function response(path, data = snapshot()) {
  if (path === '/provider-instances') return { data: { instances: data.instances } };
  if (path === '/provider-state') return { data: { observations: data.state, runtimeState: data.runtimeState } };
  if (path === '/credentials') return { data: { credentials: data.credentials } };
  throw new Error('Unexpected health request: ' + path);
}

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((accept, fail) => { resolve = accept; reject = fail; });
  return { promise, resolve, reject };
};

test('health loads only its own projections, with authorized credentials optional', async () => {
  const page = createUI();
  await page.run('loadGlobalHealth()');
  assert.deepEqual(page.calls.map(call => call.path).sort(), ['/credentials', '/provider-instances', '/provider-state']);
  assert.ok(page.calls.every(call => call.method === 'GET'));
  assert.equal(page.elements.get('globalHealthRefresh').disabled, false);
  assert.equal(page.elements.get('tab-health').attributes['aria-busy'], 'false');
  assert.match(page.html('globalHealthUpdated'), /快照时间.*<time datetime=/);
  assert.equal(page.json('_globalData'), null, 'health does not publish incomplete data into the shared provider cache');
});

test('loading is announced without showing counts as zero', async () => {
  const pending = deferred();
  const page = createUI({ api: () => pending.promise });
  const request = page.run('loadGlobalHealth()');
  assert.equal(page.elements.get('globalHealthRefresh').disabled, true);
  assert.equal(page.elements.get('tab-health').attributes['aria-busy'], 'true');
  assert.equal((page.html('globalHealthSummary').match(/加载中…/g) || []).length, 4);
  assert.doesNotMatch(page.html('globalHealthSummary'), />0</);
  pending.resolve({ data: { instances: [], observations: [], credentials: [] } });
  await request;
  assert.equal(page.elements.get('globalHealthRefresh').disabled, false);
});

test('credential permission is identified as unavailable to the role, without a forbidden request', async () => {
  const page = createUI({ permission: false });
  await page.run('loadGlobalHealth()');
  assert.ok(!page.calls.some(call => call.path === '/credentials'));
  assert.match(page.html('globalHealthSummary'), /需关注凭据.*无权限查看/s);
  assert.doesNotMatch(page.html('globalHealthSummary'), /未知/);
  assert.equal(page.elements.get('globalHealthCredentialNote').hidden, false);
  assert.match(page.elements.get('globalHealthCredentialNote').textContent, /凭据管理权限/);
  assert.equal(page.elements.get('globalHealthCredentialIssues').hidden, true);
});

test('credential read failure preserves observations and gives an inline explanation', async () => {
  const data = snapshot({ state: [observation('rate_limited', 'a', { until: at(5) })] });
  const page = createUI({ api: (_method, path) => {
    if (path === '/credentials') throw new Error('permission <b>denied</b>');
    return response(path, data);
  } });
  await page.run('loadGlobalHealth()');
  assert.match(page.html('globalState'), /Alpha/);
  assert.match(page.html('globalHealthSummary'), /需关注凭据.*不可用/s);
  assert.doesNotMatch(page.html('globalHealthSummary'), /未知/);
  assert.equal(page.elements.get('globalHealthError').hidden, true);
  assert.match(page.elements.get('globalHealthCredentialNote').textContent, /其他健康信息仍可查看/);
  assert.equal(page.json('_globalHealthData.credentialsState'), 'failed');
});

test('failed first load shows an escaped error with retry, never an empty or healthy state', async () => {
  const page = createUI({ api: (_method, path) => {
    if (path === '/provider-state') throw new Error('store <script>down</script>');
    return response(path);
  } });
  await page.run('loadGlobalHealth()');
  assert.equal(page.elements.get('globalHealthError').hidden, false);
  assert.match(page.html('globalHealthError'), /store &lt;script&gt;down&lt;\/script&gt;/);
  assert.match(page.html('globalHealthError'), /onclick="loadGlobalHealth\(\)"/);
  assert.equal(page.html('globalHealthSummary'), '');
  assert.equal(page.html('globalState'), '');
  assert.equal(page.elements.get('globalHealthRefresh').disabled, false);
});

test('malformed successful responses are not silently interpreted as zero data', async () => {
  for (const payload of [{}, { data: {} }, { data: { observations: 'unavailable' } }]) {
    const page = createUI({ api: (_method, path) => path === '/provider-state' ? payload : response(path) });
    await page.run('loadGlobalHealth()');
    assert.match(page.html('globalHealthError'), /健康快照格式无效/);
    assert.equal(page.html('globalHealthSummary'), '');
  }
  const empty = createUI({ api: (_method, path) => path === '/provider-state' ? { data: { observations: null } } : response(path) });
  await empty.run('loadGlobalHealth()');
  assert.equal(empty.elements.get('globalHealthError').hidden, true, 'Go nil slices represent an actual empty list');
});

test('failed refresh retains the previous snapshot and a successful retry clears failure', async () => {
  let fail = false;
  const data = snapshot({ state: [observation('auth_expired')] });
  const page = createUI({ api: (_method, path) => {
    if (fail && path === '/provider-state') throw new Error('offline');
    return response(path, data);
  } });
  await page.run('loadGlobalHealth()');
  const before = page.html('globalState');
  fail = true;
  await page.run('loadGlobalHealth()');
  assert.equal(page.html('globalState'), before);
  assert.match(page.html('globalHealthUpdated'), /刷新失败，显示上次快照/);
  assert.equal(page.elements.get('globalHealthError').hidden, false);
  fail = false;
  await page.run('loadGlobalHealth()');
  assert.equal(page.elements.get('globalHealthError').hidden, true);
  assert.doesNotMatch(page.html('globalHealthUpdated'), /刷新失败/);
});

test('older successful or failed requests cannot replace the newest health snapshot', async () => {
  for (const failOld of [false, true]) {
    const pending = deferred();
    let stateCalls = 0;
    const latest = snapshot({ state: [observation('model_gone', 'b')] });
    const page = createUI({ api: (_method, path) => {
      if (path === '/provider-state' && ++stateCalls === 1) return pending.promise;
      return response(path, latest);
    } });
    const old = page.run('loadGlobalHealth()');
    await page.run('loadGlobalHealth()');
    if (failOld) pending.reject(new Error('old failure'));
    else pending.resolve({ data: { observations: [observation('auth_expired', 'a')] } });
    await old;
    assert.match(page.html('globalState'), /Beta/);
    assert.doesNotMatch(page.html('globalState'), /Alpha/);
    assert.equal(page.elements.get('globalHealthError').hidden, true);
  }
});

test('summary counts affected providers once, and active cooldowns follow backend expiry semantics', () => {
  const page = createUI();
  page.load(snapshot({ state: [
    observation('auth_expired'),
    observation('quota_exhausted', 'a', { until: at(-2), resetAt: at(10) }),
    observation('rate_limited', 'b', { until: at(-2), resetAt: at(10) }),
    observation('rate_limited', 'a', { until: at(4) }),
    observation('quota_reset', 'b'),
    observation('recovered', 'b'),
  ] }));
  const summary = page.html('globalHealthSummary');
  assert.match(summary, /已启用 Providers<\/span><strong data-numeric>2</);
  assert.match(summary, /观察涉及 Provider<\/span><strong data-numeric>2</);
  assert.match(summary, /有效冷却<\/span><strong data-numeric>2</);
});

test('credential attention includes model cooldowns and nonrefreshable expiry but excludes deliberately disabled credentials', () => {
  const page = createUI();
  const credential = (id, extra = {}) => ({ id, providerIds: ['a'], status: 'active', kind: 'api_key', secretConfigured: true, ...extra });
  page.load(snapshot({ credentials: [
    credential('active'), credential('model', { modelCooldowns: { m: at(5), old: at(-5) } }),
    credential('expiry', { expiresAt: at(-1) }), credential('refreshable', { expiresAt: at(-1), refreshable: true }),
    credential('disabled', { status: 'disabled', cooldownUntil: at(5), secretConfigured: false }),
    credential('expired', { status: 'expired' }), credential('reauth', { status: 'reauth_required' }),
    credential('missing', { secretConfigured: false }), credential('cooldown', { cooldownUntil: at(5) }),
    credential('public', { kind: 'public', secretConfigured: false }), credential('zero', { expiresAt: zero, cooldownUntil: zero }),
  ] }));
  assert.equal(page.json('globalHealthCredentialIssues(_globalHealthData).length'), 6);
  assert.equal(page.elements.get('globalHealthCredentialCount').textContent, '6');
  assert.match(page.html('globalHealthCredentials'), /模型冷却/);
  assert.match(page.html('globalHealthCredentials'), /需要重新认证/);
  assert.doesNotMatch(page.html('globalHealthCredentials'), /disabled|refreshable|<code>old<\/code>/);
});

test('provider, issue and search filters compose while summary retains the provider scope', () => {
  const page = createUI();
  page.load(snapshot({ state: [observation('auth_expired'), observation('rate_limited', 'a', { scope: { providerId: 'a', modelId: 'vendor/model' }, until: at(5) }), observation('rate_limited', 'b', { until: at(4) })] }));
  page.run('setGlobalFilter("a"); setGlobalHealthKind("rate_limited"); setGlobalHealthSearch("vendor/model")');
  assert.equal(page.elements.get('globalHealthObservationCount').textContent, '已显示 1 / 2');
  assert.match(page.html('globalState'), /vendor\/model/);
  assert.doesNotMatch(page.html('globalState'), /Beta|认证过期/);
  assert.match(page.html('globalHealthSummary'), /观察涉及 Provider<\/span><strong data-numeric>1</);
  page.run('setGlobalHealthSearch("no-match")');
  assert.match(page.html('globalState'), /没有匹配的观察/);
  assert.doesNotMatch(page.html('globalState'), /暂无可用性观察/);
  assert.equal(page.elements.get('globalHealthProviderFilter').value, 'a');
});

test('observations use one compact table with explicit headers and complete disclosure', () => {
  const page = createUI();
  page.load(snapshot({ state: [observation('rate_limited', 'a', { scope: { providerId: 'a', credentialId: 'key:a', modelId: 'vendor/model', egressId: 'egress:redacted' }, until: at(5), source: 'retry-after' })] }));
  const html = page.html('globalState');
  assert.equal((html.match(/<table /g) || []).length, 1);
  assert.equal((html.match(/<th scope="col"/g) || []).length, 6);
  assert.doesNotMatch(html, /observation-card|observation-time/);
  for (const value of ['key:a', 'vendor/model', 'egress:redacted', 'retry-after', '查看证据', '未确认重置', '没有上游重置证据']) assert.ok(html.includes(value), value);
  assert.match(html, /<details class="health-evidence">/);
  assert.match(html, /<summary aria-label="详情 · 查看证据/);
  assert.match(html, /<time datetime=/);
});

test('authentication and delisting issues appear first, followed by the soonest timed issues', () => {
  const page = createUI();
  page.load(snapshot({ state: [observation('rate_limited', 'a', { until: at(10) }), observation('quota_exhausted', 'a', { until: at(3) }), observation('model_gone', 'a'), observation('auth_expired', 'b')] }));
  const html = page.html('globalState');
  assert.ok(html.indexOf('认证过期') < html.indexOf('模型下架'));
  assert.ok(html.indexOf('模型下架') < html.indexOf('配额耗尽'));
  assert.ok(html.indexOf('配额耗尽') < html.indexOf('限流'));
});

test('fallback, configured windows and Retry-After are never presented as confirmed upstream resets', () => {
  const page = createUI();
  const evidence = data => { page.context.observation = data; return page.json('globalHealthResetEvidence(observation)'); };
  assert.equal(evidence(observation('quota_exhausted', 'a', { until: at(5), confidence: 'fallback', source: 'fallback' })).label, '未确认重置');
  assert.equal(evidence(observation('quota_exhausted', 'a', { resetAt: at(5), source: 'configured_window' })).label, '配置窗口重置');
  assert.equal(evidence(observation('quota_exhausted', 'a', { resetAt: at(5), source: 'retry-after' })).label, '建议重试时间');
  assert.equal(evidence(observation('quota_exhausted', 'a', { resetAt: at(5), source: 'reset-header' })).label, '上游确认重置');
  assert.equal(evidence(observation('quota_exhausted', 'a', { resetAt: at(5), source: 'body', confidence: 'inferred' })).label, '推断重置');
  assert.equal(evidence(observation('quota_exhausted', 'a', { resetAt: at(5), source: 'future-source', confidence: 'fallback' })).tone, 'neutral');
  page.load(snapshot({ state: [observation('auth_expired', 'a', { until: zero, resetAt: zero })] }));
  assert.match(page.html('globalState'), /不适用/);
  assert.doesNotMatch(page.html('globalState'), /1\/1\/1|0001|1970/);
});

test('elapsed cooldowns remain unverified, not labelled recovered', () => {
  const page = createUI();
  page.load(snapshot({ state: [observation('rate_limited', 'a', { until: at(-1) })] }));
  assert.match(page.html('globalState'), /is-expired/);
  assert.match(page.html('globalState'), /已到期，待刷新/);
  assert.doesNotMatch(page.html('globalState'), /即将恢复|已恢复/);
});

test('runtime degradation and stale evidence are exposed, including enforcement and diagnosis', () => {
  const page = createUI();
  page.load(snapshot({ runtimeState: { backend: 'local_file', ready: false, degraded: true, stale: true, multiReplicaSupported: false, enforcement: 'fail_closed', lastSuccessfulRead: at(-10), reason: 'state_corrupt' } }));
  assert.ok(page.elements.get('globalHealthRuntime').classes.has('is-warning'));
  const html = page.html('globalHealthRuntime');
  for (const value of ['状态快照已陈旧', '故障时拒绝派发', '不支持多副本部署', '存储状态损坏', '最近成功读取', '最近成功写入']) assert.ok(html.includes(value), value);
  page.load(snapshot({ runtimeState: { backend: 'shared', ready: true, degraded: false, stale: false, multiReplicaSupported: true, enforcement: 'fail_open' } }));
  assert.match(page.html('globalHealthRuntime'), /状态存储就绪/);
  assert.ok(!page.elements.get('globalHealthRuntime').classes.has('is-warning'));
  page.load(snapshot());
  assert.match(page.html('globalHealthRuntime'), /未提供状态存储诊断/);
  assert.doesNotMatch(page.html('globalHealthRuntime'), /状态存储就绪/);
});

test('an unavailable state backend never turns a cached empty snapshot into zero blockers', () => {
  const page = createUI();
  page.load(snapshot({ runtimeState: { ready: false, degraded: true, stale: true } }));
  assert.match(page.html('globalHealthSummary'), /观察涉及 Provider<\/span><strong class="health-stat-unknown">未知</);
  assert.match(page.html('globalHealthSummary'), /有效冷却<\/span><strong class="health-stat-unknown">未知</);
  assert.match(page.html('globalState'), /当前观察状态未知/);
  assert.doesNotMatch(page.html('globalState'), /暂无可用性观察/);
  assert.match(page.html('globalHealthRuntime'), /观察可能来自上次成功读取/);
});

test('credential links use the health snapshot when no shared inventory has loaded', () => {
  const page = createUI();
  const calls = [];
  page.context._activeTabName = 'health';
  page.context.applyTab = tab => { calls.push(['tab', tab]); page.context._activeTabName = tab; };
  page.context.switchPageSubTab = (tab, sub) => calls.push(['sub', tab, sub]);
  page.load(snapshot({ credentials: [{ id: 'connection:sample', providerIds: ['a'], kind: 'oauth_account', status: 'expired' }] }));
  page.run('openGlobalProviderView("credentials", "a")');
  assert.deepEqual(calls, [['tab', 'credentials'], ['sub', 'credentials', 'conns']]);
});

test('credential issue links select the exact credential kind, not the provider majority', () => {
  const credentials = [
    { id: 'cline:one', providerIds: ['cline'], kind: 'oauth_account', status: 'expired' },
    { id: 'connection:one', providerIds: ['a'], kind: 'oauth_account', status: 'expired' },
    { id: 'api-key:one', providerIds: ['a'], kind: 'api_key', status: 'expired' },
    { id: 'connection:unbound', kind: 'imported_token', status: 'expired' },
  ];
  const page = createUI();
  const calls = [];
  page.context.applyTab = tab => calls.push(['tab', tab]);
  page.context.switchPageSubTab = (tab, sub) => calls.push(['sub', tab, sub]);
  page.load(snapshot({ credentials }));
  for (const [index, sub] of [[0, 'cline'], [1, 'conns'], [2, 'inventory'], [3, 'conns']]) {
    calls.length = 0;
    page.context.credentialID = credentials[index].id;
    page.run('openGlobalHealthCredential(credentialID, "a")');
    assert.deepEqual(calls, [['tab', 'credentials'], ['sub', 'credentials', sub]]);
  }
  assert.match(page.html('globalHealthCredentials'), /openGlobalHealthCredential\(&quot;api-key:one&quot;/);
  const denied = createUI({ permission: false });
  denied.context.applyTab = () => assert.fail('forbidden credential navigation');
  denied.load(snapshot({ credentials }));
  denied.run('openGlobalHealthCredential("api-key:one", "a")');
});

test('malformed entries are rejected before any health cache is published', async () => {
  for (const [path, data] of [
    ['/provider-instances', { instances: [null] }],
    ['/provider-instances', { instances: [{ id: 'a', enabled: 'yes' }] }],
    ['/provider-state', { observations: [null] }],
    ['/provider-state', { observations: [observation('auth_expired', 'a', { scope: { providerId: {} } })] }],
    ['/provider-state', { observations: [], runtimeState: { ready: 'yes' } }],
  ]) {
    const page = createUI({ api: (_method, requestPath) => requestPath === path ? { data } : response(requestPath) });
    await page.run('loadGlobalHealth()');
    assert.equal(page.json('_globalHealthData'), null);
    assert.equal(page.html('globalHealthSummary'), '');
    assert.match(page.html('globalHealthError'), /健康快照格式无效/);
    assert.doesNotMatch(page.html('globalHealthUpdated'), /刷新失败，显示上次快照/);
  }
  const credential = createUI({ api: (_method, path) => path === '/credentials' ? { data: { credentials: [null] } } : response(path) });
  await credential.run('loadGlobalHealth()');
  assert.equal(credential.json('_globalHealthData.credentialsState'), 'failed');
  assert.deepEqual(credential.json('_globalHealthData.credentials'), []);
  assert.match(credential.html('globalHealthSummary'), /不可用/);
});

test('a malformed refresh never replaces a valid earlier snapshot', async () => {
  let malformed = false;
  const data = snapshot({ state: [observation('auth_expired')] });
  const page = createUI({ api: (_method, path) => malformed && path === '/provider-state' ? { data: { observations: [null] } } : response(path, data) });
  await page.run('loadGlobalHealth()');
  const before = page.json('_globalHealthData');
  const beforeHTML = page.html('globalState');
  malformed = true;
  await page.run('loadGlobalHealth()');
  assert.deepEqual(page.json('_globalHealthData'), before);
  assert.equal(page.html('globalState'), beforeHTML);
  assert.match(page.html('globalHealthUpdated'), /刷新失败，显示上次快照/);
});

test('no observations does not assert that the provider is healthy', () => {
  const page = createUI();
  page.load(snapshot());
  assert.match(page.html('globalState'), /没有观察记录不代表健康已验证/);
  assert.doesNotMatch(page.html('globalState'), /状态存储就绪|一切正常/);
});

test('untrusted provider, credential, scope and source values cannot escape markup or handlers', () => {
  const evil = 'p"\');alert("injected")//<b>&';
  const page = createUI();
  page.load(snapshot({ instances: [{ id: evil, name: '<img src=x onerror=alert(1)>', enabled: true }], state: [observation('auth_expired', evil, { scope: { providerId: evil, credentialId: evil }, source: '<script>bad</script>' })], credentials: [{ id: evil, subject: '<svg onload=alert(1)>', providerIds: [evil], status: 'expired' }] }));
  const html = page.html('globalState') + page.html('globalHealthCredentials');
  assert.doesNotMatch(html, /<(?:img|script|svg)\b/);
  assert.match(html, /&lt;svg onload=alert\(1\)&gt;/);
  assert.match(html, /&lt;script&gt;bad&lt;\/script&gt;/);
  const decode = text => text.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
  const calls = [];
  const sandbox = vm.createContext({ openGlobalProviderView: (...args) => calls.push(args), openGlobalHealthCredential: (...args) => calls.push(args), alert: () => assert.fail('Injected handler ran') });
  for (const match of html.matchAll(/onclick="([^"]+)"/g)) vm.runInContext(decode(match[1]), sandbox);
  assert.ok(calls.length >= 2);
  assert.ok(calls.every(call => call[1] === evil));
});

test('a shared inventory refresh cannot overwrite independent health counts or filters', () => {
  const page = createUI();
  page.load(snapshot({ instances: [instances[0]], state: [observation('auth_expired')] }));
  page.run('_globalData = { instances: [{ id: "other", name: "Other" }], state: [], profiles: [] }; renderGlobal()');
  assert.match(page.html('globalHealthProviderFilter'), /Alpha/);
  assert.doesNotMatch(page.html('globalHealthProviderFilter'), /Other/);
  assert.match(page.html('globalState'), /认证过期/);
});

test('every new health label has English text and the route calls the independent health loader', () => {
  const page = createUI({ lang: 'en' });
  page.load(snapshot({ state: [observation('quota_exhausted', 'a', { until: at(5), source: 'configured_window', resetAt: at(5) })], runtimeState: { ready: true, backend: 'local_file', enforcement: 'fail_closed', multiReplicaSupported: false } }));
  const html = page.html('globalState') + page.html('globalHealthSummary') + page.html('globalHealthRuntime');
  assert.doesNotMatch(html, /[\u4e00-\u9fff]/);
  const markup = ui('html/pages/health.html');
  for (const match of markup.matchAll(/>([^<>]+)</g)) {
    const text = match[1].trim();
    if (/[\u4e00-\u9fff]/.test(text)) assert.ok(dictionary[text], 'missing English label: ' + text);
  }
  assert.match(ui('js/02_nav.js'), /name === 'health'\) \{ loadGlobalHealth\(\); \}/);
  assert.match(markup, /id="globalHealthRefresh"[^>]*onclick="loadGlobalHealth\(\)"/);
  assert.match(markup, /aria-labelledby="globalHealthObservationTitle"/);
  assert.match(ui('css/43_pages_provider_health.css'), /#tab-health \.health-summary-grid\{[^}]*repeat\(4,minmax\(0,1fr\)\)/);
  assert.match(ui('css/43_pages_provider_health.css'), /@media \(max-width:760px\)[\s\S]*#tab-health \.health-summary-grid\{grid-template-columns:repeat\(2/);
  assert.match(ui('css/43_pages_provider_health.css'), /@media \(pointer:coarse\)[\s\S]*min-height:40px;min-width:40px/);
});

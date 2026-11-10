import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

if (process.env.ROUTENEST_ONBOARDING_VERIFY_MUTATIONS !== '1') {
  throw new Error('ROUTENEST_ONBOARDING_VERIFY_MUTATIONS=1 is required for this disposable-instance test.');
}
const base = new URL(process.env.BASE_URL || 'http://127.0.0.1:3467/admin/');
if (!['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname) || base.pathname !== '/admin/' || base.search || base.hash) {
  throw new Error('BASE_URL must be a clean loopback /admin/ URL.');
}
const bootstrapSecret = process.env.ADMIN_TEST_BOOTSTRAP_SECRET || '';
const initialPassword = process.env.ADMIN_TEST_PASSWORD || '';
const recoveredPassword = process.env.ADMIN_TEST_RECOVERED_PASSWORD || '';
if (!bootstrapSecret || Array.from(initialPassword).length < 8 || Array.from(recoveredPassword).length < 8) {
  throw new Error('Disposable bootstrap, initial password, and recovered password are required.');
}

const require = createRequire(import.meta.url);
const modules = process.env.PLAYWRIGHT_MODULE
  ? [process.env.PLAYWRIGHT_MODULE]
  : ['playwright', 'playwright-core', '/tmp/routenest-playwright/node_modules/playwright-core'];
let chromium;
for (const name of modules) {
  try { ({ chromium } = require(name)); break; }
  catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
}
if (!chromium) throw new Error('Set PLAYWRIGHT_MODULE to an existing Playwright installation.');

const widths = [1440, 1280, 1024, 768, 390, 360];
const artifactDir = process.env.ARTIFACT_DIR ? resolve(process.env.ARTIFACT_DIR) : null;
if (artifactDir) mkdirSync(artifactDir, { recursive: true });
const result = { welcomeChecks: 0, recoveryChecks: 0, screenshots: 0, failures: [], diagnostics: { consoleErrors: 0, expectedUnauthorizedResources: 0, pageErrors: 0, requestFailures: 0, consoleSamples: [] } };
let step = 'launch';

function check(actual, expected, label) {
  try { assert.deepEqual(actual, expected); }
  catch { result.failures.push({ label, actual, expected }); }
}

async function post(context, path, data) {
  return context.request.post(new URL('api' + path, base).href, { headers: { Origin: base.origin }, data });
}

async function ensureAdmin(context) {
  const status = await context.request.get(new URL('api/auth/status', base).href);
  const body = await status.json();
  if (body.data.bootstrapRequired) {
    const response = await post(context, '/auth/bootstrap', { secret: bootstrapSecret, password: initialPassword });
    if (response.status() !== 200) throw new Error('disposable bootstrap failed');
  } else {
    const response = await post(context, '/login', { username: 'admin', password: initialPassword });
    if (response.status() !== 200) throw new Error('disposable login failed');
  }
}

async function attachDiagnostics(page) {
  page.on('console', message => {
    if (message.type() !== 'error') return;
    if (message.text().startsWith('Failed to load resource: the server responded with a status of 401')) {
      result.diagnostics.expectedUnauthorizedResources++;
      return;
    }
    result.diagnostics.consoleErrors++;
    if (result.diagnostics.consoleSamples.length < 5) result.diagnostics.consoleSamples.push(message.text().replace(/\?.*/, '').slice(0, 240));
  });
  page.on('pageerror', () => { result.diagnostics.pageErrors++; });
  page.on('requestfailed', () => { result.diagnostics.requestFailures++; });
}

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROMIUM_PATH || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined),
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
let recoveryCode = '';
let replacementCode = '';
try {
  const adminContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await ensureAdmin(adminContext);
  const adminPage = await adminContext.newPage();
  await attachDiagnostics(adminPage);
  step = 'open authenticated welcome';
  await adminPage.goto(base.href + '#dashboard', { waitUntil: 'networkidle' });
  await adminPage.waitForFunction(() => typeof _adminPrincipal !== 'undefined' && _adminPrincipal && document.getElementById('onboardingWelcome') && !document.getElementById('onboardingWelcome').hidden);
  const status = await adminContext.request.get(new URL('api/onboarding/status', base).href);
  const statusBody = await status.json();
  check(status.status(), 200, 'onboarding status is protected and available');
  check(statusBody.data.stage, 'connect_provider', 'fresh configured administrator starts at provider connection');

  for (const theme of ['light', 'dark']) {
    for (const language of ['en', 'zh']) {
      await adminPage.evaluate(({ theme, language }) => { setTheme(theme); setLang(language); showOnboardingWelcome(true); }, { theme, language });
      for (const width of widths) {
        step = `welcome ${theme} ${language} ${width}`;
        await adminPage.setViewportSize({ width, height: width <= 768 ? 1100 : 900 });
        const layout = await adminPage.evaluate(() => ({
          visible: !document.getElementById('onboardingWelcome').hidden,
          overflow: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) > innerWidth + 1,
          actions: [...document.querySelectorAll('#onboardingWelcome button')].filter(button => !button.hidden).every(button => !button.disabled),
        }));
        check(layout, { visible: true, overflow: false, actions: true }, step);
        if (artifactDir) {
          await adminPage.screenshot({ path: resolve(artifactDir, `welcome-${theme}-${language}-${width}.png`), fullPage: true, animations: 'disabled' });
          result.screenshots++;
        }
        result.welcomeChecks++;
      }
    }
  }

  step = 'issue recovery code while authenticated';
  await adminPage.setViewportSize({ width: 1280, height: 1000 });
  await adminPage.evaluate(() => { if (typeof closeModelSyncModal === 'function') closeModelSyncModal(); setTheme('light'); setLang('en'); switchTab('security'); });
  await adminPage.locator('#recoveryCodeCurrentPassword').waitFor({ state: 'visible' });
  await adminPage.locator('#recoveryCodeCurrentPassword').fill(initialPassword);
  const issueButtonState = await adminPage.locator('#issueRecoveryCodeButton').evaluate(button => {
    const rect = button.getBoundingClientRect();
    const style = getComputedStyle(button);
    const hit = document.elementFromPoint(Math.min(innerWidth - 1, Math.max(0, rect.left + rect.width / 2)), Math.min(innerHeight - 1, Math.max(0, rect.top + rect.height / 2)));
    return { disabled: button.disabled, display: style.display, visibility: style.visibility, pointerEvents: style.pointerEvents, rect: { left: rect.left, top: rect.top, width: rect.width, height: rect.height }, hit: hit && (hit.id || hit.className || hit.tagName) };
  });
  try { await adminPage.locator('#issueRecoveryCodeButton').click({ timeout: 5000 }); }
  catch { throw new Error('recovery issue button was not actionable: ' + JSON.stringify(issueButtonState)); }
  await adminPage.waitForFunction(() => !document.getElementById('issuedRecoveryCodePanel').hidden || document.getElementById('recoveryCodeIssueError').textContent.trim() !== '');
  const issueError = (await adminPage.locator('#recoveryCodeIssueError').textContent())?.trim();
  if (issueError) throw new Error('recovery code issuance was rejected: ' + issueError);
  recoveryCode = (await adminPage.locator('#issuedRecoveryCode').textContent()) || '';
  check(recoveryCode.length, 43, 'issued recovery code has 256-bit base64url shape');
  await adminPage.evaluate(() => { if (typeof closeModelSyncModal === 'function') closeModelSyncModal(); });
  const savedControlState = await adminPage.locator('#issuedRecoveryCodeSaved').evaluate(input => {
    const rect = input.getBoundingClientRect();
    const style = getComputedStyle(input);
    const hit = document.elementFromPoint(Math.min(innerWidth - 1, Math.max(0, rect.left + rect.width / 2)), Math.min(innerHeight - 1, Math.max(0, rect.top + rect.height / 2)));
    return { display: style.display, visibility: style.visibility, rect: { left: rect.left, top: rect.top, width: rect.width, height: rect.height }, hit: hit && (hit.id || hit.className || hit.tagName), panelHidden: document.getElementById('issuedRecoveryCodePanel').hidden };
  });
  try { await adminPage.locator('#issuedRecoveryCodeSaved').check({ timeout: 5000 }); }
  catch { throw new Error('recovery saved checkbox was not actionable: ' + JSON.stringify(savedControlState)); }
  await adminPage.locator('#issuedRecoveryCodeFinish').click();
  await adminPage.locator('#issuedRecoveryCodePanel').waitFor({ state: 'hidden' });

  const recoveryContext = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  const recoveryRequests = [];
  recoveryContext.on('request', request => { if (new URL(request.url()).pathname === '/admin/api/auth/recovery') recoveryRequests.push(request.method()); });
  const recoveryPage = await recoveryContext.newPage();
  await attachDiagnostics(recoveryPage);
  step = 'open forgot-password flow';
  await recoveryPage.goto(base.href + '#recover', { waitUntil: 'networkidle' });
  await recoveryPage.waitForFunction(() => document.documentElement.dataset.adminAuthView === 'recover' && document.activeElement?.id === 'passwordRecoveryCode');

  for (const theme of ['light', 'dark']) {
    for (const language of ['en', 'zh']) {
      await recoveryPage.evaluate(({ theme, language }) => { setTheme(theme); setLang(language); }, { theme, language });
      for (const width of widths) {
        step = `recovery form ${theme} ${language} ${width}`;
        await recoveryPage.setViewportSize({ width, height: width <= 768 ? 900 : 800 });
        const layout = await recoveryPage.evaluate(() => {
          const visible = element => !!element && element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden';
          const root = document.getElementById('adminAuthPage');
          const header = root.querySelector(':scope > header.auth-header');
          const main = root.querySelector(':scope > main#adminAuthContent');
          const login = document.getElementById('loginOverlay');
          const recovery = document.getElementById('passwordRecoveryOverlay');
          const dialog = recovery.querySelector(':scope > .recovery-dialog');
          const rect = dialog.getBoundingClientRect();
          const style = getComputedStyle(dialog);
          const pageStyle = getComputedStyle(root);
          return {
            route: location.hash,
            dataset: document.documentElement.dataset.adminAuthView,
            structure: root.matches('.auth-page') && header.parentElement === root && main.parentElement === root && login.matches('section.auth-view') && recovery.matches('section.auth-view') && login.parentElement === main && recovery.parentElement === main,
            views: [login, recovery].filter(visible).map(element => element.id),
            mains: [...document.querySelectorAll('main')].filter(visible).map(element => element.id),
            heading: document.getElementById('passwordRecoveryTitle').tagName === 'H1' && [...root.querySelectorAll('h1')].filter(visible).map(element => element.id).join(',') === 'passwordRecoveryTitle',
            preferences: [...header.querySelectorAll('[data-theme-opt],[data-lang-opt]')].filter(visible).length === 4,
            dashboardHidden: !visible(document.querySelector('.layout')),
            nonModal: !root.querySelector('[role="dialog"],[role="alertdialog"],[aria-modal],.overlay-modal,.modal-close'),
            flow: [root, header, main, recovery, dialog].every(element => getComputedStyle(element).position !== 'fixed' && ['none', ''].includes(getComputedStyle(element).backdropFilter || 'none')),
            pageBackdrop: pageStyle.backgroundImage === 'none' && ['none', ''].includes(pageStyle.backdropFilter || 'none'),
            undecorated: style.boxShadow === 'none' && parseFloat(style.borderTopWidth) === 0 && parseFloat(style.borderRadius) === 0,
            contained: rect.left >= 0 && rect.right <= innerWidth + 1 && document.documentElement.scrollWidth <= innerWidth + 1,
            back: !!document.getElementById('passwordRecoveryBack')?.closest('#passwordRecoveryFormView') && !document.querySelector('#passwordRecoveryResultView #passwordRecoveryBack'),
          };
        });
        check(layout, { route: '#recover', dataset: 'recover', structure: true, views: ['passwordRecoveryOverlay'], mains: ['adminAuthContent'], heading: true, preferences: true, dashboardHidden: true, nonModal: true, flow: true, pageBackdrop: true, undecorated: true, contained: true, back: true }, step);
        if (artifactDir) {
          await recoveryPage.screenshot({ path: resolve(artifactDir, `recovery-${theme}-${language}-${width}.png`), fullPage: true, animations: 'disabled' });
          result.screenshots++;
        }
        result.recoveryChecks++;
      }
    }
  }
  await recoveryPage.reload({ waitUntil: 'networkidle' });
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recover' && document.activeElement?.id === 'passwordRecoveryCode');
  check(recoveryRequests, [], 'direct #recover load/refresh and layout checks do not call the reset API');

  step = 'recover password';
  await adminPage.close();
  await recoveryPage.setViewportSize({ width: 1280, height: 900 });
  await recoveryPage.evaluate(() => { setTheme('light'); setLang('en'); });
  await recoveryPage.locator('#passwordRecoveryCode').fill(recoveryCode);
  await recoveryPage.locator('#passwordRecoveryNewPassword').fill(recoveredPassword);
  await recoveryPage.locator('#passwordRecoveryConfirmPassword').fill(recoveredPassword);
  await recoveryPage.locator('#passwordRecoverySubmit').click();
  await recoveryPage.locator('#passwordRecoveryResultView').waitFor({ state: 'visible' });
  await recoveryPage.waitForFunction(() => document.documentElement.dataset.adminAuthView === 'recovery-result' && location.hash === '#recover' && document.activeElement?.id === 'passwordRecoveryReplacementCode');
  replacementCode = (await recoveryPage.locator('#passwordRecoveryReplacementCode').textContent()) || '';
  check(replacementCode.length, 43, 'successful recovery rotates the recovery code');
  check(replacementCode === recoveryCode, false, 'recovery code is single-use and rotated');
  const resultPage = await recoveryPage.evaluate(() => ({
    views: ['loginOverlay', 'passwordRecoveryOverlay'].filter(id => document.getElementById(id).getClientRects().length > 0),
    headings: [...document.querySelectorAll('#adminAuthPage h1')].filter(element => element.getClientRects().length > 0).map(element => element.id),
    backControls: [...document.querySelectorAll('#passwordRecoveryOverlay #passwordRecoveryBack')].filter(element => element.getClientRects().length > 0).length,
    closeControls: document.querySelectorAll('#passwordRecoveryOverlay .modal-close').length,
  }));
  check(resultPage, { views: ['passwordRecoveryOverlay'], headings: ['passwordRecoveryResultTitle'], backControls: 0, closeControls: 0 }, 'completed recovery stays a plain #recover page with its result h1 and no Back/close control');
  await recoveryPage.keyboard.press('Escape');
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
  await recoveryPage.evaluate(() => closePasswordRecovery());
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
  await recoveryPage.evaluate(() => { location.hash = '#login'; });
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
  await recoveryPage.goBack();
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
  check((await recoveryPage.locator('#passwordRecoveryReplacementCode').textContent())?.length, 43, 'escape, direct close, hash, and Back cannot discard an unacknowledged result');

  const revoked = await adminContext.request.get(new URL('api/auth/session', base).href);
  check(revoked.status(), 401, 'password recovery revokes the existing administrator session');
  await recoveryPage.locator('#passwordRecoverySaved').check();
  await recoveryPage.evaluate(() => { location.hash = '#login'; });
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
  await recoveryPage.goBack();
  await recoveryPage.waitForFunction(() => location.hash === '#recover' && document.documentElement.dataset.adminAuthView === 'recovery-result');
  check((await recoveryPage.locator('#passwordRecoveryReplacementCode').textContent())?.length, 43, 'acknowledgement alone does not discard the result before Finish');
  await recoveryPage.locator('#passwordRecoveryFinish').click();
  await recoveryPage.waitForFunction(() => document.documentElement.dataset.adminAuthView === 'login' && location.hash === '#login');
  await recoveryPage.locator('#loginOverlay').waitFor({ state: 'visible' });
  await recoveryPage.locator('#loginPassword').fill(recoveredPassword);
  let loginResponsePromise = recoveryPage.waitForResponse(response => new URL(response.url()).pathname === '/admin/api/login' && response.request().method() === 'POST');
  await recoveryPage.locator('#loginPassword').press('Enter');
  let loginResponse = await loginResponsePromise;
  if (loginResponse.status() === 429) {
    const retryAfter = Number(loginResponse.headers()['retry-after'] || 1);
    await new Promise(resolve => setTimeout(resolve, Math.max(1, retryAfter) * 1000));
    await recoveryPage.locator('#loginPassword').fill(recoveredPassword);
    loginResponsePromise = recoveryPage.waitForResponse(response => new URL(response.url()).pathname === '/admin/api/login' && response.request().method() === 'POST');
    await recoveryPage.locator('#loginPassword').press('Enter');
    loginResponse = await loginResponsePromise;
  }
  check(loginResponse.status(), 200, 'new administrator password signs in');
  await recoveryPage.waitForFunction(() => typeof _adminPrincipal !== 'undefined' && _adminPrincipal && getComputedStyle(document.getElementById('adminAuthPage')).display === 'none');
  const replay = await post(recoveryContext, '/auth/recovery', { recoveryCode, newPassword: 'ReplayRejected8!', confirmPassword: 'ReplayRejected8!' });
  check(replay.status(), 401, 'used recovery code is rejected');
  await adminContext.close();
  await recoveryContext.close();
} catch (error) {
  result.failures.push({ label: 'browser execution stopped', step, errorType: error.name, detail: String(error.message || '').split('\n')[0] });
} finally {
  recoveryCode = '';
  replacementCode = '';
  await browser.close();
}

for (const kind of ['consoleErrors', 'pageErrors', 'requestFailures']) check(result.diagnostics[kind], 0, kind);
console.log(JSON.stringify(result, null, 2));
if (result.failures.length) process.exitCode = 1;

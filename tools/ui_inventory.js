// Browser inventory of the admin console: every tab and subtab, the visible
// controls with their inline handlers, the element IDs, the /admin/api calls each
// view makes, and any console or page errors.
//
// Usage:
//   PLAYWRIGHT_CORE=/path/to/playwright-core node tools/ui_inventory.js <baseUrl> <out.json>
// Set CHROME_PATH to a Chromium binary if the bundled one is not installed.
const fs = require('fs');
const pw = require(process.env.PLAYWRIGHT_CORE || 'playwright-core');

const [base, out] = process.argv.slice(2);
if (!base || !out) {
  console.error('usage: ui_inventory.js <baseUrl> <out.json>');
  process.exit(2);
}

async function main() {
  const browser = await pw.chromium.launch({
    executablePath: process.env.CHROME_PATH || undefined,
    args: ['--no-sandbox'],
  });
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  page.on('response', r => { if (r.status() >= 400) errors.push('http ' + r.status() + ': ' + r.url()); });

  let apiCalls = new Set();
  page.on('request', r => {
    const u = new URL(r.url());
    if (u.pathname.startsWith('/admin/api')) apiCalls.add(r.method() + ' ' + u.pathname);
  });

  await page.goto(base + '/admin/', { waitUntil: 'networkidle' });
  const tabs = await page.$$eval('.nav-item[data-tab]', els => els.map(e => e.getAttribute('data-tab')));

  const result = { tabs: {}, errors: null };
  for (const tab of tabs) {
    apiCalls = new Set();
    await page.goto(base + '/admin/#' + tab, { waitUntil: 'networkidle' });
    await page.waitForTimeout(400);
    const subs = await page.evaluate(t => {
      const panel = document.getElementById('tab-' + t);
      return panel ? [...panel.querySelectorAll('.subtab[data-sub]')].map(e => e.getAttribute('data-sub')) : [];
    }, tab);
    const views = [{ sub: null }].concat([...new Set(subs)].map(s => ({ sub: s })));
    const tabResult = { subviews: {} };
    for (const v of views) {
      if (v.sub) {
        await page.evaluate(([tab, sub]) => {
          const panel = document.getElementById('tab-' + tab);
          const el = panel && panel.querySelector(`.subtab[data-sub="${sub}"]`);
          if (el) el.click();
        }, [tab, v.sub]);
        await page.waitForTimeout(250);
      }
      const snap = await page.evaluate(() => {
        const visible = el => {
          const r = el.getBoundingClientRect();
          return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden';
        };
        const controls = [];
        for (const el of document.querySelectorAll('[onclick]')) {
          if (!visible(el)) continue;
          const label = (el.innerText || el.getAttribute('title') || el.getAttribute('aria-label') || '').trim().slice(0, 60);
          controls.push({ tag: el.tagName.toLowerCase(), id: el.id || '', onclick: el.getAttribute('onclick'), label });
        }
        const ids = [...document.querySelectorAll('[id]')].filter(visible).map(e => e.id).sort();
        return { controls, ids };
      });
      tabResult.subviews[v.sub || 'default'] = {
        controls: snap.controls.map(c => JSON.stringify(c)).sort(),
        ids: snap.ids,
      };
    }
    tabResult.apiCalls = [...apiCalls].sort();
    result.tabs[tab] = tabResult;
  }
  result.errors = errors;
  await browser.close();
  fs.writeFileSync(out, JSON.stringify(result, null, 2));
  console.log(`tabs=${tabs.length} errors=${errors.length} -> ${out}`);
}

main().catch(e => { console.error(e); process.exit(1); });

#!/usr/bin/env node
// Real Chromium end-to-end tests. No package.json or dependency changes needed.
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const net = require('node:net');
const assert = require('node:assert/strict');
const playwright = process.env.PLAYWRIGHT_MODULE
  ? require(path.resolve(process.env.PLAYWRIGHT_MODULE)) : require('playwright');
const root = path.resolve(__dirname, '../..');
const artifacts = path.join(root, 'bin/browser');
const work = fs.mkdtempSync(path.join(os.tmpdir(), 'msbd-browser-'));
const password = crypto.randomBytes(24).toString('hex');
const changedPassword = crypto.randomBytes(24).toString('hex');
const apiKey = crypto.randomBytes(32).toString('hex');
const env = { PATH: process.env.PATH, HOME: work, MSB_HOME: path.join(work, 'runtime'),
  MSBD_DATA_DIR: path.join(work, 'data'), MSBD_API_KEY: apiKey, NO_COLOR: '1' };
// Never inherit MSBD_* credentials or load the checkout's .env; all subprocesses
// run in a new temp working directory. Only runtime executables may be copied.
fs.mkdirSync(artifacts, { recursive: true });
const binary = path.join(artifacts, 'msbd');
const results = [];
const errors = [];
let daemon, browser, page, base;
let sandboxID;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(fn, message, timeout = 10000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await fn()) return;
    await delay(100);
  }
  throw new Error(message);
}
async function visible(locator) { await locator.waitFor({ state: 'visible', timeout: 10000 }); }
async function gone(locator) { await locator.waitFor({ state: 'hidden', timeout: 10000 }); }
async function screenshot(name) {
  // Capture the settled UI, not the first frame of a sheet/dialog transition.
  // Leave infinite animations (spinners/cursors) alone.
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().filter(animation =>
      animation.effect?.getComputedTiming().iterations !== Infinity
    ).map(animation => animation.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  // Token reveal is sensitive even in an isolated test. Never capture its value.
  await page.screenshot({ path: path.join(artifacts, `${name}.png`), fullPage: true,
    mask: [page.locator('#new-key-token')] });
}
async function test(name, fn) {
  try { await fn(); results.push({ name, status: 'passed' }); console.log(`PASS ${name}`); }
  catch (e) {
    results.push({ name, status: 'failed', error: e.message }); console.log(`FAIL ${name}: ${e.message}`);
    if (page && !page.isClosed()) await screenshot(`failure-${name}`).catch(() => {});
  }
}
async function goto(url) {
  await page.goto(`${base}${url}`, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => !!window.templ?.dialog);
}
async function login(p, user, pass = password) {
  await p.goto(`${base}/login`, { waitUntil: 'domcontentloaded' });
  await p.locator('#username').fill(user);
  await p.locator('#password').fill(pass);
  await p.getByRole('button', { name: 'Sign in', exact: true }).click();
  await p.waitForURL(base + '/', { waitUntil: 'domcontentloaded' });
}
async function confirm(action, accept) {
  await page.getByRole('button', { name: action, exact: true }).click();
  await visible(page.locator('#confirm'));
  await page.locator('#confirm').getByRole('button', { name: accept ? /^(Delete|Revoke|Make admin|Make viewer)$/ : 'Cancel', exact: true }).click();
  await gone(page.locator('#confirm'));
}
async function api(url, method = 'GET', body) {
  const response = await fetch(base + '/api/v1' + url, { method,
    headers: { Authorization: `Bearer ${apiKey}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body) });
  if (!response.ok) throw new Error(`${method} ${url}: HTTP ${response.status} ${(await response.text()).slice(0, 500)}`);
  return response.status === 204 ? null : response.json();
}
async function main() {
  const build = spawnSync('go', ['build', '-o', binary, './cmd/msbd'], { cwd: root, stdio: 'inherit' });
  assert.equal(build.status, 0, 'go build failed');
  if (process.env.MSBD_BROWSER_RUNTIME_SEED) {
    const seed = path.resolve(process.env.MSBD_BROWSER_RUNTIME_SEED);
    for (const dir of ['bin', 'lib']) {
      if (fs.existsSync(path.join(seed, dir))) fs.cpSync(path.join(seed, dir), path.join(env.MSB_HOME, dir), { recursive: true });
    }
  }
  const add = spawnSync(binary, ['users', 'add', 'browser-admin', '--password-stdin'],
    { cwd: work, env, input: password + '\n', encoding: 'utf8' });
  assert.equal(add.status, 0, 'could not seed isolated admin');
  const server = net.createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  base = `http://127.0.0.1:${port}`;
  const log = fs.openSync(path.join(artifacts, 'daemon.log'), 'w', 0o600);
  daemon = spawn(binary, ['serve', '--listen', `127.0.0.1:${port}`], { cwd: work, env, stdio: ['ignore', log, log] });
  fs.closeSync(log);
  await until(async () => {
    if (daemon.exitCode !== null) throw new Error('daemon exited; see bin/browser/daemon.log');
    try { return (await fetch(base + '/healthz')).ok; } catch { return false; }
  }, 'daemon failed to become healthy', 300000);
  const chromium = process.env.CHROMIUM || spawnSync('sh', ['-c', 'command -v chromium'], { encoding: 'utf8' }).stdout.trim();
  assert.ok(chromium, 'chromium must be on PATH or set CHROMIUM');
  browser = await playwright.chromium.launch({ executablePath: chromium, headless: true });
  console.log(`Chromium ${browser.version()}; isolated workdir ${work}`);
  page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  page.setDefaultTimeout(10000);
  page.on('pageerror', e => errors.push(e.message));
  if (process.env.MSBD_BROWSER_CSS) await page.route('**/assets/css/output.css', route => route.fulfill({ path: path.resolve(process.env.MSBD_BROWSER_CSS), contentType: 'text/css' }));
  await test('admin-login', async () => {
    await page.goto(base + '/settings/users');
    assert.ok(page.url().includes('/login?next='), 'protected page should redirect to login');
    await page.locator('#username').fill('browser-admin');
    await page.locator('#password').fill(password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await page.waitForURL(base + '/settings/users');
    await visible(page.locator('#user-table').getByText('browser-admin', { exact: false }));
    assert.equal(await page.getByRole('button', { name: 'Delete user browser-admin', exact: true }).count(), 0);
    assert.equal(await page.getByRole('button', { name: 'Make viewer browser-admin', exact: true }).count(), 0);
  });
  await test('navigation', async () => {
    await goto('/');
    for (const [name, url] of [['Sandboxes', '/sandboxes'], ['Volumes', '/volumes'], ['Images', '/images'], ['Snapshots', '/snapshots'], ['API keys', '/settings/keys'], ['Users', '/settings/users'], ['Overview', '/']]) {
      await page.getByRole('link', { name, exact: true }).filter({ visible: true }).click();
      await page.waitForURL(base + url);
      assert.equal(await page.title(), `${name} · msbd`);
    }
    await screenshot('overview');
    await page.goBack();
    assert.equal(new URL(page.url()).pathname, '/settings/users');
  });
  await test('desktop-layout', async () => {
    await goto('/settings/keys');
    const content = await page.locator('#content').boundingBox();
    const sidebar = await page.locator('[data-slot="sidebar-container"]').filter({ visible: true }).boundingBox();
    assert.ok(sidebar && content.x >= sidebar.x + sidebar.width - 1,
      `#content x=${content.x} overlaps sidebar right=${sidebar && sidebar.x + sidebar.width}`);
  });
  await test('theme', async () => {
    await goto('/');
    await page.getByRole('button', { name: 'Dark theme', exact: true }).filter({ visible: true }).click();
    await until(() => page.locator('html').evaluate(el => el.classList.contains('dark')), 'dark theme not applied');
    await screenshot('dark-overview');
    await page.reload({ waitUntil: 'domcontentloaded' });
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    await page.getByRole('button', { name: 'Light theme', exact: true }).filter({ visible: true }).click();
    await until(() => page.locator('html').evaluate(el => !el.classList.contains('dark')), 'light theme not applied');
  });
  await test('mobile-sidebar', async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    await goto('/');
    await page.getByRole('button', { name: 'Toggle navigation', exact: true }).click();
    await visible(page.locator('#main-sidebar-mobile'));
    await screenshot('mobile-navigation');
    await page.locator('#main-sidebar-mobile').getByRole('link', { name: 'Sandboxes', exact: true }).click();
    await page.waitForURL(base + '/sandboxes');
    await page.getByRole('button', { name: 'Toggle navigation', exact: true }).click();
    await visible(page.locator('#main-sidebar-mobile'));
    await page.keyboard.press('Escape');
    await gone(page.locator('#main-sidebar-mobile'));
    await screenshot('mobile-sandboxes');
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await test('checkbox', async () => {
    // Force Datastar to initialize before the deferred component bundle:
    // guarded effects must still hydrate live:true when that bundle arrives.
    const bundle = '**/assets/js/shadcn-templ-*.js';
    const slowBundle = async route => { await delay(1500); await route.continue(); };
    await page.route(bundle, slowBundle);
    try {
      await goto('/sandboxes');
      const check = page.getByRole('checkbox', { name: 'Auto-refresh', exact: true });
      await until(async () => (await check.getAttribute('aria-checked')) === 'true', 'auto-refresh checkbox did not hydrate from live:true');
      const initial = await check.getAttribute('aria-checked');
      await check.click();
      await until(async () => (await check.getAttribute('aria-checked')) !== initial, '#live checkbox did not change');
      await check.click();
      await until(async () => (await check.getAttribute('aria-checked')) === initial, '#live checkbox did not revert');
    } finally { await page.unroute(bundle, slowBundle); }
  });
  await test('key-create-select-reveal', async () => {
    await goto('/settings/keys');
    await page.getByRole('button', { name: 'New key', exact: true }).first().click();
    await visible(page.locator('#create-key'));
    await page.locator('#keyname').fill('browser-key');
    await page.locator('#keyexpires').click();
    await page.getByRole('option', { name: '7 days', exact: true }).click();
    assert.match(await page.locator('#keyexpires').innerText(), /7 days/);
    await page.locator('#create-key').getByRole('button', { name: 'Create', exact: true }).click();
    await visible(page.locator('#key-table tbody tr').filter({ hasText: 'browser-key' }));
    await visible(page.locator('#new-key'));
    assert.match(await page.locator('#new-key-token').innerText(), /^msbd_/);
    await screenshot('key-reveal-masked');
    await page.locator('#new-key').getByRole('button', { name: 'Done', exact: true }).click();
    await gone(page.locator('#new-key'));
  });
  await test('key-confirm-cancel-revoke-delete-toast', async () => {
    await goto('/settings/keys');
    await confirm('Revoke key browser-key', false);
    assert.match(await page.locator('#key-table').innerText(), /active/);
    await confirm('Revoke key browser-key', true);
    await visible(page.locator('[data-slot="toast-viewport"]').getByText('Key revoked', { exact: true }));
    await until(async () => (await page.locator('#key-table').innerText()).includes('revoked'), 'key not revoked');
    await screenshot('sse-toast');
    await confirm('Delete key browser-key', false);
    await visible(page.locator('#key-table tbody tr').filter({ hasText: 'browser-key' }));
    await confirm('Delete key browser-key', true);
    await gone(page.locator('#key-table tbody tr').filter({ hasText: 'browser-key' }));
  });
  await test('user-create-select', async () => {
    await goto('/settings/users');
    await page.getByRole('button', { name: 'New user', exact: true }).first().click();
    await page.locator('#newuser').fill('browser-viewer');
    await page.locator('#newpass').fill(password);
    await page.locator('#newrole').click();
    await page.getByRole('option', { name: 'viewer', exact: true }).click();
    await page.locator('#create-user').getByRole('button', { name: 'Create', exact: true }).click();
    await gone(page.locator('#create-user'));
    await visible(page.locator('#user-table').getByText('browser-viewer', { exact: true }));
    await visible(page.locator('[data-slot="toast-viewport"]').getByText('User created', { exact: true }));
  });
  await test('viewer-role-enforcement', async () => {
    const context = await browser.newContext();
    try {
      const viewer = await context.newPage();
      await login(viewer, 'browser-viewer');
      assert.equal(await viewer.getByRole('link', { name: 'Users', exact: true }).count(), 0);
      await viewer.goto(base + '/sandboxes');
      // Hidden-button courtesy is checked separately; authorization must still run.
      const offered = await viewer.getByRole('button', { name: 'New sandbox', exact: true }).count();
      results.push(offered === 0 ? { name: 'viewer-create-button-hidden', status: 'passed' }
        : { name: 'viewer-create-button-hidden', status: 'failed', error: 'Viewer is offered New sandbox controls' });
      for (const url of ['/settings/users', '/settings/keys']) {
        const response = await context.request.get(base + url);
        assert.equal(response.status(), 403, url);
      }
      for (const [method, url] of [['post', '/ui/users'], ['post', '/ui/keys'], ['post', '/ui/sandboxes'], ['delete', '/ui/users/browser-admin']]) {
        const response = await context.request[method](base + url, { data: {} });
        assert.equal(response.status(), 403, url);
      }
      await viewer.getByRole('button', { name: 'Sign out', exact: true }).filter({ visible: true }).click();
      await viewer.waitForURL(base + '/login');
    } finally { await context.close(); }
  });
  await test('user-role-update', async () => {
    await goto('/settings/users');
    await confirm('Make admin browser-viewer', false);
    await visible(page.getByRole('button', { name: 'Make admin browser-viewer', exact: true }));
    await confirm('Make admin browser-viewer', true);
    await visible(page.getByRole('button', { name: 'Make viewer browser-viewer', exact: true }));
    await confirm('Make viewer browser-viewer', true);
    await visible(page.getByRole('button', { name: 'Make admin browser-viewer', exact: true }));
  });
  await test('user-password-update', async () => {
    await goto('/settings/users');
    await page.getByRole('button', { name: 'Set password for browser-viewer', exact: true }).click();
    await page.locator('#pwvalue').fill(changedPassword);
    await page.locator('#set-password').getByRole('button', { name: 'Set password', exact: true }).click();
    await gone(page.locator('#set-password'));
    await visible(page.locator('[data-slot="toast-viewport"]').getByText('Password set', { exact: true }));
    const context = await browser.newContext();
    try { await login(await context.newPage(), 'browser-viewer', changedPassword); }
    finally { await context.close(); }
  });
  await test('user-delete-cancel-accept', async () => {
    await goto('/settings/users');
    await confirm('Delete user browser-viewer', false);
    await visible(page.locator('#user-table').getByText('browser-viewer', { exact: true }));
    await confirm('Delete user browser-viewer', true);
    await gone(page.locator('#user-table').getByText('browser-viewer', { exact: true }));
  });
  if (process.env.MSBD_BROWSER_VM === '1') await vmTests();
  else results.push({ name: 'real-sandbox', status: 'skipped', reason: 'set MSBD_BROWSER_VM=1; requires /dev/kvm and network for isolated OCI pull' });
  await test('javascript-errors', async () => assert.deepEqual(errors, []));
}
// Optional real-VM tests never touch pre-existing sandboxes; API provisioning
// uses only the random key and fresh runtime created above.
async function vmTests() {
  await test('real-sandbox-create', async () => {
    const sandbox = await api('/sandboxes', 'POST', { image: process.env.MSBD_BROWSER_IMAGE || 'microsandbox/python' });
    sandboxID = sandbox.id;
    assert.ok(sandboxID);
    await api(`/sandboxes/${sandboxID}/files/write`, 'POST', { path: '/tmp/browser-test.txt', content_b64: Buffer.from('browser test fixture\n').toString('base64') });
  });
  if (!sandboxID) return;
  await test('real-sandbox-tabs', async () => {
    await goto(`/sandboxes/${sandboxID}`);
    const actions = page.getByRole('group', { name: 'Sandbox actions', exact: true });
    await visible(actions.getByRole('button', { name: 'Stop', exact: true }));
    const corners = await actions.evaluate(el => {
      const children = [...el.children].filter(child => child.getBoundingClientRect().width > 0);
      return {
        left: parseFloat(getComputedStyle(children[0]).borderTopLeftRadius),
        right: parseFloat(getComputedStyle(children.at(-1)).borderTopRightRadius),
        count: children.length,
      };
    });
    assert.equal(corners.count, 4, 'action group must have no hidden lifecycle sibling');
    assert.ok(corners.left > 0 && corners.right > 0, 'both action group ends must be rounded');
    for (const name of ['Run', 'Logs', 'Files', 'Overview']) {
      const tab = page.getByRole('tab', { name, exact: true });
      await tab.click();
      await until(async () => (await tab.getAttribute('aria-selected')) === 'true', `${name} tab not selected`);
    }
    await screenshot('sandbox-overview');
  });
  await test('real-sandbox-files', async () => {
    await page.getByRole('tab', { name: 'Files', exact: true }).click();
    await visible(page.locator('#files-panel'));
    // A visible path control allows changing the directory without reaching into signals.
    await page.getByRole('button', { name: 'Edit path', exact: true }).click();
    await page.locator('#filepath').fill('/tmp');
    await page.locator('#filepath').press('Enter');
    await page.locator('#files-panel').getByText('browser-test.txt', { exact: true }).click();
    await visible(page.locator('#file-view'));
    await screenshot('file-dialog');
    await page.locator('#file-view').getByRole('button', { name: 'Close', exact: true }).first().click();
  });
  await test('real-sandbox-xterm', async () => {
    await goto(`/sandboxes/${sandboxID}`);
    await page.getByRole('tab', { name: 'Terminal', exact: true }).click();
    const terminal = page.frameLocator('iframe');
    await visible(terminal.locator('.xterm-screen'));
    await until(async () => (await terminal.locator('#dot').getAttribute('class')) === 'ok', 'terminal WebSocket did not connect', 30000);
    // WebSocket OPEN is transport readiness, not a rendered shell prompt.
    // The tab/iframe can still take focus during mounting; click the visible
    // terminal like a user, then verify focus before sending any keystrokes.
    await until(async () => /(?:#|\$)\s*$/.test(await terminal.locator('.xterm-rows').innerText()), 'terminal shell prompt did not render', 30000);
    await terminal.locator('.xterm-screen').click();
    const input = terminal.locator('.xterm-helper-textarea');
    await until(() => input.evaluate(el => document.hasFocus() && document.activeElement === el), 'terminal input did not receive focus');
    await input.pressSequentially('printf BROWSER_TERMINAL_OK > /tmp/browser-terminal-ok');
    await input.press('Enter');
    await until(async () => {
      try { const file = await api(`/sandboxes/${sandboxID}/files/read`, 'POST', { path: '/tmp/browser-terminal-ok' }); return Buffer.from(file.content_b64, 'base64').toString() === 'BROWSER_TERMINAL_OK'; } catch { return false; }
    }, 'terminal input did not create expected guest file', 30000);
    await screenshot('xterm');
  });
  await test('real-sandbox-terminal-last-row', async () => {
    // Fit must leave every complete row inside the padded terminal content,
    // even at fractional container sizes. This caught parent-padding clipping.
    const terminal = page.frameLocator('iframe');
    for (const height of [321.5, 447, 512.75]) {
      await page.locator('iframe').evaluate((el, h) => { el.style.height = `${h}px`; }, height);
      await until(() => terminal.locator('#term').evaluate(el => {
        const xterm = el.querySelector('.xterm');
        const screen = el.querySelector('.xterm-screen');
        const rows = el.querySelector('.xterm-rows');
        if (!xterm || !screen || !rows || !rows.lastElementChild) return false;
        const bottom = el.getBoundingClientRect().bottom - parseFloat(getComputedStyle(xterm).paddingBottom);
        return screen.getBoundingClientRect().bottom <= bottom + 0.5 &&
          rows.lastElementChild.getBoundingClientRect().bottom <= bottom + 0.5;
      }), `terminal last row clipped at height ${height}`);
    }
    await screenshot('xterm-last-row');
  });
}
(async () => {
  try { await main(); }
  catch (e) { results.push({ name: 'setup', status: 'failed', error: e.message }); console.error(e.message); }
  finally {
    if (sandboxID) await api(`/sandboxes/${sandboxID}`, 'DELETE').catch(e => results.push({ name: 'sandbox-cleanup', status: 'failed', error: e.message }));
    if (browser) await browser.close();
    if (daemon && daemon.exitCode === null) {
      daemon.kill('SIGTERM');
      await Promise.race([new Promise(resolve => daemon.once('exit', resolve)), delay(10000)]);
      if (daemon.exitCode === null) daemon.kill('SIGKILL');
    }
    fs.writeFileSync(path.join(artifacts, 'results.json'), JSON.stringify({ results, errors, work }, null, 2), { mode: 0o600 });
    // Preserve isolated workdir for failure diagnosis, but never persist secrets.
    console.log(`Artifacts: ${artifacts}; isolated state: ${work}`);
    const counts = results.reduce((out, r) => ({ ...out, [r.status]: (out[r.status] || 0) + 1 }), {});
    console.log(JSON.stringify(counts));
    process.exitCode = results.some(r => r.status === 'failed') ? 1 : 0;
  }
})();

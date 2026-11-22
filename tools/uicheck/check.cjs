// Проход по интерфейсу: вход, вкладки, меню, изменения, копирование, телефон.
const { chromium, devices } = require('playwright');
const OUT = process.env.OUT || __dirname + '/shots';
const URL = 'http://127.0.0.1:8451/';
require('fs').mkdirSync(OUT, { recursive: true });

(async () => {
  const browser = await chromium.launch();
  const errors = [];
  const watch = (page) => {
    page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
    page.on('console', (m) => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  };
  const step = async (name, fn) => {
    try { await fn(); console.log('ok  ', name); } catch (e) { console.log('FAIL', name, '-', e.message.split('\n')[0]); }
  };

  const ctx = await browser.newContext({ viewport: { width: 1440, height: 860 } });
  const page = await ctx.newPage();
  watch(page);
  await page.goto(URL);
  await step('login screen', async () => {
    await page.fill('input[autocomplete=username]', 'sysadmin');
    await page.fill('input[type=password]', 'wrong');
    await page.click('button[type=submit]');
    await page.waitForSelector('.login .err:text("Неверный")');
    await page.fill('input[type=password]', 'secret');
    await page.click('button[type=submit]');
    await page.waitForSelector('.tab.active');
  });
  await step('route has repo and wt', async () => {
    await page.waitForFunction(() => location.hash.includes('/repo/r1/wt/main'));
  });
  await page.waitForTimeout(400);
  await page.screenshot({ path: OUT + '/1-main.png' });

  await step('open feature tab and see badge', async () => {
    await page.click('.tab:has-text("feature/login")');
    await page.waitForFunction(() => location.hash.includes('feature%2Flogin'));
    await page.waitForSelector('.row:has-text("worktree.go") .st-new');
  });
  await page.waitForTimeout(400);
  await step('branch context menu', async () => {
    await page.click('.left .item:has-text("fix/api-timeout")', { button: 'right' });
    await page.waitForSelector('.menu button:has-text("Открыть в отдельной вкладке")');
  });
  await page.screenshot({ path: OUT + '/2-menu.png' });
  await page.keyboard.press('Escape');

  await step('enter folder and breadcrumbs', async () => {
    await page.dblclick('.row:has-text("git/")');
    await page.waitForSelector('.row:has-text("status.go")');
    await page.click('.crumbs button:has-text("myrepo")');
    await page.waitForSelector('.row:has-text("server.go")');
  });

  await step('open server panel and settings', async () => {
    await page.click('button:has-text("Открыть локальную ФС")');
    await page.waitForSelector('.right .row:has-text("README.md")');
    await page.click('.right button[aria-label="Настройки локальной ФС"]');
    await page.waitForSelector('.popover:has-text("Папка по умолчанию")');
  });
  await page.screenshot({ path: OUT + '/3-fs-settings.png' });
  await page.click('.right button[aria-label="Настройки локальной ФС"]');

  await step('drag file to server: conflict dialog', async () => {
    await page.dragAndDrop('.center .row:has-text("server.go")', '.right .dropzone');
    await page.waitForSelector('.dialog:has-text("уже существуют")');
  });
  await page.screenshot({ path: OUT + '/4-conflict.png' });
  await step('keep both and run', async () => {
    await page.click('.dialog button:has-text("Оставить оба для всех")');
    await page.click('.dialog footer button:has-text("Копировать")');
    await page.waitForSelector('.right .row:has-text("server.go (2)")');
  });

  await step('changes view: stage, diff, commit', async () => {
    await page.click('.seg button:has-text("Изменения")');
    await page.waitForSelector('.chg:has-text("status.go")');
    await page.click('.chg:has-text("status.go")');
    await page.waitForSelector('.diff .a');
    await page.hover('.chg:has-text("status.go")');
    await page.click('.chg:has-text("status.go") button[title="Подготовить (stage)"]');
    await page.fill('.commit-box textarea', 'Parse porcelain v1');
    await page.waitForSelector('.commit-box button:has-text("Коммит (2)")');
  });
  await page.screenshot({ path: OUT + '/5-changes.png' });
  await step('commit', async () => {
    await page.click('.commit-box button.primary');
    await page.waitForSelector('.toast:has-text("Коммит abcdef1")');
  });

  await step('push error goes to output', async () => {
    await page.click('.topbar button:has-text("Push")');
    await page.waitForSelector('.dock .log-entry .cmd.bad:has-text("git push")');
  });

  await step('file: edit, draft, save via Ctrl+S', async () => {
    await page.click('.seg button:has-text("Файлы")');
    await page.dblclick('.center .row:has-text("README.md")');
    await page.waitForSelector('textarea.editor');
    await page.click('textarea.editor');
    await page.keyboard.press('End');
    await page.keyboard.type('Новая строка');
    await page.waitForSelector('.dirty');
    await page.keyboard.press('Control+s');
    await page.waitForSelector('.toast:has-text("Сохранено")');
  });
  await page.screenshot({ path: OUT + '/6-editor.png' });

  await step('palette', async () => {
    await page.keyboard.press('Control+k');
    await page.fill('.palette input', 'fetch');
    await page.waitForSelector('.palette .results button:has-text("Git: fetch")');
  });
  await page.screenshot({ path: OUT + '/7-palette.png' });
  await page.keyboard.press('Enter');
  await step('fetch output logged', async () => {
    await page.waitForSelector('.log-entry .cmd.ok:has-text("git fetch")');
  });

  await step('reload keeps route', async () => {
    await page.goto(URL + '#/repo/r1/wt/feature%2Flogin/file/server.go:3');
    await page.waitForSelector('textarea.editor');
    const sel = await page.$eval('textarea.editor', (a) => a.value.slice(a.selectionStart, a.selectionEnd));
    if (!sel.includes('func main')) throw new Error('line not selected: ' + JSON.stringify(sel));
  });

  // телефон
  const m = await browser.newContext({ ...devices['iPhone 13'] });
  const mp = await m.newPage();
  watch(mp);
  await mp.goto(URL + '#/repo/r1/wt/feature%2Flogin');
  await step('mobile: files pane', async () => {
    await mp.waitForSelector('.mnav');
    await mp.waitForSelector('.center .row:has-text("server.go")');
  });
  await mp.waitForTimeout(300);
  await mp.screenshot({ path: OUT + '/8-mobile-files.png' });
  await step('mobile: branches pane', async () => {
    await mp.click('.mnav button:has-text("Ветки")');
    await mp.waitForSelector('.left .item:has-text("feature/login")');
  });
  await mp.screenshot({ path: OUT + '/9-mobile-branches.png' });
  await step('mobile: server pane', async () => {
    await mp.click('.mnav button:has-text("Сервер")');
    await mp.waitForSelector('.right .row:has-text("README.md")');
  });
  await mp.screenshot({ path: OUT + '/10-mobile-server.png' });
  await step('mobile: output pane', async () => {
    await mp.click('.mnav button:has-text("Вывод")');
    await mp.waitForSelector('.dock-body');
  });
  await mp.screenshot({ path: OUT + '/11-mobile-out.png' });

  console.log('errors:', errors.length ? errors : 'none');
  await browser.close();
})();

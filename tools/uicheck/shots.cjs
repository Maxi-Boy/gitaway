// Снимки для README (docs/images): та же заглушка API, что и у check.cjs.
//   node tools/uicheck/mock.mjs "$PWD/web" 8451 &
//   node tools/uicheck/shots.cjs
const { chromium, devices } = require('playwright');
const path = require('path');
const OUT = process.env.OUT || path.join(__dirname, '../../docs/images');
const URL = 'http://127.0.0.1:8451/';
require('fs').mkdirSync(OUT, { recursive: true });

async function open(browser, opts, theme) {
  const ctx = await browser.newContext({ locale: 'ru-RU', ...opts });
  await ctx.addInitScript((th) => {
    localStorage.setItem('gitaway:theme', th);
    localStorage.setItem('gitaway:lang', 'ru');
  }, theme);
  const page = await ctx.newPage();
  await page.goto(URL);
  if (await page.$('input[type=password]')) {
    await page.fill('input[autocomplete=username]', 'sysadmin');
    await page.fill('input[type=password]', 'secret');
    await page.click('button[type=submit]');
  }
  await page.waitForSelector('.left .item.open', { state: 'attached' });
  return page;
}

const settle = (page) => page.waitForTimeout(700);

(async () => {
  const browser = await chromium.launch();
  const desk = { viewport: { width: 1440, height: 860 } };

  // главный экран: открытые ветки, таблица файлов, серверная панель
  let p = await open(browser, desk, 'dark');
  await p.goto(URL + '#/repo/r1/wt/feature%2Flogin');
  await p.waitForSelector('.center .row:has-text("server.go") >> text=Wire auth middleware');
  await p.click('button:has-text("Открыть локальную ФС")');
  await p.waitForSelector('.right .row:has-text("README.md")');
  await p.click('.dock-tabs .t:has-text("Терминал")');
  await settle(p);
  await p.screenshot({ path: OUT + '/main.png' });

  // история и diff коммита
  await p.click('button:has-text("Закрыть локальную ФС")').catch(() => {});
  await p.goto(URL + '#/repo/r1/wt/feature%2Flogin/history');
  await p.waitForSelector('.commit-detail h3');
  await p.click('.cfile-head:has-text("api/pull.go")');
  await p.waitForSelector('.cfile-diff .diff');
  await settle(p);
  await p.screenshot({ path: OUT + '/history.png' });

  // поиск по проекту
  await p.goto(URL + '#/repo/r1/wt/feature%2Flogin/search');
  await p.fill('.search input[type=search]', 'todo');
  await p.waitForSelector('.search-sum');
  await settle(p);
  await p.screenshot({ path: OUT + '/search.png' });
  await p.context().close();

  // светлая тема: редактор и терминал
  p = await open(browser, desk, 'light');
  await p.goto(URL + '#/repo/r1/wt/feature%2Flogin/file/server.go');
  await p.waitForSelector('.cm-editor');
  await p.click('.dock-tabs .t:has-text("Терминал")');
  await p.click('.term-tabs button:has-text("+ терминал")').catch(() => {});
  await p.waitForSelector('.term-screen:not([hidden]) .xterm');
  await p.waitForTimeout(500);
  await p.keyboard.type('pwd');
  await p.keyboard.press('Enter');
  await settle(p);
  await p.screenshot({ path: OUT + '/light.png' });
  await p.context().close();

  // телефон
  p = await open(browser, { ...devices['iPhone 13'] }, 'dark');
  await p.goto(URL + '#/repo/r1/wt/feature%2Flogin');
  await p.waitForSelector('.center .row:has-text("server.go")');
  await settle(p);
  await p.screenshot({ path: OUT + '/mobile.png' });
  await p.context().close();

  await browser.close();
})();

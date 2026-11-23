// Проход по интерфейсу: вход, вкладки, меню, изменения, копирование, телефон.
const { chromium, devices } = require('playwright');
const OUT = process.env.OUT || __dirname + '/shots';
const URL = 'http://127.0.0.1:8451/';
require('fs').mkdirSync(OUT, { recursive: true });

(async () => {
  const browser = await chromium.launch();
  const errors = [];
  const watch = (page) => {
    page.on('pageerror', (e) => { errors.push('pageerror: ' + e.message); console.log('pageerror:', e.message); });
    page.on('console', (m) => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  };
  const step = async (name, fn) => {
    try { await fn(); console.log('ok  ', name); } catch (e) { console.log('FAIL', name, '-', e.message.split('\n')[0]); }
  };

  // язык интерфейса выбирается по языку браузера: основной сценарий на русском
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 860 }, locale: 'ru-RU' });
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

  await step('drag two files to server: first conflict', async () => {
    // коммиты приходят вторым запросом и перерисовывают таблицу: тащить строку после этого
    await page.waitForSelector('.center .row:has-text("Wire auth middleware")');
    await page.click('.center .row:has-text("README.md")');
    await page.click('.center .row:has-text("server.go")', { modifiers: ['Control'] });
    await page.dragAndDrop('.center .row:has-text("server.go")', '.right .dropzone');
    await page.waitForSelector('.dialog:has-text("конфликт 1 из 2")');
    await page.waitForSelector('.dialog .newer');
    await page.waitForSelector('.dialog:has-text("Для всех оставшихся конфликтов (2)")');
  });
  await page.screenshot({ path: OUT + '/4-conflict.png' });
  await step('step through: replace, back, replace, keep both', async () => {
    await page.click('.dialog button:has-text("Заменить")');
    await page.waitForSelector('.dialog:has-text("конфликт 2 из 2")');
    await page.click('.dialog button:has-text("Назад")');
    await page.waitForSelector('.dialog:has-text("конфликт 1 из 2")');
    await page.click('.dialog button:text-is("Заменить")');
    await page.click('.dialog button:text-is("Оставить оба")');
    await page.waitForSelector('.right .row:has-text("server.go (2)")');
    await page.waitForSelector('.dock .log-entry:has-text("заменён: README.md")');
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

  await step('merge with conflict, then abort', async () => {
    await page.click('.left .item:has-text("origin/release-2.1")', { button: 'right' });
    await page.click('.menu button:has-text("Слить в feature/login")');
    await page.waitForSelector('.dialog:has-text("конфликтах")');
    await page.click('.dialog button:has-text("Отменить слияние")');
    await page.waitForSelector('.log-entry .cmd.ok:has-text("git merge-abort")');
  });
  await step('delete unmerged branch asks twice', async () => {
    await page.click('.left .item:has-text("fix/api-timeout")', { button: 'right' });
    await page.click('.menu button:has-text("Удалить ветку…")');
    await page.click('.dialog footer button:has-text("Удалить")');
    await page.waitForSelector('.dialog:has-text("неслитые коммиты")');
    await page.click('.dialog footer button:has-text("Удалить всё равно")');
    await page.waitForSelector('.log-entry .cmd.ok:has-text("git branch -D fix/api-timeout")');
    await page.waitForFunction(() => ![...document.querySelectorAll('.left .item')].some((e) => e.textContent.includes('fix/api-timeout')));
  });

  await step('file: edit, draft, save via Ctrl+S', async () => {
    await page.click('.seg button:has-text("Файлы")');
    await page.dblclick('.center .row:has-text("README.md")');
    await page.waitForSelector('.cm-content');
    await page.click('.cm-content');
    await page.keyboard.press('Control+End');
    await page.keyboard.type('Новая строка');
    await page.waitForSelector('.dirty');
    await page.keyboard.press('Control+s');
    await page.waitForSelector('.toast:has-text("Сохранено")');
  });
  await step('editor: search panel in Russian', async () => {
    await page.click('.cm-content');
    await page.keyboard.press('Control+f');
    await page.waitForSelector('.cm-search input[placeholder="Найти"]');
    await page.keyboard.type('myrepo');
    await page.waitForSelector('.cm-searchMatch');
  });
  await page.screenshot({ path: OUT + '/6-editor.png' });
  await page.keyboard.press('Escape');

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
    await page.waitForSelector('.cm-activeLine');
    const line = await page.$eval('.cm-activeLine', (a) => a.textContent);
    if (!line.includes('func main')) throw new Error('line 3 not active: ' + JSON.stringify(line));
  });

  await step('terminal: open, type, reconnect after reload', async () => {
    await page.keyboard.press('Control+Backquote');
    await page.waitForSelector('.term-screen:not([hidden]) .xterm');
    await page.waitForFunction(() => document.querySelector('.term-screen:not([hidden]) .xterm-rows')?.textContent.includes('sysadmin@nvme-lab'));
    await page.keyboard.type('pwd');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => document.querySelector('.term-screen:not([hidden]) .xterm-rows').textContent.includes('/home/sysadmin/projects/myrepo'));
  });
  await page.screenshot({ path: OUT + '/12-terminal.png' });
  await step('terminal: Ctrl+K goes to the shell, not the palette', async () => {
    await page.keyboard.press('Control+k');
    await page.waitForTimeout(200);
    if (await page.$('.palette')) throw new Error('palette opened inside terminal');
  });
  await step('terminal survives reload', async () => {
    await page.reload();
    await page.waitForSelector('.dock-tabs .t.on:has-text("Терминал")');
    await page.waitForFunction(() => document.querySelector('.term-screen:not([hidden]) .xterm-rows')?.textContent.includes('/home/sysadmin/projects/myrepo'));
  });

  await step('tasks: select lists detected tasks, Scripts opens the editor', async () => {
    await page.waitForSelector('.task-select optgroup[label="Найдены в проекте"] option:has-text("go test")', { state: 'attached' });
    await page.click('.task-edit');
    await page.click('.dialog footer button:has-text("Новый скрипт")');
    await page.fill('.dialog input[aria-label="Название"]', 'Сборка и бэкап');
    await page.fill('.dialog textarea[aria-label="Команды, по одной в строке"]', 'make build\ntar czf ~/b-{branch}.tgz bin/');
    await page.click('.dialog footer button:has-text("Сохранить")');
    await page.waitForSelector('.dialog .task-row:has-text("Сборка и бэкап")');
  });
  await page.screenshot({ path: OUT + '/13-scripts.png' });
  await step('tasks: choose in the list and run with ▶, tab gets ✓', async () => {
    await page.keyboard.press('Escape');
    await page.selectOption('.task-select', { label: 'Сборка и бэкап' });
    await page.click('.task-run');
    await page.waitForSelector('.term-tabs .t.on:has-text("▶ Сборка и бэкап")');
    await page.waitForSelector('.term-tabs .t.on .ok');
    await page.waitForSelector('.tab.active .badge.ok');
    await page.waitForFunction(() => document.querySelector('.term-screen:not([hidden]) .xterm-rows').textContent.includes('готово'));
  });
  await page.screenshot({ path: OUT + '/14-task-run.png' });
  await step('tasks: selection is remembered after reload', async () => {
    await page.reload();
    await page.waitForFunction(() => document.querySelector('.task-select')?.selectedOptions[0]?.textContent === 'Сборка и бэкап');
  });
  await step('tasks: repository task asks for trust first', async () => {
    await page.selectOption('.task-select', { label: 'Линтер · нужно одобрить' });
    await page.click('.task-run');
    await page.waitForSelector('.dialog:has-text("Доверять задачам этого репозитория?") li:has-text("golangci-lint run")');
    await page.click('.dialog footer button:has-text("Доверять и запустить")');
    await page.waitForSelector('.term-tabs .t.on:has-text("▶ Линтер")');
  });
  await step('tasks: Ctrl+Shift+B reruns the last task', async () => {
    const before = await page.$$eval('.term-tabs .t', (els) => els.length);
    await page.keyboard.press('Control+Shift+B');
    await page.waitForFunction((n) => document.querySelectorAll('.term-tabs .t').length > n, before);
  });
  await step('tasks: dock tab lists runs', async () => {
    await page.click('.dock-tabs .t:has-text("Задачи")');
    await page.waitForSelector('.dock-body .link-row:has-text("Линтер")');
  });
  await page.screenshot({ path: OUT + '/15-tasks-tab.png' });

  await step('github: add repository with a deploy key', async () => {
    await page.click('.topbar button:has-text("myrepo")');
    await page.click('.menu button:has-text("Добавить репозиторий…")');
    await page.fill('.dialog input[aria-label="Адрес репозитория на GitHub"]', 'https://github.com/acme/widgets');
    const dest = await page.inputValue('.dialog input[aria-label="Куда клонировать на сервере"]');
    if (!dest.endsWith('/widgets')) throw new Error('folder not suggested: ' + dest);
    await page.click('.dialog button:has-text("Далее")');
    await page.waitForSelector('.dialog textarea.key-box');
    await page.waitForSelector('.dialog a[href="https://github.com/acme/widgets/settings/keys/new"]');
    await page.click('.dialog button:has-text("Проверить доступ")');
    await page.waitForSelector('.dialog .bad:has-text("не принял ключ")');
    await page.click('.dialog button:has-text("Проверить доступ")');
    await page.waitForSelector('.dialog .ok:has-text("через порт 443")');
  });
  await page.screenshot({ path: OUT + '/16-github-key.png' });
  await step('github: clone shows output and opens the new repository', async () => {
    await page.click('.dialog button:has-text("Клонировать")');
    await page.waitForSelector('.term-tabs .t.on:has-text("⇣ clone acme/widgets")');
    await page.waitForSelector('.topbar button:has-text("widgets")');
    if (await page.$('.left .item:has-text("myrepo")')) throw new Error('repositories must not be listed on the left');
    await page.waitForFunction(() => decodeURIComponent(location.hash).includes('/repo/r3'));
  });
  await page.screenshot({ path: OUT + '/17-cloned.png' });

  // телефон
  const m = await browser.newContext({ ...devices['iPhone 13'], locale: 'ru-RU' });
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
  await step('mobile: terminal pane with extra keys', async () => {
    await mp.click('.mnav button:has-text("Терминал")');
    await mp.waitForSelector('.term-screen:not([hidden]) .xterm');
    await mp.waitForSelector('.term-keys button:has-text("Esc")');
  });
  await mp.screenshot({ path: OUT + '/11-mobile-out.png' });

  // английский интерфейс: переключатель на экране входа, выбор запоминается
  const en = await browser.newContext({ viewport: { width: 1440, height: 860 }, locale: 'ru-RU' });
  const ep = await en.newPage();
  watch(ep);
  await ep.request.post(URL + 'api/logout', { headers: { 'X-CSRF-Token': 'tok' } }); // заглушка: вход общий
  await ep.goto(URL);
  await step('english: switch on the login screen', async () => {
    await ep.click('.login .seg button:has-text("EN")');
    await ep.waitForSelector('.login button[type=submit]:has-text("Sign in")');
    await ep.waitForSelector('.login p:has-text("server account")'); // подсказка режима PAM
  });
  await step('english: main screen after reload', async () => {
    await ep.fill('input[autocomplete=username]', 'sysadmin');
    await ep.fill('input[type=password]', 'secret');
    await ep.click('button[type=submit]');
    await ep.goto(URL + '#/repo/r1/wt/feature%2Flogin');
    await ep.waitForSelector('.topbar button:has-text("Open server folders")');
    await ep.waitForSelector('.task-edit:has-text("Scripts")');
    await ep.waitForSelector('.row.head:has-text("Last commit")');
    if ((await ep.getAttribute('html', 'lang')) !== 'en') throw new Error('html lang');
  });
  await ep.screenshot({ path: OUT + '/18-english.png' });
  await step('english: back to Russian from the user menu', async () => {
    await ep.click('.topbar button:has-text("sysadmin@nvme-lab")');
    await ep.click('.menu button:has-text("Русский")');
    await ep.waitForSelector('.topbar button:has-text("Открыть локальную ФС")');
  });

  console.log('errors:', errors.length ? errors : 'none');
  await browser.close();
})();

// Точка входа: вход, каркас экрана, команды, маршрут.
import { h, mount, toast } from './ui.js';
import { t, lang, setLang } from './i18n.js';
import { api, setCSRF, events } from './api.js';
import { state, on, emit } from './store.js';
import { register } from './commands.js';
import * as act from './actions.js';
import { loginView } from './views/login.js';
import { topbar, tabs, newBranch } from './views/top.js';
import { left } from './views/left.js';
import { center } from './views/center.js';
import { dock } from './views/dock.js';
import { serverPanel } from './views/server.js';
import { registerTreeCommands } from './views/tree.js';
import { currentEditor } from './views/editor.js';
import { addRepoDialog } from './views/addrepo.js';
import { selectedTask, runTask, editMine, rerunLast } from './views/tasks.js';
import { quickOpen } from './views/quickopen.js';
import { setSearchPrefill, editorSelection } from './views/search.js';
import { attachKeyDialog } from './views/addrepo.js';
import { setTheme, themeMode } from './theme.js';

const app = document.getElementById('app');

async function boot() {
  try {
    const me = await api.get('/api/me');
    start(me);
  } catch (err) {
    if (err.status === 401) return showLogin();
    mount(app, h('div', { class: 'login' }, h('h1', {}, 'gitaway'), h('p', { class: 'err' }, t('boot.failed', { msg: err.message })),
      h('button', { class: 'btn', onclick: () => location.reload() }, t('boot.retry'))));
  }
}

function showLogin() {
  document.body.classList.remove('fs-open');
  loginView(app, async () => {
    try {
      start(await api.get('/api/me'));
    } catch (err) {
      toast(err.message, { bad: true });
    }
  });
}

let started = false;

async function start(me) {
  setCSRF(me.csrf);
  state.me = me;
  if (!started) {
    started = true;
    buildShell();
    registerCommands();
    addEventListener('hashchange', () => act.applyRoute());
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') act.refreshAll(); // правки в терминале, pull снаружи
    });
    events.addEventListener('unauthorized', () => location.reload());
  }
  emit('me');
  try {
    await Promise.all([act.loadRepos(), act.loadSettings()]);
  } catch (err) {
    toast(err.message, { bad: true });
  }
  await act.applyRoute();
}

function buildShell() {
  const top = h('header', { class: 'topbar' });
  const tabBar = h('div', { class: 'tabs', 'aria-label': t('tab.list') });
  const leftEl = h('aside', { class: 'left', 'aria-label': t('pane.left') });
  const dockEl = h('section', { class: 'dock', 'aria-label': t('pane.out') });
  const centerEl = h('main', { class: 'center' }, dockEl);
  const rightEl = h('aside', { class: 'right', 'aria-label': t('fs.title') });
  const mnav = h('nav', { class: 'mnav', 'aria-label': t('pane.nav') });
  mount(app, top, tabBar, h('div', { class: 'main' }, leftEl, centerEl, rightEl), mnav);

  topbar(top);
  tabs(tabBar);
  left(leftEl);
  state.center = center(centerEl);
  const d = dock(dockEl);
  state.dock = d;
  serverPanel(rightEl);

  // телефон: одна колонка и переключатель внизу
  const panes = [['left', t('pane.left')], ['center', t('pane.center')], ['right', t('pane.right')], ['out', t('pane.out')]];
  const renderNav = () => {
    document.body.dataset.pane = state.pane;
    mount(mnav, panes.map(([id, label]) => h('button', {
      class: state.pane === id ? 'on' : null, 'aria-pressed': String(state.pane === id),
      onclick: () => setPane(id),
    }, label)));
  };
  on('pane', renderNav);
  on('route', () => { if (state.pane === 'left') setPane('center'); });
  renderNav();
}

function setPane(id) {
  state.pane = id;
  if (id === 'right' && !state.fsOpen) setFs(true);
  if (id === 'out') state.dock?.show('terminal');
  emit('pane');
}

function setFs(open) {
  state.fsOpen = open;
  document.body.classList.toggle('fs-open', open);
  emit('fs');
}

function registerCommands() {
  const hasWt = () => !!state.wt;
  register({ id: 'git.fetch', title: t('cmd.fetch'), when: hasWt, run: () => act.gitOp('fetch') });
  register({ id: 'git.pull', title: t('cmd.pull'), when: hasWt, run: () => act.gitOp('pull') });
  register({ id: 'git.push', title: t('cmd.push'), when: hasWt, run: () => act.pushBranch() });
  register({ id: 'fs.toggle', title: t('cmd.fsToggle'), keys: 'Ctrl+Shift+E', run: () => {
    setFs(!state.fsOpen);
    if (innerWidth < 900) setPane(state.fsOpen ? 'right' : 'center');
  } });
  register({ id: 'repo.add', title: t('repo.add'), run: () => addRepoDialog() });
  register({ id: 'branch.new', title: t('branches.new'), when: () => !!state.repo, run: newBranch });
  register({ id: 'view.files', title: t('cmd.files'), when: hasWt, run: () => act.nav({ view: 'tree', path: '' }) });
  register({ id: 'view.changes', title: t('cmd.changes'), keys: 'Ctrl+Shift+G', when: hasWt, run: () => act.nav({ view: 'changes', path: '' }) });
  register({ id: 'view.history', title: t('cmd.history'), keys: 'Ctrl+Shift+H', when: hasWt, run: () => act.nav({ view: 'history', path: '', commit: '' }) });
  register({ id: 'history.file', title: t('cmd.fileHistory'), when: () => hasWt() && state.route.view === 'file',
    run: () => act.nav({ view: 'history', path: state.route.path, commit: '' }) });
  register({ id: 'quick.open', title: t('quick.title'), keys: 'Ctrl+P', when: hasWt, run: quickOpen });
  register({ id: 'search.show', title: t('search.title'), keys: 'Ctrl+Shift+F', when: hasWt, run: () => {
    const sel = editorSelection();
    if (innerWidth < 900) setPane('center');
    if (state.route.view === 'search') {
      const v = state.center?.current();
      if (sel) v?.setQuery?.(sel);
      else v?.focus?.();
      return;
    }
    setSearchPrefill(sel);
    act.nav({ view: 'search', path: '' });
  } });
  register({ id: 'repo.attachKey', title: t('key.attachTitle'), when: () => !!state.repo, run: () => attachKeyDialog(state.repo) });
  register({ id: 'file.save', title: t('file.save'), keys: 'Ctrl+S', when: () => !!currentEditor(), run: () => currentEditor().save() });
  register({ id: 'dock.output', title: t('cmd.output'), keys: 'Ctrl+J', run: () => state.dock?.show('output') });
  register({ id: 'term.new', title: t('term.new'), keys: 'Ctrl+`', inTerminal: true, run: () => {
    if (innerWidth < 900) setPane('out');
    return state.dock?.newTerminal();
  } });
  register({ id: 'term.show', title: t('cmd.terminal'), run: () => state.dock?.show('terminal') });
  register({ id: 'task.run', title: t('cmd.taskRun'), run: () => { const x = selectedTask(); if (x) runTask(x); } });
  register({ id: 'task.scripts', title: t('tasks.editMine'), run: editMine });
  register({ id: 'task.rerun', title: t('cmd.taskRerun'), keys: 'Ctrl+Shift+B', inTerminal: true, run: rerunLast });
  register({ id: 'tasks.show', title: t('cmd.tasks'), run: () => state.dock?.show('tasks') });
  register({ id: 'tab.next', title: t('cmd.nextTab'), keys: 'Alt+PageDown', when: () => state.worktrees.length > 1, run: () => cycleTab(1) });
  register({ id: 'tab.prev', title: t('cmd.prevTab'), keys: 'Alt+PageUp', when: () => state.worktrees.length > 1, run: () => cycleTab(-1) });
  register({ id: 'logout', title: t('auth.logout'), run: async () => {
    try {
      await api.post('/api/logout');
    } finally {
      location.hash = '';
      location.reload();
    }
  } });
  register({ id: 'theme.light', title: t('cmd.themeLight'), when: () => themeMode() !== 'light', run: () => setTheme('light') });
  register({ id: 'theme.dark', title: t('cmd.themeDark'), when: () => themeMode() !== 'dark', run: () => setTheme('dark') });
  register({ id: 'theme.auto', title: t('cmd.themeAuto'), when: () => themeMode() !== 'auto', run: () => setTheme('auto') });
  register({ id: 'lang.ru', title: t('cmd.langRu'), when: () => lang !== 'ru', run: () => setLang('ru') });
  register({ id: 'lang.en', title: t('cmd.langEn'), when: () => lang !== 'en', run: () => setLang('en') });
  registerTreeCommands(register);
}

function cycleTab(step) {
  const i = state.worktrees.findIndex((w) => w.path === state.wt?.path);
  const next = state.worktrees[(i + step + state.worktrees.length) % state.worktrees.length];
  act.nav({ wt: next.branch || next.path.split('/').pop(), view: 'tree', path: '' });
}

boot();

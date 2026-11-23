// Загрузка данных и операции git. Представления вызывают эти функции и слушают события store.
import { api } from './api.js';
import { state, emit, wtParam, tabName, addLog } from './store.js';
import { parse, go } from './router.js';
import { h, toast, confirm } from './ui.js';
import { t } from './i18n.js';

export async function loadRepos() {
  state.repos = await api.get('/api/repos');
  emit('repos');
}

export async function loadSettings() {
  state.settings = await api.get('/api/settings');
  emit('settings');
}

export async function saveSettings(patch) {
  state.settings = await api.put('/api/settings', patch);
  emit('settings');
}

// --- маршрут ---

let routeSeq = 0;

// applyRoute приводит состояние к адресу: выбирает репозиторий и вкладку, грузит данные.
export async function applyRoute() {
  const seq = ++routeSeq;
  const r = parse();
  state.route = r;
  if (!r.repo) {
    if (state.repos.length) return go({ repo: state.repos[0].id }, true);
    state.repo = null;
    state.worktrees = [];
    state.wt = null;
    emit('repo', 'worktrees', 'route');
    return;
  }
  if (state.repo?.id !== r.repo) {
    const repo = state.repos.find((x) => x.id === r.repo);
    if (!repo) {
      toast(t('repo.notFound'), { bad: true });
      return go({}, true);
    }
    state.repo = repo;
    state.wt = null;
    state.worktrees = [];
    state.branches = [];
    state.wtStatus = new Map();
    emit('repo');
    await loadWorktrees();
    if (seq !== routeSeq) return;
  }
  const wt = state.worktrees.find((w) => tabName(w) === r.wt) || state.worktrees[0] || null;
  if (wt && r.wt !== tabName(wt)) return go({ ...r, wt: tabName(wt) }, true);
  if (state.wt?.path !== wt?.path) {
    state.wt = wt;
    state.status = state.wtStatus.get(wt?.path) || null;
    emit('wt', 'status');
    loadBranches();
  }
  emit('route');
}

// nav меняет часть адреса, сохраняя остальное (строка сбрасывается).
export function nav(patch, replace = false) {
  go({ ...state.route, line: 0, ...patch }, replace);
}

// --- данные репозитория ---

export async function loadWorktrees() {
  if (!state.repo) return;
  state.worktrees = (await api.get('/api/worktrees', { repo: state.repo.id })).filter((w) => !w.bare && !w.prunable);
  if (state.wt) state.wt = state.worktrees.find((w) => w.path === state.wt.path) || null;
  emit('worktrees');
  await refreshStatus(true);
}

export async function loadBranches() {
  if (!state.repo) return;
  try {
    state.branches = await api.get('/api/branches', { repo: state.repo.id, wt: wtParam() });
  } catch (err) {
    state.branches = [];
    toast(err.message, { bad: true });
  }
  emit('branches');
}

// refreshStatus перечитывает статус активной вкладки (all: всех вкладок, для значков).
export async function refreshStatus(all = false) {
  if (!state.repo) return;
  const list = all ? state.worktrees : state.wt ? [state.wt] : [];
  await Promise.all(list.map(async (w) => {
    try {
      const st = await api.get('/api/status', { repo: state.repo.id, wt: wtParam(w) });
      state.wtStatus.set(w.path, st);
    } catch {
      state.wtStatus.delete(w.path);
    }
  }));
  state.status = state.wt ? state.wtStatus.get(state.wt.path) || null : null;
  emit('status');
}

function setStatus(st) {
  if (!st || !state.wt) return;
  state.wtStatus.set(state.wt.path, st);
  state.status = st;
  emit('status');
}

// refreshAll: после операции, при возврате на вкладку браузера и по кнопке «Обновить».
export async function refreshAll() {
  if (!state.repo) return;
  await Promise.all([refreshStatus(true), loadBranches()]);
  emit('changed'); // таблица файлов и серверная панель перечитают каталог
}

// --- операции git ---

function logResult(cmd, ok, output) {
  addLog({ cmd, ok, output: (output || '').trim() });
}

// gitOp выполняет fetch, pull, push, switch или update-branch в активной вкладке (или в wt).
export async function gitOp(op, body = {}, wt = state.wt) {
  const cmd = 'git ' + op + (body.branch ? ' ' + body.branch : '');
  try {
    const res = await api.post('/api/git/' + op, { repo: state.repo.id, wt: wtParam(wt), ...body });
    logResult(cmd, true, res.output || t('log.done'));
    return res;
  } catch (err) {
    logResult(cmd, false, err.message);
    toast(t('git.failed', { op }) + ': ' + err.message, { bad: true });
    err.shown = true;
    throw err;
  } finally {
    refreshAll();
  }
}

export async function pushBranch(wt = state.wt) {
  const st = state.wtStatus.get(wt.path);
  const body = {};
  if (st && !st.upstream && wt.branch) Object.assign(body, { remote: 'origin', branch: wt.branch, set_upstream: true });
  return gitOp('push', body, wt);
}

// pushNamed отправляет ветку по имени: из её вкладки, если она открыта, иначе из основной.
export async function pushNamed(branch) {
  const wt = state.worktrees.find((w) => w.branch === branch.name);
  if (wt) return pushBranch(wt);
  const remote = branch.upstream ? branch.upstream.split('/')[0] : 'origin';
  return gitOp('push', { remote, branch: branch.name, set_upstream: !branch.upstream }, state.worktrees[0]);
}

export async function switchBranch(name) {
  await gitOp('switch', { branch: name });
  await loadWorktrees();
  go({ repo: state.repo.id, wt: name });
}

// openBranchTab открывает ветку во вкладке (git worktree). Для удалённой ветки
// создаётся локальная с тем же именем, отслеживающая удалённую; {create, start}
// создаёт новую ветку от start.
export async function openBranchTab(branch) {
  const existing = state.worktrees.find((w) => w.branch === branch.name);
  if (existing) return go({ repo: state.repo.id, wt: tabName(existing) });
  let body = { repo: state.repo.id, branch: branch.name };
  if (branch.create) {
    body = { repo: state.repo.id, branch: branch.name, create: true, start: branch.start || '' };
  } else if (branch.remote) {
    const local = branch.name.slice(branch.name.indexOf('/') + 1);
    const open = state.worktrees.find((w) => w.branch === local);
    if (open) return go({ repo: state.repo.id, wt: tabName(open) });
    const hasLocal = state.branches.some((b) => !b.remote && b.name === local);
    body = hasLocal ? { repo: state.repo.id, branch: local } : { repo: state.repo.id, branch: local, create: true, start: branch.name };
  }
  try {
    const res = await api.post('/api/worktrees', body);
    logResult('git worktree add ' + body.branch, true, res.output);
  } catch (err) {
    logResult('git worktree add ' + body.branch, false, err.message);
    toast(err.message, { bad: true });
    return;
  }
  await loadWorktrees();
  await loadBranches();
  go({ repo: state.repo.id, wt: body.branch });
}

export async function closeTab(wt) {
  if (wt.main) return;
  const st = state.wtStatus.get(wt.path);
  const dirty = st?.changes?.length > 0;
  const ok = await confirm(t('tab.closeTitle', { name: tabName(wt) }),
    dirty ? t('tab.closeDirty', { n: st.changes.length }) : t('tab.closeBody'), t('tab.close'), dirty);
  if (!ok) return;
  try {
    const res = await api.del('/api/worktrees', { repo: state.repo.id, wt: wt.path, force: dirty });
    logResult('git worktree remove ' + tabName(wt), true, res.output || t('log.done'));
  } catch (err) {
    logResult('git worktree remove ' + tabName(wt), false, err.message);
    toast(err.message, { bad: true });
    return;
  }
  const wasActive = state.wt?.path === wt.path;
  await loadWorktrees();
  if (wasActive) go({ repo: state.repo.id, wt: tabName(state.worktrees[0]) });
}

export async function addRepo(path) {
  const repo = await api.post('/api/repos', { path });
  await loadRepos();
  go({ repo: repo.id });
}

export async function removeRepo(repo) {
  const ok = await confirm(t('repo.removeTitle'), t('repo.removeBody', { path: repo.path }), t('repo.remove'), true);
  if (!ok) return;
  await api.del('/api/repos', { id: repo.id });
  await loadRepos();
  if (state.repo?.id === repo.id) go({}, true);
}

// --- изменения ---

async function changeOp(op, body, cmd) {
  try {
    const res = await api.post('/api/git/' + op, { repo: state.repo.id, wt: wtParam(), ...body });
    setStatus(res.status);
    emit('changed');
    if (cmd) logResult(cmd, true, res.output || t('log.done'));
    return res;
  } catch (err) {
    if (cmd) logResult(cmd, false, err.message);
    toast(err.message, { bad: true });
    err.shown = true;
    throw err;
  }
}

export const stage = (paths) => changeOp('stage', { paths });
export const unstage = (paths) => changeOp('unstage', { paths });

export async function discard(paths) {
  const list = paths.length <= 30
    ? h('ul', {}, paths.map((p) => h('li', {}, p)))
    : h('p', {}, t('changes.nFiles', { n: paths.length }));
  const ok = await confirm(t('changes.discardTitle'), [h('p', {}, t('changes.discardBody')), list], t('changes.discard'), true);
  if (!ok) return null;
  return changeOp('discard', { paths }, 'git restore ' + (paths.length === 1 ? paths[0] : t('changes.nFiles', { n: paths.length })));
}

export async function commit(message) {
  const res = await changeOp('commit', { message }, 'git commit');
  toast(t('changes.committed', { hash: res.commit.slice(0, 7) }));
  refreshAll();
  return res;
}

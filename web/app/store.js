// Состояние приложения и шина событий. Представления подписываются на события
// и перерисовывают свою часть экрана.

export const state = {
  me: null,          // {user, host, roots, default_folder}
  settings: null,    // настройки серверной панели
  repos: [],
  repo: null,        // выбранный репозиторий
  worktrees: [],     // вкладки-ветки выбранного репозитория
  wt: null,          // активная вкладка (worktree)
  branches: [],
  status: null,      // git status активной вкладки
  wtStatus: new Map(), // путь worktree -> status (значки на вкладках)
  route: null,       // разобранный адрес
  fsOpen: false,     // серверная панель открыта
  pane: 'center',    // видимая колонка на телефоне: left, center, right, out
  log: [],           // вывод операций для нижней панели
};

export const bus = new EventTarget();

// emit сообщает об изменении: 'repos', 'repo', 'worktrees', 'status', 'branches',
// 'route', 'fs', 'settings', 'log', 'pane'.
export function emit(...names) {
  for (const n of names) bus.dispatchEvent(new Event(n));
}

export function on(names, fn) {
  for (const n of names.split(' ')) bus.addEventListener(n, fn);
}

// wtParam это значение параметра wt для API: путь worktree, для основной папки пусто.
export function wtParam(wt = state.wt) {
  return wt && !wt.main ? wt.path : '';
}

// tabName это имя вкладки и сегмент адреса: ветка, иначе имя папки (detached HEAD).
export function tabName(wt) {
  return wt.branch || wt.path.split('/').pop();
}

export function addLog(entry) {
  state.log.push({ time: Date.now(), ...entry });
  if (state.log.length > 200) state.log.shift();
  emit('log');
}

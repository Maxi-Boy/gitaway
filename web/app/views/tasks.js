// Задачи: мои скрипты, задачи из .gitaway.json (после одобрения) и автоопределённые.
// Запуск идёт сеансом на сервере: вывод открывается в терминале и переживает закрытие браузера.
import { h, mount, dialog, confirm, toast, stamp } from '../ui.js';
import { t, lang } from '../i18n.js';
import { api } from '../api.js';
import { state, emit, on, wtParam } from '../store.js';

// Выбранная задача запоминается для каждого репозитория (как конфигурация запуска в IDE).
const SEL_KEY = 'gitaway:task-selected';

function readSelected() {
  try {
    return JSON.parse(localStorage.getItem(SEL_KEY)) || {};
  } catch {
    return {};
  }
}

export function setSelected(id) {
  const all = readSelected();
  all[state.repo?.id || ''] = id;
  try {
    localStorage.setItem(SEL_KEY, JSON.stringify(all));
  } catch { /* не запомнится */ }
  emit('tasks');
}

// selectedTask возвращает выбранную задачу; если выбора нет или задачи больше нет, первую из списка.
export function selectedTask() {
  const list = allTasks();
  const id = readSelected()[state.repo?.id || ''];
  return list.find((x) => x.id === id) || list[0] || null;
}

// loadTasks перечитывает задачи для активной вкладки.
export async function loadTasks() {
  try {
    state.tasks = await api.get('/api/tasks', state.repo ? { repo: state.repo.id, wt: wtParam() } : {});
  } catch (err) {
    state.tasks = { mine: [], auto: [], repo: null, error: err.message };
  }
  emit('tasks');
}

function allTasks() {
  const tk = state.tasks || {};
  return [...(tk.mine || []), ...(tk.repo?.tasks || []), ...(tk.auto || [])];
}

// runTask запускает задачу: подтверждение (если задано), одобрение файла репозитория.
export async function runTask(task) {
  if (task.id.startsWith('repo:') && !state.tasks?.repo?.trusted) {
    if (!await trustDialog()) return;
  }
  if (task.confirm && !await confirm(t('tasks.confirmTitle', { name: task.name }), stepsList(task), t('tasks.run'))) return;
  askNotify();
  try {
    const info = await api.post('/api/tasks/run', { repo: state.repo?.id || '', wt: wtParam(), id: task.id, cols: 120, rows: 30, lang });
    state.lastTask = task;
    if (innerWidth < 900) { state.pane = 'out'; emit('pane'); }
    await state.dock?.openSession(info);
  } catch (err) {
    if (err.code === 'not_trusted') {
      await loadTasks();
      return runTask(task);
    }
    toast(err.message, { bad: true });
  }
}

// rerunLast повторяет последнюю задачу, а если ещё ничего не запускалось, выбранную.
export function rerunLast() {
  const last = (state.lastTask && allTasks().find((x) => x.id === state.lastTask.id)) || selectedTask();
  if (last) return runTask(last);
  toast(t('tasks.noLast'));
  return null;
}

function stepsList(task) {
  return h('ol', { class: 'steps' }, task.steps.map((s) => h('li', { class: 'mono' }, s)));
}

// trustDialog показывает задачи из .gitaway.json и спрашивает, доверять ли им.
// Одобряется именно эта версия файла (по хешу): после изменения вопрос появится снова.
async function trustDialog() {
  await loadTasks();
  const rt = state.tasks?.repo;
  if (!rt?.tasks?.length) return false;
  if (rt.trusted) return true;
  const ok = await confirm(t('tasks.trustTitle'), [
    h('p', {}, t('tasks.trustBody')),
    rt.tasks.map((task) => [h('p', { style: { margin: '10px 0 4px', fontWeight: '600' } }, task.name), stepsList(task)]),
    h('p', { class: 'muted' }, t('tasks.trustNote')),
  ], t('tasks.trustOk'));
  if (!ok) return false;
  try {
    await api.post('/api/tasks/trust', { repo: state.repo.id, wt: wtParam(), hash: rt.hash });
  } catch (err) {
    toast(err.code === 'changed' ? t('tasks.trustChanged') : err.message, { bad: true });
    await loadTasks();
    return false;
  }
  await loadTasks();
  return true;
}

// taskControls это блок в верхней панели: выпадающий список задач, «▶» и «Скрипты».
export function taskControls() {
  const tk = state.tasks || {};
  const sel = selectedTask();
  const opt = (task, note) => h('option', { value: task.id, selected: sel?.id === task.id }, task.name + (note ? ' · ' + note : ''));
  const group = (label, list, note) => (list?.length ? h('optgroup', { label }, list.map((x) => opt(x, note))) : null);
  const select = h('select', {
    class: 'task-select', 'aria-label': t('tasks.select'), title: sel ? sel.steps.join(' ; ') : '',
    disabled: !sel, onchange: (e) => setSelected(e.target.value),
  },
  sel ? null : h('option', {}, t('tasks.none')),
  group(t('tasks.mine'), tk.mine),
  group(t('tasks.repo'), tk.repo?.tasks, tk.repo && !tk.repo.trusted ? t('tasks.needsTrust') : ''),
  group(t('tasks.auto'), tk.auto));
  return h('span', { class: 'task-controls' }, select,
    h('button', {
      class: 'btn task-run', disabled: !sel, 'aria-label': t('tasks.run'),
      title: sel ? t('tasks.runNamed', { name: sel.name }) : t('tasks.none'),
      onclick: () => { const x = selectedTask(); if (x) runTask(x); },
    }, '▶'),
    h('button', { class: 'btn task-edit', title: t('tasks.editMine'), onclick: () => editMine() }, t('tasks.scripts')));
}

// --- уведомление о завершении ---

function askNotify() {
  if ('Notification' in window && Notification.permission === 'default') {
    Notification.requestPermission().catch(() => {});
  }
}

// taskFinished вызывается терминалом, когда задача завершилась на глазах у пользователя.
export function taskFinished(info) {
  const ok = info.exit_code === 0;
  const text = ok ? t('tasks.doneOk', { name: info.title }) : t('tasks.doneFail', { name: info.title, code: info.exit_code });
  toast(text, { bad: !ok });
  if ('Notification' in window && Notification.permission === 'granted' && document.visibilityState !== 'visible') {
    try {
      new Notification('gitaway', { body: text, tag: 'task-' + info.id });
    } catch { /* уведомления недоступны в этом контексте */ }
  }
}

// --- редактор моих скриптов ---

export async function editMine() {
  let list;
  try {
    list = await api.get('/api/tasks/mine');
  } catch (err) {
    toast(err.message, { bad: true });
    return;
  }
  const box = h('div', {});
  let d = null;
  const save = async (next) => {
    try {
      list = await api.put('/api/tasks/mine', next);
      render();
      loadTasks();
      return true;
    } catch (err) {
      toast(err.message, { bad: true });
      return false;
    }
  };
  const render = () => {
    mount(box,
      list.length ? h('div', { class: 'task-list' }, list.map((task, i) => h('div', { class: 'task-row' },
        h('div', { class: 'grow' }, h('div', { style: { fontWeight: '600' } }, task.name),
          h('div', { class: 'muted mono ellipsis' }, task.steps.join(' ; ')),
          h('div', { class: 'muted', style: { fontSize: '12px' } }, scopeText(task), ' · ', task.cwd || t('tasks.cwdWorktree'))),
        h('button', { class: 'btn small', onclick: () => editOne(task, (v) => save(list.map((x, k) => (k === i ? v : x)))) }, t('tasks.edit')),
        h('button', { class: 'btn small danger', onclick: async () => {
          if (await confirm(t('tasks.deleteTitle', { name: task.name }), '', t('tasks.delete'), true)) save(list.filter((_, k) => k !== i));
        } }, t('tasks.delete'))))) : h('p', { class: 'muted' }, t('tasks.mineEmpty')),
      h('p', { class: 'muted', style: { fontSize: '12px', marginTop: '12px' } }, t('tasks.placeholders')));
  };
  render();
  d = dialog({
    title: t('tasks.mineTitle'), body: box, mid: true,
    buttons: [{ label: t('tasks.new'), primary: true, run: () => editOne(null, (v) => save([...list, v])) }, 'grow', { label: t('common.close'), value: null }],
  });
  void d;
}

function scopeText(task) {
  if (!task.scope) return t('tasks.scopeAll');
  const repo = state.repos.find((r) => r.id === task.scope);
  return t('tasks.scopeOne', { repo: repo ? repo.path.split('/').pop() : task.scope });
}

// editOne показывает форму скрипта; onSave получает задачу и возвращает true при успехе.
function editOne(task, onSave) {
  const v = task || { name: '', steps: [], cwd: '', scope: '', confirm: false, continue_on_error: false, timeout: '' };
  const name = h('input', { type: 'text', value: v.name, placeholder: t('tasks.namePh'), 'aria-label': t('tasks.name') });
  const steps = h('textarea', { class: 'mono', rows: 6, placeholder: t('tasks.stepsPh'), 'aria-label': t('tasks.steps') });
  steps.value = v.steps.join('\n');
  const inServer = !!v.cwd;
  const cwdWt = h('input', { type: 'radio', name: 'cwd', checked: !inServer });
  const cwdDir = h('input', { type: 'radio', name: 'cwd', checked: inServer });
  const dir = h('input', { type: 'text', value: v.cwd || state.fsLoc?.dir || state.settings?.default_folder || '', 'aria-label': t('tasks.cwdDir') });
  const scopeAll = h('input', { type: 'radio', name: 'scope', checked: !v.scope });
  const scopeRepo = h('input', { type: 'radio', name: 'scope', checked: !!v.scope, disabled: !state.repo && !v.scope });
  const conf = h('input', { type: 'checkbox', checked: !!v.confirm });
  const cont = h('input', { type: 'checkbox', checked: !!v.continue_on_error });
  const timeout = h('input', { type: 'text', value: v.timeout ? String(Math.round(parseDuration(v.timeout) / 60)) : '', placeholder: '—', style: { width: '80px' }, 'aria-label': t('tasks.timeout') });
  const field = (label, ...children) => h('div', { class: 'form-row' }, h('div', { class: 'muted' }, label), children);
  dialog({
    title: task ? t('tasks.editTitle') : t('tasks.newTitle'), mid: true,
    body: h('div', { class: 'form' },
      field(t('tasks.name'), name),
      field(t('tasks.steps'), steps, h('div', { class: 'muted', style: { fontSize: '12px' } }, t('tasks.stepsHint'))),
      field(t('tasks.cwd'), h('label', {}, cwdWt, t('tasks.cwdWorktree')), h('label', {}, cwdDir, t('tasks.cwdDir')), dir),
      field(t('tasks.scope'), h('label', {}, scopeAll, t('tasks.scopeAll')),
        h('label', {}, scopeRepo, state.repo || v.scope ? scopeText({ scope: v.scope || state.repo.id }) : t('tasks.scopeOneNone'))),
      h('label', {}, conf, t('tasks.confirm')),
      h('label', {}, cont, t('tasks.continue')),
      h('label', {}, t('tasks.timeout'), timeout, t('tasks.minutes'))),
    buttons: [{ label: t('common.cancel'), value: null }, { label: t('tasks.save'), primary: true, run: async (close) => {
      const mins = parseFloat(timeout.value.replace(',', '.'));
      const next = {
        ...(task && task.id ? { id: task.id } : {}),
        name: name.value.trim(),
        steps: steps.value.split('\n').map((s) => s.trim()).filter(Boolean),
        cwd: cwdDir.checked ? dir.value.trim() : '',
        scope: scopeRepo.checked ? (v.scope || state.repo?.id || '') : '',
        confirm: conf.checked,
        continue_on_error: cont.checked,
        timeout: mins > 0 ? Math.round(mins * 60) + 's' : '',
      };
      if (!next.name || !next.steps.length) return toast(t('tasks.needNameSteps'), { bad: true });
      if (await onSave(next)) close();
    } }],
  });
}

function parseDuration(s) {
  let sec = 0;
  for (const [, n, u] of s.matchAll(/([\d.]+)(h|m|s)/g)) sec += parseFloat(n) * { h: 3600, m: 60, s: 1 }[u];
  return sec;
}

// --- вкладка «Задачи» нижней панели ---

export function tasksPane(el) {
  const render = () => {
    const tk = state.tasks;
    if (!tk) return mount(el, h('div', { class: 'muted' }, t('common.loading')));
    const runs = (state.sessions || []).filter((s) => s.kind === 'task').slice().reverse();
    const group = (title, list, note) => (list?.length ? [h('div', { class: 'section-title' }, title),
      list.map((task) => h('div', { class: 'task-row' },
        h('button', { class: 'btn small primary', 'aria-label': t('tasks.run') + ' ' + task.name, onclick: () => runTask(task) }, '▶'),
        h('span', { class: 'grow ellipsis' }, task.name, note ? h('span', { class: 'muted' }, '  · ' + note) : null),
        h('span', { class: 'muted mono ellipsis', style: { maxWidth: '50%' } }, task.steps.join(' ; '))))] : null);
    mount(el,
      runs.length ? [h('div', { class: 'section-title' }, t('tasks.runs')), runs.map((s) => h('button', {
        class: 'task-row link-row', onclick: () => state.dock?.openSession(s),
      }, h('span', { class: s.running ? 'muted' : s.exit_code === 0 ? 'ok' : 'bad' }, s.running ? '…' : s.exit_code === 0 ? '✓' : '✗'),
      h('span', { class: 'grow ellipsis' }, s.title), h('span', { class: 'muted' }, stamp(Date.parse(s.started) / 1000))))] : null,
      group(t('tasks.mine'), tk.mine),
      group(t('tasks.repo'), tk.repo?.tasks, tk.repo && !tk.repo.trusted ? t('tasks.needsTrust') : ''),
      tk.repo?.error ? h('div', { class: 'notice' }, t('tasks.repoError', { err: tk.repo.error })) : null,
      group(t('tasks.auto'), tk.auto),
      h('div', { class: 'field', style: { marginTop: '12px' } },
        h('button', { class: 'btn small', onclick: () => editMine() }, t('tasks.editMine')),
        h('span', { class: 'muted' }, t('tasks.rerunHint'))));
  };
  on('tasks sessions', render);
  render();
  return { show: () => loadTasks() };
}

on('wt', () => { state.tasks = null; loadTasks(); });

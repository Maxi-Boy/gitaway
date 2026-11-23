// Верхняя панель (репозиторий, fetch/pull/push, связь, команды) и вкладки веток.
import { h, mount, icon, menuAt, prompt, toast } from '../ui.js';
import { t, lang, languages, setLang } from '../i18n.js';
import { themes, themeMode, setTheme } from '../theme.js';
import { state, on, wtParam } from '../store.js';
import { events, isOnline } from '../api.js';
import { run } from '../commands.js';
import { go } from '../router.js';
import * as act from '../actions.js';
import { taskControls } from './tasks.js';

const base = (p) => p.split('/').filter(Boolean).pop() || p;

export function repoName(repo) {
  return repo ? base(repo.path) : '';
}

export function topbar(el) {
  const conn = h('span', { class: 'conn', role: 'status' });
  const setConn = () => {
    conn.classList.toggle('off', !isOnline());
    conn.textContent = isOnline() ? '' : t('conn.offline');
    conn.title = isOnline() ? t('conn.online') : t('conn.offline');
  };
  events.addEventListener('online', setConn);
  events.addEventListener('offline', setConn);
  setConn();

  const render = () => {
    const st = state.status;
    const has = !!state.wt;
    const repoBtn = h('button', { class: 'btn', 'aria-haspopup': 'menu', onclick: (e) => menuAt(e.currentTarget, repoMenu()) },
      h('span', { class: 'ellipsis', style: { maxWidth: '180px' } }, state.repo ? repoName(state.repo) : t('repo.choose')), icon('chevron'));
    const fsBtn = h('button', {
      class: 'btn wide-only' + (state.fsOpen ? ' on' : ''), 'aria-pressed': String(state.fsOpen),
      onclick: () => run('fs.toggle'),
    }, state.fsOpen ? t('fs.hide') : t('fs.show'));
    const userBtn = h('button', { class: 'btn wide-only', 'aria-haspopup': 'menu', onclick: (e) => menuAt(e.currentTarget, [
      { section: t('theme.title') },
      ...themes.map((m) => ({ label: (m === themeMode() ? '✓ ' : '') + t('theme.' + m), run: () => setTheme(m) })),
      { section: t('lang.title') },
      ...languages.map((l) => ({ label: (l === lang ? '✓ ' : '') + t('lang.' + l), run: () => setLang(l) })),
      '-',
      { label: t('auth.logout'), run: () => run('logout') },
    ]) }, state.me ? `${state.me.user}@${state.me.host || ''}` : '');
    mount(el,
      h('span', { class: 'brand' }, 'gitaway'),
      repoBtn,
      h('span', { class: 'sep wide-only' }),
      h('button', { class: 'btn', disabled: !has, onclick: () => run('git.fetch') }, t('git.fetch')),
      h('button', { class: 'btn', disabled: !has, onclick: () => run('git.pull') },
        t('git.pull'), st?.behind ? ' ↓' + st.behind : ''),
      h('button', { class: 'btn' + (st?.ahead ? ' primary' : ''), disabled: !has, onclick: () => run('git.push') },
        t('git.push'), st?.ahead ? ' ↑' + st.ahead : ''),
      h('span', { class: 'sep wide-only' }),
      h('span', { class: 'wide-only' }, taskControls()),
      h('span', { class: 'grow' }),
      conn,
      h('button', { class: 'btn icon', title: t('palette.title') + ' (Ctrl+K)', 'aria-label': t('palette.title'), onclick: () => run('palette') }, icon('search')),
      fsBtn,
      userBtn,
    );
  };
  on('repo repos status wt fs me tasks', render);
  render();
}

function repoMenu() {
  const items = [{ section: t('repo.list') }];
  for (const r of state.repos) {
    items.push({ label: (r.id === state.repo?.id ? '✓ ' : '') + repoName(r), run: () => go({ repo: r.id }) });
  }
  if (!state.repos.length) items.push({ label: t('repo.none'), disabled: true, run() {} });
  items.push('-', { label: t('repo.add'), run: () => run('repo.add') });
  if (state.repo) {
    items.push({ label: t('key.attachTitle') + '…', run: () => run('repo.attachKey') });
    items.push({ label: t('repo.remove'), danger: true, run: () => act.removeRepo(state.repo) });
  }
  return items;
}

// --- вкладки веток ---

// taskMark: последняя задача вкладки: ▶ идёт, ✓ успешно, ✗ с ошибкой.
export function taskMark(w) {
  const runs = (state.sessions || []).filter((s) => s.kind === 'task' && s.repo === state.repo?.id && (s.wt || '') === wtParam(w));
  const last = runs[runs.length - 1];
  if (!last) return null;
  if (last.running) return h('span', { class: 'badge', title: last.title }, '▶');
  return h('span', { class: 'badge ' + (last.exit_code === 0 ? 'ok' : 'bad'), title: last.title }, last.exit_code === 0 ? '✓' : '✗');
}

export function badge(st) {
  if (!st) return null;
  const parts = [];
  if (st.changes?.length) parts.push('● ' + st.changes.length);
  if (st.ahead) parts.push('↑' + st.ahead);
  if (st.behind) parts.push('↓' + st.behind);
  return parts.length ? h('span', { class: 'badge' }, parts.join(' ')) : null;
}



export async function newBranch() {
  const from = state.wt?.branch || '';
  const name = await prompt(t('branches.new'), from ? t('branches.newFrom', { from }) : '', '', 'feature/…');
  if (!name) return;
  await act.openBranchTab({ name, remote: false, create: true, start: from });
}

export function copyText(text) {
  navigator.clipboard?.writeText(text).then(() => toast(t('common.copied')), () => toast(text));
}

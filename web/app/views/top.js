// Верхняя панель (репозиторий, fetch/pull/push, связь, команды) и вкладки веток.
import { h, mount, icon, menuAt, onContext, prompt, toast } from '../ui.js';
import { t } from '../i18n.js';
import { state, on, tabName } from '../store.js';
import { events, isOnline } from '../api.js';
import { run } from '../commands.js';
import { go } from '../router.js';
import * as act from '../actions.js';

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
      h('span', { class: 'grow' }),
      conn,
      h('button', { class: 'btn icon', title: t('palette.title') + ' (Ctrl+K)', 'aria-label': t('palette.title'), onclick: () => run('palette') }, icon('search')),
      fsBtn,
      userBtn,
    );
  };
  on('repo repos status wt fs me', render);
  render();
}

function repoMenu() {
  const items = [{ section: t('repo.list') }];
  for (const r of state.repos) {
    items.push({ label: (r.id === state.repo?.id ? '✓ ' : '') + repoName(r), run: () => go({ repo: r.id }) });
  }
  if (!state.repos.length) items.push({ label: t('repo.none'), disabled: true, run() {} });
  items.push('-', { label: t('repo.add'), run: () => run('repo.add') });
  if (state.repo) items.push({ label: t('repo.remove'), danger: true, run: () => act.removeRepo(state.repo) });
  return items;
}

// --- вкладки веток ---

function badge(st) {
  if (!st) return null;
  const parts = [];
  if (st.changes?.length) parts.push('● ' + st.changes.length);
  if (st.ahead) parts.push('↑' + st.ahead);
  if (st.behind) parts.push('↓' + st.behind);
  // место под состояние задачи (идёт, ✓, ✗) появится вместе с сеансами
  return parts.length ? h('span', { class: 'badge' }, parts.join(' ')) : null;
}

export function tabs(el) {
  const render = () => {
    if (!state.repo) return mount(el);
    const list = state.worktrees.map((w) => {
      const name = tabName(w);
      const active = state.wt?.path === w.path;
      const tab = h('button', {
        class: 'tab' + (active ? ' active' : ''), role: 'tab', 'aria-selected': String(active), title: w.path,
        onclick: () => go({ repo: state.repo.id, wt: name }),
        onauxclick: (e) => { if (e.button === 1) act.closeTab(w); },
      }, h('span', {}, name), badge(state.wtStatus.get(w.path)),
      w.main ? null : h('span', {
        class: 'x', role: 'button', 'aria-label': t('tab.close'), title: t('tab.close'),
        onclick: (e) => { e.stopPropagation(); act.closeTab(w); },
      }, '×'));
      onContext(tab, () => [
        { label: t('git.fetch'), run: () => act.gitOp('fetch', {}, w) },
        { label: t('git.pullFF'), run: () => act.gitOp('pull', {}, w) },
        { label: t('git.push'), run: () => act.pushBranch(w) },
        '-',
        { label: t('path.copy'), run: () => copyText(w.path) },
        w.main ? null : { label: t('tab.close'), danger: true, run: () => act.closeTab(w) },
      ]);
      return tab;
    });
    const add = h('button', { class: 'btn small', 'aria-haspopup': 'menu', onclick: (e) => menuAt(e.currentTarget, openMenu()) }, t('tab.open'));
    mount(el, h('div', { role: 'tablist', style: { display: 'contents' } }, list), add);
  };
  on('repo worktrees wt status', render);
  render();
}

function openMenu() {
  const opened = new Set(state.worktrees.map((w) => w.branch));
  const local = state.branches.filter((b) => !b.remote && !opened.has(b.name));
  const localNames = new Set(state.branches.filter((b) => !b.remote).map((b) => b.name));
  const remote = state.branches.filter((b) => b.remote && !opened.has(b.name.slice(b.name.indexOf('/') + 1)) &&
    !localNames.has(b.name.slice(b.name.indexOf('/') + 1)));
  const items = [];
  if (local.length) items.push({ section: t('branches.local') }, ...local.map((b) => ({ label: b.name, run: () => act.openBranchTab(b) })));
  if (remote.length) items.push({ section: t('branches.remote') }, ...remote.map((b) => ({ label: b.name, run: () => act.openBranchTab(b) })));
  if (!items.length) items.push({ label: t('tab.allOpen'), disabled: true, run() {} });
  items.push('-', { label: t('branches.new'), run: () => run('branch.new') });
  return items;
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

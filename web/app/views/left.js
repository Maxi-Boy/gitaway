// Левая колонка: репозитории и ветки выбранного репозитория с контекстным меню.
import { h, mount, onContext, showMenu } from '../ui.js';
import { t } from '../i18n.js';
import { state, on, tabName } from '../store.js';
import { go } from '../router.js';
import { run } from '../commands.js';
import * as act from '../actions.js';
import { repoName, copyText } from './top.js';

export function left(el) {
  // репозиторий выбирается в списке вверху; здесь только его ветки
  const render = () => {
    const parts = [];
    if (!state.repo) {
      parts.push(h('div', { class: 'empty' }, state.repos.length ? t('branches.pickRepo') : t('repo.none')),
        h('button', { class: 'item muted', onclick: () => run('repo.add') }, '+ ' + t('repo.add')));
    } else {
      const local = state.branches.filter((b) => !b.remote);
      const remote = state.branches.filter((b) => b.remote);
      parts.push(h('div', { class: 'section-title' }, t('branches.title', { repo: repoName(state.repo) })),
        local.map(branchItem));
      if (remote.length) parts.push(h('div', { class: 'section-title' }, t('branches.remote')), remote.map(branchItem));
    }
    mount(el, parts);
  };
  on('repos repo branches worktrees wt status', render);
  render();
}

function branchItem(b) {
  const wt = state.worktrees.find((w) => w.branch === b.name);
  const active = wt && state.wt?.path === wt.path;
  const notes = [];
  if (b.ahead) notes.push('↑' + b.ahead);
  if (b.behind) notes.push('↓' + b.behind);
  if (b.gone) notes.push(t('branches.gone'));
  if (active) notes.push('● ' + t('branches.active'));
  else if (wt) notes.push(t('branches.tab'));
  const el = h('button', {
    class: 'item' + (active ? ' active' : ''), title: b.upstream ? t('branches.tracks', { up: b.upstream }) : b.name,
    onclick: () => { if (wt) go({ repo: state.repo.id, wt: tabName(wt) }); },
    ondblclick: () => act.openBranchTab(b),
  }, h('span', { class: 'ellipsis' + (b.remote ? ' muted' : '') }, b.name),
  notes.length ? h('span', { class: 'note' + (active ? ' open' : '') }, notes.join(' ')) : null);
  onContext(el, () => branchMenu(b, wt));
  el.addEventListener('keydown', (e) => {
    if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      showMenu(r.left + 24, r.bottom, branchMenu(b, wt));
    }
  });
  return el;
}

function branchMenu(b, wt) {
  if (b.remote) {
    return [
      { label: t('branches.openTab'), run: () => act.openBranchTab(b) },
      { label: t('git.fetch'), run: () => act.gitOp('fetch', { remote: b.name.split('/')[0] }) },
      state.wt?.branch ? { label: t('merge.into', { into: state.wt.branch }), run: () => act.merge(b.name) } : null,
      '-',
      { label: t('branches.copyName'), run: () => copyText(b.name) },
      { label: t('branchDel.remoteMenu'), danger: true, run: () => act.deleteRemoteBranch(b.name) },
    ];
  }
  const current = state.wt?.branch === b.name;
  return [
    { label: wt ? t('branches.goTab') : t('branches.openTab'), run: () => act.openBranchTab(b) },
    { label: t('branches.checkout'), disabled: current || !!wt, run: () => act.switchBranch(b.name) },
    '-',
    { label: t('git.fetch'), run: () => act.gitOp('fetch', {}) },
    { label: t('git.pullFF'), disabled: !b.upstream, run: () => act.gitOp('update-branch', { branch: b.name }, wt || state.wt) },
    { label: t('git.push'), run: () => act.pushNamed(b) },
    '-',
    state.wt?.branch && !current ? { label: t('merge.into', { into: state.wt.branch }), run: () => act.merge(b.name) } : null,
    { label: t('branches.copyName'), run: () => copyText(b.name) },
    { label: wt ? t('branchDel.hasTab') : t('branchDel.menu'), danger: true, disabled: !!wt, run: () => act.deleteBranch(b.name) },
  ];
}

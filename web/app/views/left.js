// Левая колонка: ветки выбранного репозитория. Сверху открытые ветки (у каждой своя рабочая
// папка, git worktree) со счётчиками и крестиком закрытия, ниже остальные локальные
// и удалённые: щелчок открывает ветку. Репозиторий выбирается в списке вверху.
import { h, mount, onContext, showMenu } from '../ui.js';
import { t } from '../i18n.js';
import { state, on, tabName } from '../store.js';
import { go } from '../router.js';
import { run } from '../commands.js';
import * as act from '../actions.js';
import { repoName, copyText, badge, taskMark } from './top.js';

export function left(el) {
  const render = () => {
    const parts = [];
    if (!state.repo) {
      parts.push(h('div', { class: 'empty' }, state.repos.length ? t('branches.pickRepo') : t('repo.none')),
        h('button', { class: 'item muted', onclick: () => run('repo.add') }, '+ ' + t('repo.add')));
      return mount(el, parts);
    }
    const opened = new Set(state.worktrees.map((w) => w.branch).filter(Boolean));
    const local = state.branches.filter((b) => !b.remote && !opened.has(b.name));
    const localNames = new Set(state.branches.filter((b) => !b.remote).map((b) => b.name));
    // удалённая ветка, у которой уже есть локальная, открывается через локальную
    const remote = state.branches.filter((b) => b.remote && !localNames.has(shortName(b.name)));
    parts.push(h('div', { class: 'section-title' }, t('branches.openTitle', { repo: repoName(state.repo) })),
      state.worktrees.map(openItem));
    parts.push(h('div', { class: 'section-title' }, t('branches.local')), local.map(branchItem),
      h('button', { class: 'item muted', onclick: () => run('branch.new') }, t('branches.new')));
    if (remote.length) parts.push(h('div', { class: 'section-title' }, t('branches.remote')), remote.map(branchItem));
    mount(el, parts);
  };
  on('repos repo branches worktrees wt status sessions', render);
  render();
}

const shortName = (name) => name.slice(name.indexOf('/') + 1);

function withKeyMenu(el, menu) {
  onContext(el, menu);
  el.addEventListener('keydown', (e) => {
    if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      showMenu(r.left + 24, r.bottom, menu());
    }
  });
  return el;
}

// openItem это открытая ветка: переход по щелчку, изменения, ↑↓, итог задачи, закрытие.
function openItem(w) {
  const name = tabName(w);
  const active = state.wt?.path === w.path;
  const b = state.branches.find((x) => !x.remote && x.name === w.branch);
  const el = h('button', {
    class: 'item open' + (active ? ' active' : ''), title: w.path, 'aria-current': active ? 'page' : null,
    onclick: () => go({ repo: state.repo.id, wt: name }),
    onauxclick: (e) => { if (e.button === 1) act.closeTab(w); },
  }, h('span', { class: 'ellipsis grow' }, name),
  badge(state.wtStatus.get(w.path)), taskMark(w),
  w.main ? null : h('span', {
    class: 'x', role: 'button', tabindex: '-1', 'aria-label': t('tab.close'), title: t('tab.close'),
    onclick: (e) => { e.stopPropagation(); act.closeTab(w); },
  }, '×'));
  return withKeyMenu(el, () => (b ? branchMenu(b, w) : [
    { label: t('path.copy'), run: () => copyText(w.path) },
    w.main ? null : { label: t('tab.close'), danger: true, run: () => act.closeTab(w) },
  ]));
}

// branchItem это ветка без рабочей папки: щелчок открывает её (создаёт worktree).
function branchItem(b) {
  const notes = [];
  if (b.ahead) notes.push('↑' + b.ahead);
  if (b.behind) notes.push('↓' + b.behind);
  if (b.gone) notes.push(t('branches.gone'));
  const el = h('button', {
    class: 'item', title: (b.upstream ? t('branches.tracks', { up: b.upstream }) + '. ' : '') + t('branches.clickToOpen'),
    onclick: () => act.openBranchTab(b),
  }, h('span', { class: 'ellipsis' + (b.remote ? ' muted' : '') }, b.name),
  notes.length ? h('span', { class: 'note' }, notes.join(' ')) : null);
  return withKeyMenu(el, () => branchMenu(b, null));
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
    wt ? null : { label: t('branches.openTab'), run: () => act.openBranchTab(b) },
    { label: t('branches.checkout'), disabled: current || !!wt, run: () => act.switchBranch(b.name) },
    '-',
    { label: t('git.fetch'), run: () => act.gitOp('fetch', {}, wt || undefined) },
    { label: t('git.pullFF'), disabled: !b.upstream, run: () => act.gitOp('update-branch', { branch: b.name }, wt || state.wt) },
    { label: t('git.push'), run: () => (wt ? act.pushBranch(wt) : act.pushNamed(b)) },
    '-',
    state.wt?.branch && !current ? { label: t('merge.into', { into: state.wt.branch }), run: () => act.merge(b.name) } : null,
    { label: t('branches.copyName'), run: () => copyText(b.name) },
    wt ? { label: t('path.copy'), run: () => copyText(wt.path) } : null,
    wt && !wt.main ? { label: t('tab.close'), run: () => act.closeTab(wt) } : null,
    { label: wt ? t('branchDel.hasTab') : t('branchDel.menu'), danger: true, disabled: !!wt, run: () => act.deleteBranch(b.name) },
  ];
}

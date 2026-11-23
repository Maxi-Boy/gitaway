// Изменения вкладки: поле коммита, подготовленные и неподготовленные файлы, diff.
import { h, mount, onContext } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam } from '../store.js';
import * as act from '../actions.js';
import { copyText } from './top.js';

const messages = new Map(); // черновик сообщения коммита по вкладке

export function changesView(route) {
  const el = h('div', { class: 'changes' });
  const list = h('div', { class: 'list' });
  const pane = h('div', { class: 'diffpane', 'aria-live': 'polite' });
  const wtKey = state.wt?.path;
  const msg = h('textarea', { placeholder: t('changes.messagePlaceholder'), 'aria-label': t('changes.message') });
  msg.value = messages.get(wtKey) || '';
  const commitBtn = h('button', { class: 'btn primary', onclick: () => doCommit() });
  let selected = route.path ? { path: route.path, staged: false } : null;
  let busy = false;

  const staged = () => (state.status?.changes || []).filter((c) => c.x !== ' ' && c.x !== '?');
  const unstaged = () => (state.status?.changes || []).filter((c) => c.y !== ' ' || c.x === '?');

  const setBtn = () => {
    const n = staged().length;
    commitBtn.textContent = n ? t('changes.commitN', { n }) : t('changes.commit');
    commitBtn.disabled = busy || !n || !msg.value.trim();
  };
  msg.addEventListener('input', () => { messages.set(wtKey, msg.value); setBtn(); });
  msg.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); doCommit(); }
  });

  const doCommit = async () => {
    if (commitBtn.disabled) return;
    busy = true;
    setBtn();
    try {
      await act.commit(msg.value);
      msg.value = '';
      messages.delete(wtKey);
    } catch { /* ошибка уже показана */ }
    busy = false;
    setBtn();
  };

  const showDiff = async (path, isStaged) => {
    selected = { path, staged: isStaged };
    renderList();
    mount(pane, h('div', { class: 'empty' }, t('common.loading')));
    try {
      const d = await api.get('/api/diff', { repo: state.repo.id, wt: wtParam(), path, staged: isStaged });
      mount(pane, renderDiff(d));
    } catch (err) {
      mount(pane, h('div', { class: 'notice' }, err.message));
    }
  };

  const item = (c, isStaged) => {
    const flag = isStaged ? c.x : c.y; // у неотслеживаемого файла x и y равны "?"
    const sel = selected && selected.path === c.path && selected.staged === isStaged;
    const slash = c.path.lastIndexOf('/');
    const acts = isStaged
      ? [h('button', { class: 'btn small', title: t('changes.unstage'), 'aria-label': t('changes.unstage') + ' ' + c.path, onclick: (e) => { e.stopPropagation(); act.unstage([c.path]); } }, '−')]
      : [h('button', { class: 'btn small', title: t('changes.stage'), 'aria-label': t('changes.stage') + ' ' + c.path, onclick: (e) => { e.stopPropagation(); act.stage([c.path]); } }, '+'),
        h('button', { class: 'btn small danger', title: t('changes.discard'), 'aria-label': t('changes.discard') + ' ' + c.path, onclick: (e) => { e.stopPropagation(); act.discard([c.path]); } }, '↺')];
    const row = h('div', {
      class: 'chg' + (sel ? ' sel' : ''), tabindex: '0', role: 'button',
      onclick: () => showDiff(c.path, isStaged),
      ondblclick: () => { if (flag !== 'D') act.nav({ view: 'file', path: c.path }); },
      onkeydown: (e) => { if (e.key === 'Enter') showDiff(c.path, isStaged); },
    }, h('span', { class: 'flag st-' + (flag === '?' ? 'new' : flag) }, flag),
    h('span', { class: 'grow ellipsis', title: c.path },
      h('span', {}, c.path.slice(slash + 1)), slash > 0 ? h('span', { class: 'muted' }, '  ' + c.path.slice(0, slash)) : null),
    h('span', { class: 'acts' }, acts));
    onContext(row, () => [
      { label: t('changes.showDiff'), run: () => showDiff(c.path, isStaged) },
      flag !== 'D' ? { label: t('files.open'), run: () => act.nav({ view: 'file', path: c.path }) } : null,
      '-',
      isStaged ? { label: t('changes.unstage'), run: () => act.unstage([c.path]) } : { label: t('changes.stage'), run: () => act.stage([c.path]) },
      isStaged ? null : { label: t('changes.discard') + '…', danger: true, run: () => act.discard([c.path]) },
      '-',
      { label: t('path.copy'), run: () => copyText(c.path) },
    ]);
    return row;
  };

  const renderList = () => {
    const s = staged();
    const u = unstaged();
    mount(list,
      state.status?.merging ? h('div', { class: 'notice field' }, h('span', { class: 'grow' }, t('merge.inProgress')),
        h('button', { class: 'btn small danger', onclick: () => act.abortMerge() }, t('merge.abort'))) : null,
      h('div', { class: 'commit-box' }, msg, h('div', { class: 'field' }, commitBtn, h('span', { class: 'muted grow' }, t('changes.commitHint')))),
      h('div', { class: 'section-title field' }, h('span', { class: 'grow' }, t('changes.staged', { n: s.length })),
        s.length ? h('button', { class: 'btn small', onclick: () => act.unstage(['.']) }, t('changes.unstageAll')) : null),
      s.length ? s.map((c) => item(c, true)) : h('div', { class: 'empty' }, t('changes.noneStaged')),
      h('div', { class: 'section-title field' }, h('span', { class: 'grow' }, t('changes.unstaged', { n: u.length })),
        u.length ? h('button', { class: 'btn small', onclick: () => act.stage(['.']) }, t('changes.stageAll')) : null,
        u.length ? h('button', { class: 'btn small danger', onclick: () => act.discard(u.map((c) => c.path)) }, t('changes.discardAll')) : null),
      u.length ? u.map((c) => item(c, false)) : h('div', { class: 'empty' }, t('changes.noneUnstaged')));
    setBtn();
  };

  mount(el, list, pane);
  renderList();
  if (selected) {
    const inStaged = staged().some((c) => c.path === selected.path) && !unstaged().some((c) => c.path === selected.path);
    showDiff(selected.path, inStaged);
  } else mount(pane, h('div', { class: 'empty' }, t('changes.pickFile')));

  return {
    el,
    update: (ev) => {
      if (ev !== 'status') return;
      renderList();
      if (selected) {
        const inSame = (selected.staged ? staged() : unstaged()).some((c) => c.path === selected.path);
        const inOther = (selected.staged ? unstaged() : staged()).some((c) => c.path === selected.path);
        if (inSame || inOther) showDiff(selected.path, inSame ? selected.staged : !selected.staged);
        else { selected = null; mount(pane, h('div', { class: 'empty' }, t('changes.pickFile'))); renderList(); }
      }
    },
  };
}

// renderDiff раскрашивает unified diff: добавленные, удалённые строки, заголовки фрагментов.
export function renderDiff(d) {
  if (!d.text) return h('div', { class: 'empty' }, d.untracked && d.staged ? t('changes.notInIndex') : t('changes.noDiff'));
  const lines = d.text.replace(/\n$/, '').split('\n');
  const out = lines.map((l) => {
    let cls = null;
    if (/^(diff --git|index |--- |\+\+\+ |new file|deleted file|similarity|rename |old mode|new mode)/.test(l)) cls = 'm';
    else if (l.startsWith('@@')) cls = 'h';
    else if (l.startsWith('+')) cls = 'a';
    else if (l.startsWith('-')) cls = 'd';
    return h('div', { class: cls }, l || ' ');
  });
  return h('div', {}, h('pre', { class: 'diff' }, out), d.truncated ? h('div', { class: 'notice' }, t('changes.diffTruncated')) : null);
}

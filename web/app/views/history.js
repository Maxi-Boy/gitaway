// История ветки вкладки (или одного файла): слева коммиты, справа выбранный коммит
// с описанием и изменёнными файлами; diff файла раскрывается под ним. Коммит в адресе:
// #/repo/<id>/wt/<ветка>/history/<хеш>/path/<файл>.
import { h, mount, onContext, ago, fullStamp } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam } from '../store.js';
import * as act from '../actions.js';
import { copyText } from './top.js';
import { renderDiff } from './changes.js';

const PAGE = 100;

export function historyView(route) {
  const path = route.path;
  const el = h('div', { class: 'changes history' });
  const list = h('div', { class: 'list', role: 'listbox', 'aria-label': t('history.title') });
  const pane = h('div', { class: 'diffpane commit-pane', 'aria-live': 'polite' });
  let commits = [];
  let more = false;
  let loading = false;
  let selected = route.commit || '';
  let detailSeq = 0;

  const params = (extra) => ({ repo: state.repo.id, wt: wtParam(), path, ...extra });

  const load = async (append) => {
    if (loading) return;
    loading = true;
    try {
      const res = await api.get('/api/log', params({ skip: append ? commits.length : 0, limit: append ? PAGE : Math.max(PAGE, commits.length) }));
      commits = append ? commits.concat(res.commits) : res.commits;
      more = res.more;
    } catch (err) {
      mount(list, h('div', { class: 'notice' }, err.message));
      loading = false;
      return;
    }
    loading = false;
    renderList();
  };

  const select = (hash, replace = false) => {
    act.nav({ view: 'history', path, commit: hash }, replace);
  };

  const row = (c) => {
    const sel = selected && c.hash.startsWith(selected);
    const r = h('div', {
      class: 'chg commit-row' + (sel ? ' sel' : ''), role: 'option', tabindex: '0', 'aria-selected': String(!!sel),
      onclick: () => select(c.hash),
      onkeydown: (e) => {
        if (e.key === 'Enter') select(c.hash);
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          const i = commits.indexOf(c) + (e.key === 'ArrowDown' ? 1 : -1);
          if (commits[i]) select(commits[i].hash, true);
        }
      },
    },
    h('div', { class: 'grow', style: { minWidth: '0' } },
      h('div', { class: 'ellipsis' },
        c.unpushed ? h('span', { class: 'badge unpushed', title: t('history.unpushed') }, '↑') : null,
        (c.refs || []).map((ref) => h('span', { class: 'badge ref' + (ref.startsWith('tag: ') ? ' tag' : '') }, ref.replace(/^tag: /, ''))),
        h('span', { title: c.subject }, c.subject || t('history.noSubject'))),
      h('div', { class: 'muted small ellipsis' }, c.author, ' · ', h('span', { title: fullStamp(c.time) }, ago(c.time)),
        c.parents.length > 1 ? ' · ' + t('history.merge') : '')),
    h('code', { class: 'muted small' }, c.hash.slice(0, 7)));
    onContext(r, () => [
      { label: t('history.copyHash'), run: () => copyText(c.hash) },
      { label: t('history.copySubject'), run: () => copyText(c.subject) },
    ]);
    return r;
  };

  const renderList = () => {
    mount(list,
      path ? h('div', { class: 'notice field' }, h('span', { class: 'grow ellipsis', title: path }, t('history.ofFile', { path })),
        h('button', { class: 'btn small', onclick: () => act.nav({ view: 'history', path: '', commit: '' }) }, t('history.all'))) : null,
      commits.length ? commits.map(row) : h('div', { class: 'empty' }, t('history.empty')),
      more ? h('div', { class: 'field', style: { padding: '8px 16px' } },
        h('button', { class: 'btn small', onclick: () => load(true) }, t('history.more'))) : null);
    if (!selected && commits.length && innerWidth >= 900) select(commits[0].hash, true);
    list.querySelector('.sel')?.scrollIntoView({ block: 'nearest' });
  };

  const fileRow = (d, f) => {
    const box = h('div', { class: 'cfile' });
    let open = false;
    const name = f.old_path ? f.old_path + ' → ' + f.path : f.path;
    const counts = f.added < 0 ? t('history.binary') : [h('span', { class: 'plus' }, '+' + f.added), ' ', h('span', { class: 'minus' }, '−' + f.deleted)];
    const toggle = async () => {
      open = !open;
      head.setAttribute('aria-expanded', String(open));
      head.firstChild.textContent = open ? '▾' : '▸';
      if (!open) return diff.replaceChildren();
      mount(diff, h('div', { class: 'empty' }, t('common.loading')));
      try {
        const res = await api.get('/api/commit/diff', params({ hash: d.hash, path: f.path, old: f.old_path || '' }));
        mount(diff, renderDiff(res));
      } catch (err) {
        mount(diff, h('div', { class: 'notice' }, err.message));
      }
    };
    const head = h('button', { class: 'chg cfile-head', 'aria-expanded': 'false', title: name, onclick: toggle },
      h('span', { class: 'ico', 'aria-hidden': 'true' }, '▸'),
      h('span', { class: 'flag st-' + f.status }, f.status),
      h('span', { class: 'grow ellipsis' }, name),
      h('span', { class: 'muted small' }, counts));
    const diff = h('div', { class: 'cfile-diff' });
    onContext(head, () => [
      f.status !== 'D' ? { label: t('files.open'), run: () => act.nav({ view: 'file', path: f.path }) } : null,
      { label: t('history.fileHistory'), run: () => act.nav({ view: 'history', path: f.path, commit: '' }) },
      '-',
      { label: t('path.copy'), run: () => copyText(f.path) },
    ]);
    mount(box, head, diff);
    return { box, toggle };
  };

  const showCommit = async (hash) => {
    const my = ++detailSeq;
    if (!hash) return mount(pane, h('div', { class: 'empty' }, t('history.pick')));
    mount(pane, h('div', { class: 'empty' }, t('common.loading')));
    let d;
    try {
      d = await api.get('/api/commit', params({ hash }));
    } catch (err) {
      if (my === detailSeq) mount(pane, h('div', { class: 'notice' }, err.message));
      return;
    }
    if (my !== detailSeq) return;
    const rows = d.files.map((f) => fileRow(d, f));
    const parentLinks = d.parents.map((p) => h('button', { class: 'linkish mono', onclick: () => select(p) }, p.slice(0, 7)));
    mount(pane, h('div', { class: 'commit-detail' },
      h('div', { class: 'field' }, h('button', { class: 'btn small narrow-only', onclick: () => select('') }, '← ' + t('history.title'))),
      h('h3', {}, d.subject || t('history.noSubject')),
      d.body ? h('pre', { class: 'commit-body' }, d.body) : null,
      h('dl', { class: 'commit-meta' },
        h('dt', {}, t('history.author')), h('dd', {}, d.author, d.email ? h('span', { class: 'muted' }, ' <' + d.email + '>') : null),
        h('dt', {}, t('history.date')), h('dd', { title: ago(d.time) }, fullStamp(d.time)),
        d.committer && d.committer !== d.author ? [h('dt', {}, t('history.committer')), h('dd', {}, d.committer, ' · ', fullStamp(d.commit_time))] : null,
        h('dt', {}, t('history.hash')), h('dd', {}, h('code', {}, d.hash), ' ',
          h('button', { class: 'btn small', onclick: () => copyText(d.hash) }, t('history.copy'))),
        parentLinks.length ? [h('dt', {}, t('history.parents')), h('dd', {}, parentLinks)] : null),
      h('div', { class: 'section-title' }, t('history.files', { n: d.files.length }),
        d.parents.length > 1 ? ' · ' + t('history.vsFirstParent') : ''),
      rows.map((r) => r.box),
      d.files_truncated ? h('div', { class: 'notice' }, t('history.filesTruncated')) : null));
    // один файл или файл из фильтра истории: сразу показать его diff
    const auto = rows.length === 1 ? rows[0] : path ? rows[d.files.findIndex((f) => f.path === path)] : null;
    auto?.toggle();
  };

  mount(el, list, pane);
  el.classList.toggle('picked', !!selected);
  mount(list, h('div', { class: 'empty' }, t('common.loading')));
  load(false);
  showCommit(selected);

  return {
    el,
    route: (r) => {
      if (r.commit === selected) return;
      const hadFocus = list.contains(document.activeElement);
      selected = r.commit;
      el.classList.toggle('picked', !!selected);
      renderList();
      if (hadFocus) list.querySelector('.sel')?.focus();
      showCommit(selected);
    },
    update: (ev) => { if (ev === 'changed') load(false); },
  };
}

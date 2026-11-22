// Таблица файлов ветки: имя, последний коммит, автор, дата коммита, дата на диске, статус.
// Коммиты приходят вторым запросом, чтобы таблица появлялась сразу.
import { h, mount, onContext, ago, stamp, fullStamp } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam } from '../store.js';
import * as act from '../actions.js';
import { copyText } from './top.js';
import { copyToServer } from './copy.js';

export const DRAG_TYPE = 'application/x-gitaway';

const STATUS = { M: 'st.modified', A: 'st.added', D: 'st.deleted', R: 'st.renamed', U: 'st.conflict', '?': 'st.new', '*': 'st.inside' };

export function statusLabel(flag) {
  if (!flag) return '';
  const c = flag === '??' ? '?' : flag.trim()[0];
  return STATUS[c] ? t(STATUS[c]) : flag;
}

export function statusClass(flag) {
  if (!flag) return '';
  if (flag === '??') return 'st-new';
  return 'st-' + flag.trim()[0];
}

function loadSort() {
  try {
    return JSON.parse(localStorage.getItem('gitaway:sort')) || { by: 'name', desc: false };
  } catch {
    return { by: 'name', desc: false };
  }
}

function saveSort(s) {
  try {
    localStorage.setItem('gitaway:sort', JSON.stringify(s));
  } catch { /* хранилище недоступно: сортировка просто не запомнится */ }
}

const join = (dir, name) => (dir ? dir + '/' + name : name);

export function treeView(route) {
  const el = h('div', { class: 'table', role: 'grid', 'aria-label': t('files.title') });
  const dir = route.path;
  let entries = [];
  let commits = {};
  let truncated = false;
  let error = null;
  let sort = loadSort();
  let selected = new Set();
  let loaded = false;

  const sorted = () => {
    const key = {
      name: (e) => e.name.toLowerCase(),
      commit: (e) => commits[e.name]?.time || 0,
      mtime: (e) => e.mtime || 0,
    }[sort.by];
    return [...entries].sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
      const x = key(a);
      const y = key(b);
      const c = x < y ? -1 : x > y ? 1 : 0;
      return sort.desc ? -c : c;
    });
  };

  const head = () => {
    const col = (by, label) => h('button', {
      onclick: () => { sort = { by, desc: sort.by === by ? !sort.desc : by !== 'name' }; saveSort(sort); render(); },
      'aria-sort': sort.by === by ? (sort.desc ? 'descending' : 'ascending') : null,
    }, label, sort.by === by ? (sort.desc ? ' ↓' : ' ↑') : '');
    return h('div', { class: 'row head cols-git', role: 'row' },
      h('span', {}, col('name', t('files.name'))), h('span', {}, t('files.lastCommit')), h('span', {}, t('files.author')),
      h('span', {}, col('commit', t('files.commitDate'))), h('span', {}, col('mtime', t('files.modified'))), h('span', {}, t('files.status')));
  };

  const open = (e) => {
    if (e.is_dir) act.nav({ view: 'tree', path: join(dir, e.name) });
    else act.nav({ view: 'file', path: join(dir, e.name) });
  };

  const items = (e) => (selected.has(e.name) && selected.size > 1 ? [...selected] : [e.name]);

  const row = (e) => {
    const c = commits[e.name];
    const sel = selected.has(e.name);
    const r = h('div', {
      class: 'row cols-git' + (sel ? ' sel' : ''), role: 'row', tabindex: '0', draggable: 'true',
      'aria-selected': String(sel),
      onclick: (ev) => {
        if (ev.ctrlKey || ev.metaKey) {
          if (sel) selected.delete(e.name); else selected.add(e.name);
        } else selected = new Set([e.name]);
        render();
        el.querySelector(`[data-name="${CSS.escape(e.name)}"]`)?.focus();
      },
      ondblclick: () => open(e),
      onkeydown: (ev) => { if (ev.key === 'Enter') open(e); },
      ondragstart: (ev) => {
        const names = items(e);
        ev.dataTransfer.effectAllowed = 'copyMove';
        ev.dataTransfer.setData(DRAG_TYPE, JSON.stringify({ repo: state.repo.id, wt: wtParam(), path: dir, items: names }));
        ev.dataTransfer.setData('text/plain', names.map((n) => join(dir, n)).join('\n'));
      },
      dataset: { name: e.name },
    },
    h('span', { class: 'name' }, h('span', { class: 'ico', 'aria-hidden': 'true' }, e.is_dir ? '▸' : ''),
      h('span', { class: 'ellipsis' }, e.name + (e.is_dir ? '/' : '') + (e.symlink ? ' →' : ''))),
    h('span', { class: 'muted', title: c?.subject || '' }, c ? c.subject : (loaded && !e.is_dir && e.status === '??' ? t('files.untracked') : '')),
    h('span', { class: 'muted' }, c?.author || ''),
    h('span', { class: 'muted', title: c ? fullStamp(c.time) : '' }, c ? ago(c.time) : ''),
    h('span', { class: 'muted', title: fullStamp(e.mtime) }, stamp(e.mtime)),
    h('span', { class: statusClass(e.status) }, statusLabel(e.status) || '—'));
    onContext(r, () => {
      if (!selected.has(e.name)) { selected = new Set([e.name]); render(); }
      return rowMenu(e);
    });
    return r;
  };

  const rowMenu = (e) => {
    const names = items(e);
    const paths = names.map((n) => join(dir, n));
    const changed = !!e.status;
    return [
      { label: e.is_dir ? t('files.openFolder') : t('files.open'), run: () => open(e) },
      !e.is_dir && changed ? { label: t('files.showDiff'), run: () => act.nav({ view: 'changes', path: join(dir, e.name) }) } : null,
      '-',
      { label: t('files.toServer'), run: () => copyToServer({ path: dir, items: names }) },
      changed ? { label: t('changes.stage'), run: () => act.stage(paths) } : null,
      changed ? { label: t('changes.discard') + '…', danger: true, run: () => act.discard(paths) } : null,
      '-',
      { label: t('path.copy'), run: () => copyText(paths.join('\n')) },
    ];
  };

  const render = () => {
    if (error) return mount(el, h('div', { class: 'notice' }, error));
    const rows = sorted().map(row);
    const up = dir ? h('div', {
      class: 'row cols-git', role: 'row', tabindex: '0',
      ondblclick: () => act.nav({ view: 'tree', path: dir.split('/').slice(0, -1).join('/') }),
      onkeydown: (ev) => { if (ev.key === 'Enter') act.nav({ view: 'tree', path: dir.split('/').slice(0, -1).join('/') }); },
    }, h('span', { class: 'name' }, h('span', { class: 'ico' }, '↰'), '..')) : null;
    mount(el, head(), up, rows,
      !loaded ? h('div', { class: 'empty' }, t('common.loading')) : null,
      loaded && !entries.length ? h('div', { class: 'empty' }, t('files.emptyDir')) : null,
      truncated ? h('div', { class: 'notice' }, t('files.truncated')) : null);
  };

  const load = async () => {
    const q = { repo: state.repo.id, wt: wtParam(), path: dir };
    try {
      const res = await api.get('/api/tree', q);
      entries = res.entries;
      truncated = res.truncated;
      error = null;
    } catch (err) {
      error = err.status === 404 ? t('files.notFound') : err.message;
    }
    loaded = true;
    const names = new Set(entries.map((e) => e.name));
    selected = new Set([...selected].filter((n) => names.has(n)));
    render();
    try {
      commits = await api.get('/api/commits', q) || {};
      render();
    } catch { /* без коммитов таблица всё равно полезна */ }
  };

  render();
  load();
  return {
    el,
    update: (ev) => { if (ev === 'changed') load(); },
    selection: () => [...selected].map((n) => join(dir, n)),
  };
}

export function registerTreeCommands(register) {
  register({ id: 'files.up', title: t('files.up'), keys: 'Alt+ArrowUp', when: () => state.route?.view === 'tree' && !!state.route.path,
    run: () => act.nav({ view: 'tree', path: state.route.path.split('/').slice(0, -1).join('/') }) });
  register({ id: 'files.refresh', title: t('files.refresh'), when: () => !!state.repo, run: () => act.refreshAll() });
}

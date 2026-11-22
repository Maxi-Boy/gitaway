// Центральная колонка: переключатель «Файлы / Изменения», хлебные крошки и текущий экран.
import { h, mount, icon } from '../ui.js';
import { t } from '../i18n.js';
import { state, on } from '../store.js';
import { run } from '../commands.js';
import * as act from '../actions.js';
import { repoName } from './top.js';
import { treeView } from './tree.js';
import { editorView } from './editor.js';
import { changesView } from './changes.js';

export function center(el) {
  const bar = h('div', { class: 'bar' });
  const content = h('div', { class: 'content' });
  el.prepend(bar, content);
  let view = null;
  let key = '';

  const crumbs = (path, isFile) => {
    const parts = path ? path.split('/') : [];
    const btn = (label, p) => h('button', { onclick: () => act.nav({ view: 'tree', path: p }) }, label);
    const list = [btn(repoName(state.repo), '')];
    parts.forEach((name, i) => {
      list.push(h('span', { class: 'muted' }, '/'));
      const p = parts.slice(0, i + 1).join('/');
      list.push(isFile && i === parts.length - 1 ? h('button', { 'aria-current': 'page' }, name) : btn(name, p));
    });
    return h('nav', { class: 'crumbs', 'aria-label': t('files.path') }, list);
  };

  const renderBar = () => {
    const r = state.route;
    const n = state.status?.changes?.length || 0;
    const seg = h('div', { class: 'seg', role: 'tablist' },
      h('button', { class: r.view !== 'changes' ? 'on' : null, role: 'tab', onclick: () => act.nav({ view: 'tree', path: r.view === 'tree' ? r.path : '' }) }, t('files.title')),
      h('button', { class: r.view === 'changes' ? 'on' : null, role: 'tab', onclick: () => act.nav({ view: 'changes', path: '' }) },
        t('changes.title'), n ? ' ' + n : ''));
    mount(bar, seg,
      r.view === 'changes' ? h('span', { class: 'grow' }) : h('div', { class: 'grow', style: { display: 'flex', minWidth: '0' } }, crumbs(r.path, r.view === 'file')),
      view?.actions || null,
      h('button', { class: 'btn icon', title: t('files.refresh'), 'aria-label': t('files.refresh'), onclick: () => run('files.refresh') }, icon('refresh')));
  };

  const render = () => {
    const r = state.route;
    if (!state.repo || !state.wt) {
      view?.destroy?.();
      view = null;
      key = '';
      mount(bar);
      mount(content, state.repos.length
        ? h('div', { class: 'empty' }, t('common.loading'))
        : h('div', { class: 'empty' }, h('p', {}, t('repo.welcome')), h('button', { class: 'btn primary', onclick: () => run('repo.add') }, t('repo.add'))));
      return;
    }
    const k = [state.repo.id, state.wt.path, r.view, r.path].join('\n');
    if (k === key) {
      if (r.view === 'file' && r.line) view?.goLine?.(r.line);
      renderBar();
      return;
    }
    key = k;
    view?.destroy?.();
    view = r.view === 'file' ? editorView(r) : r.view === 'changes' ? changesView(r) : treeView(r);
    content.style.overflow = r.view === 'tree' ? 'auto' : 'hidden';
    mount(content, view.el);
    renderBar();
  };

  on('route', render);
  on('status', () => {
    if (!view) return;
    renderBar();
    view.update?.('status');
  });
  on('changed', () => view?.update?.('changed'));
  render();
  return { current: () => view };
}

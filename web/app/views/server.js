// Серверная панель: обычные папки на сервере. Скрыта по умолчанию, открывается кнопкой
// «Открыть локальную ФС». Сюда перетаскиваются файлы из ветки; шестерёнка открывает настройки.
import { h, mount, icon, onContext, prompt, toast, stamp, fullStamp, size } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, on, emit } from '../store.js';
import * as act from '../actions.js';
import { DRAG_TYPE } from './tree.js';
import { runCopy } from './copy.js';
import { copyText } from './top.js';

const LAST_KEY = 'gitaway:fs-last';

function remember(dir) {
  try {
    localStorage.setItem(LAST_KEY, dir);
  } catch { /* не запомнится */ }
}

function lastDir() {
  try {
    return localStorage.getItem(LAST_KEY) || '';
  } catch {
    return '';
  }
}

const parent = (p) => p.replace(/\/[^/]+\/?$/, '') || '/';
const joinAbs = (dir, name) => (dir.endsWith('/') ? dir : dir + '/') + name;

export function serverPanel(el) {
  const bar = h('div', { class: 'bar' });
  const body = h('div', { class: 'content' });
  const drop = h('div', { class: 'dropzone' }, t('fs.dropHere'));
  let entries = [];
  let error = null;
  let popover = null;
  let started = false;
  mount(el, bar, body);

  const load = async (dir) => {
    try {
      const res = await api.get('/api/fs', { dir });
      state.fsLoc = { dir: res.dir, path: '' };
      entries = res.entries;
      error = res.truncated ? t('files.truncated') : null;
      remember(res.dir);
    } catch (err) {
      if (!state.fsLoc) state.fsLoc = { dir: '', path: '' };
      error = err.status === 403 ? t('fs.outside') : err.message;
      if (dir) toast(error, { bad: true });
      else entries = [];
    }
    render();
    emit('fs-loc');
  };

  const go = (dir) => load(dir);

  const crumbs = () => {
    const dir = state.fsLoc?.dir || '';
    const parts = dir.split('/').filter(Boolean);
    const roots = state.me?.roots || [];
    const list = [];
    parts.forEach((name, i) => {
      const p = '/' + parts.slice(0, i + 1).join('/');
      const inside = roots.some((r) => p === r || p.startsWith(r + '/'));
      if (!inside && i < parts.length - 1) return; // выше корней не показываем, полный путь в подсказке
      if (list.length) list.push(h('span', { class: 'muted' }, '/'));
      list.push(h('button', { onclick: () => go(p), title: p }, name));
    });
    return h('nav', { class: 'crumbs grow', 'aria-label': t('fs.title') }, list);
  };

  const settingsPopover = () => {
    const s = state.settings || {};
    const check = (key, label) => h('label', {}, h('input', {
      type: 'checkbox', checked: !!s[key],
      onchange: (e) => act.saveSettings({ [key]: e.target.checked }).catch((err) => toast(err.message, { bad: true })),
    }), label);
    return h('div', { class: 'popover', role: 'dialog', 'aria-label': t('fs.settings') },
      h('h3', {}, t('fs.settings')),
      h('div', { class: 'muted', style: { fontSize: '12px', marginBottom: '4px' } }, t('fs.defaultFolder')),
      h('div', { class: 'field' }, h('span', { class: 'val ellipsis', title: s.default_folder || '' }, s.default_folder || t('fs.firstRoot'))),
      h('div', { class: 'field', style: { marginTop: '8px' } },
        h('button', { class: 'btn small', disabled: !state.fsLoc?.dir || state.fsLoc.dir === s.default_folder,
          onclick: () => act.saveSettings({ default_folder: state.fsLoc.dir }).then(() => toast(t('fs.defaultSet')), (err) => toast(err.message, { bad: true })) },
        t('fs.useCurrent')),
        s.default_folder ? h('button', { class: 'btn small', onclick: () => act.saveSettings({ default_folder: '' }) }, t('fs.resetDefault')) : null),
      check('open_on_start', t('fs.openOnStart')),
      check('ask_on_overwrite', t('fs.askOnOverwrite')),
      check('move_by_default', t('fs.moveByDefault')));
  };

  const toggleSettings = () => {
    popover = popover ? null : true;
    render();
  };

  const row = (e) => {
    const full = joinAbs(state.fsLoc.dir, e.name);
    const r = h('div', {
      class: 'row cols-fs', role: 'row', tabindex: '0',
      ondblclick: () => { if (e.is_dir) go(full); },
      onkeydown: (ev) => { if (ev.key === 'Enter' && e.is_dir) go(full); },
    }, h('span', { class: 'name' }, h('span', { class: 'ico', 'aria-hidden': 'true' }, e.is_dir ? '▸' : ''), h('span', { class: 'ellipsis' }, e.name + (e.is_dir ? '/' : ''))),
    h('span', { class: 'muted' }, e.is_dir ? '—' : size(e.size)),
    h('span', { class: 'muted', title: fullStamp(e.mtime) }, stamp(e.mtime)));
    if (e.is_dir) dropTarget(r, () => ({ dir: full, path: '' }));
    onContext(r, () => [
      e.is_dir ? { label: t('files.openFolder'), run: () => go(full) } : null,
      { label: t('path.copy'), run: () => copyText(full) },
    ]);
    return r;
  };

  const render = () => {
    const dir = state.fsLoc?.dir || '';
    mount(bar,
      h('span', { class: 'muted' }, t('fs.title')),
      crumbs(),
      h('button', { class: 'btn icon', title: t('fs.up'), 'aria-label': t('fs.up'), disabled: !dir || (state.me?.roots || []).includes(dir), onclick: () => go(parent(dir)) }, icon('up')),
      h('button', { class: 'btn icon', title: t('fs.mkdir'), 'aria-label': t('fs.mkdir'), disabled: !dir, onclick: mkdir }, icon('folderPlus')),
      h('button', { class: 'btn icon' + (popover ? ' on' : ''), title: t('fs.settings'), 'aria-label': t('fs.settings'), 'aria-expanded': String(!!popover), onclick: toggleSettings }, icon('gear')),
      popover ? settingsPopover() : null);
    mount(body,
      h('div', { class: 'row head cols-fs', role: 'row' }, h('span', {}, t('files.name')), h('span', {}, t('fs.size')), h('span', {}, t('files.modified'))),
      error ? h('div', { class: 'notice' }, error) : null,
      entries.map(row),
      !entries.length && !error ? h('div', { class: 'empty' }, t('files.emptyDir')) : null,
      drop);
  };

  const mkdir = async () => {
    const name = await prompt(t('fs.mkdir'), t('fs.mkdirIn', { dir: state.fsLoc.dir }));
    if (!name) return;
    try {
      await api.post('/api/fs/mkdir', { dir: state.fsLoc.dir, path: '', name });
      load(state.fsLoc.dir);
    } catch (err) {
      toast(err.code === 'exists' ? t('fs.exists') : err.message, { bad: true });
    }
  };

  // перетаскивание из таблицы ветки: в открытую папку или в папку под курсором
  function dropTarget(node, target) {
    node.addEventListener('dragover', (e) => {
      if (!e.dataTransfer.types.includes(DRAG_TYPE)) return;
      e.preventDefault();
      e.stopPropagation();
      const move = e.shiftKey !== !!state.settings?.move_by_default;
      e.dataTransfer.dropEffect = move ? 'move' : 'copy';
      node.classList.add(node === el ? 'over' : 'drop');
    });
    node.addEventListener('dragleave', (e) => {
      if (!node.contains(e.relatedTarget)) node.classList.remove('over', 'drop');
    });
    node.addEventListener('drop', (e) => {
      node.classList.remove('over', 'drop');
      const raw = e.dataTransfer.getData(DRAG_TYPE);
      if (!raw) return;
      e.preventDefault();
      e.stopPropagation();
      let src;
      try {
        src = JSON.parse(raw);
      } catch {
        return;
      }
      const move = e.shiftKey !== !!state.settings?.move_by_default;
      runCopy({ src: { repo: src.repo, wt: src.wt, path: src.path }, items: src.items, dst: target(), move });
    });
  }
  dropTarget(el, () => ({ dir: state.fsLoc?.dir || '', path: '' }));

  document.addEventListener('pointerdown', (e) => {
    if (popover && !el.querySelector('.popover')?.contains(e.target) && !e.target.closest?.('[aria-label="' + t('fs.settings') + '"]')) {
      popover = null;
      render();
    }
  });

  const start = () => {
    if (started) return;
    started = true;
    const last = lastDir();
    load(state.settings?.open_on_start || !last ? '' : last);
  };

  on('fs', () => { if (state.fsOpen) start(); });
  on('changed', () => { if (started && state.fsOpen) load(state.fsLoc?.dir || ''); });
  on('settings', () => { if (popover) render(); });
  render();
  if (state.fsOpen) start();
}

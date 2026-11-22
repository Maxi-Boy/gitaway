// Открытый файл. До подключения CodeMirror (шаг 5) это textarea: правка, Ctrl+S,
// переход к строке, черновик несохранённой правки и защита от перезаписи чужих изменений.
import { h, mount, dialog, toast, fullStamp } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam } from '../store.js';

const LINE_PX = 13 * 1.55;

function draftKey(path) {
  return `gitaway:draft:${state.repo.id}:${state.wt?.path}:${path}`;
}

function readDraft(path) {
  try {
    return JSON.parse(localStorage.getItem(draftKey(path)));
  } catch {
    return null;
  }
}

function writeDraft(path, d) {
  try {
    if (d) localStorage.setItem(draftKey(path), JSON.stringify(d));
    else localStorage.removeItem(draftKey(path));
  } catch { /* черновик не сохранится, но правка в окне остаётся */ }
}

let current = null; // открытый редактор: для команды «Сохранить» и предупреждения при уходе

export function editorView(route) {
  const path = route.path;
  const el = h('div', { style: { height: '100%', display: 'flex', flexDirection: 'column' } });
  const info = h('span', { class: 'muted' });
  const saveBtn = h('button', { class: 'btn small', disabled: true, onclick: () => save() }, t('file.save'));
  let version = '';
  let original = '';
  let area = null;
  let draftTimer = null;

  const dirty = () => !!area && area.value !== original;

  const setInfo = () => {
    saveBtn.disabled = !dirty();
    info.textContent = dirty() ? t('file.unsaved') : '';
    info.className = dirty() ? 'dirty' : 'muted';
  };

  const goLine = (line) => {
    if (!area || !line) return;
    const lines = area.value.split('\n');
    let pos = 0;
    for (let i = 0; i < Math.min(line - 1, lines.length); i++) pos += lines[i].length + 1;
    area.focus();
    area.setSelectionRange(pos, pos + (lines[line - 1]?.length || 0));
    area.scrollTop = Math.max(0, (line - 5) * LINE_PX);
  };

  const show = (content, notice) => {
    area = h('textarea', {
      class: 'editor', spellcheck: false, autocapitalize: 'off', autocomplete: 'off', wrap: 'off',
      'aria-label': path,
    });
    area.value = content;
    area.addEventListener('input', () => {
      setInfo();
      clearTimeout(draftTimer);
      draftTimer = setTimeout(() => writeDraft(path, dirty() ? { version, content: area.value, at: Date.now() } : null), 400);
    });
    area.addEventListener('keydown', (e) => {
      if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault();
        area.setRangeText('\t', area.selectionStart, area.selectionEnd, 'end');
        area.dispatchEvent(new Event('input'));
      }
    });
    mount(el, notice, h('div', { style: { flex: '1', minHeight: '0' } }, area));
    setInfo();
    goLine(route.line);
  };

  const load = async () => {
    mount(el, h('div', { class: 'empty' }, t('common.loading')));
    let res;
    try {
      res = await api.get('/api/file', { repo: state.repo.id, wt: wtParam(), path });
    } catch (err) {
      const msg = { too_large: t('file.tooLarge'), binary_file: t('file.binary'), unsupported_encoding: t('file.encoding'), not_found: t('file.notFound') }[err.code];
      mount(el, h('div', { class: 'notice' }, msg || err.message));
      return;
    }
    version = res.version;
    original = res.content;
    const d = readDraft(path);
    if (d && d.content !== res.content) {
      const restore = h('button', { class: 'btn small', onclick: () => { show(d.content); } }, t('file.restore'));
      const drop = h('button', { class: 'btn small', onclick: () => { writeDraft(path, null); show(original); } }, t('file.dropDraft'));
      const text = d.version === version ? t('file.draftFound', { at: fullStamp(d.at / 1000) }) : t('file.draftStale', { at: fullStamp(d.at / 1000) });
      show(res.content, h('div', { class: 'notice field' }, h('span', { class: 'grow' }, text), restore, drop));
      return;
    }
    show(res.content);
  };

  const save = async (force = false) => {
    if (!area) return;
    const content = area.value;
    try {
      const res = await api.put('/api/file', { content, version: force ? '' : version }, { repo: state.repo.id, wt: wtParam(), path });
      version = res.version;
      original = content;
      writeDraft(path, null);
      setInfo();
      toast(t('file.saved'));
    } catch (err) {
      if (err.code === 'changed_on_disk') return conflict();
      toast(err.message, { bad: true });
    }
  };

  const conflict = () => {
    dialog({
      title: t('file.changedTitle'),
      body: h('p', {}, t('file.changedBody', { path })),
      buttons: [
        { label: t('common.cancel'), value: null },
        { label: t('file.reload'), run: (close) => { close(); writeDraft(path, { version, content: area.value, at: Date.now() }); load(); } },
        { label: t('file.overwrite'), danger: true, run: (close) => { close(); save(true); } },
      ],
    });
  };

  const view = {
    el,
    actions: [info, saveBtn],
    save: () => save(),
    dirty,
    goLine,
    destroy: () => {
      clearTimeout(draftTimer);
      if (dirty()) writeDraft(path, { version, content: area.value, at: Date.now() });
      if (current === view) current = null;
    },
  };
  current = view;
  load();
  return view;
}

export function currentEditor() {
  return current;
}

addEventListener('beforeunload', (e) => {
  if (current?.dirty()) e.preventDefault();
});

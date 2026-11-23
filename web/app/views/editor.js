// Открытый файл: CodeMirror 6 (web/vendor/codemirror.js, грузится при первом открытии файла).
// Если библиотека не загрузилась, работает простое текстовое поле с тем же поведением:
// Ctrl+S, переход к строке, черновик несохранённой правки, защита от перезаписи чужих изменений.
import { h, mount, dialog, toast, fullStamp } from '../ui.js';
import { t, phrases } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam } from '../store.js';
import * as act from '../actions.js';

let cmPromise = null;

function loadCM() {
  cmPromise ??= import('../../vendor/codemirror.js').catch((err) => {
    cmPromise = null;
    console.error('codemirror', err);
    return null;
  });
  return cmPromise;
}

// languageFor подбирает подсветку по имени файла.
function languageFor(cm, path) {
  const name = path.split('/').pop().toLowerCase();
  const ext = name.includes('.') ? name.split('.').pop() : '';
  const byName = {
    dockerfile: cm.dockerFile, makefile: cm.shell, '.bashrc': cm.shell, '.profile': cm.shell,
    '.bash_profile': cm.shell, '.zshrc': cm.shell, '.env': cm.properties, '.gitignore': cm.properties,
    'nginx.conf': cm.nginx, 'go.mod': cm.go, 'go.sum': null,
  };
  if (name in byName) return byName[name];
  if (name.startsWith('dockerfile')) return cm.dockerFile;
  if (path.includes('nginx/') && ext === 'conf') return cm.nginx;
  const byExt = {
    go: cm.go, py: cm.python, sh: cm.shell, bash: cm.shell, zsh: cm.shell, yml: cm.yaml, yaml: cm.yaml,
    toml: cm.toml, css: cm.css, scss: cm.css, html: cm.html, htm: cm.html, xml: cm.xml, svg: cm.xml,
    sql: cm.standardSQL, rs: cm.rust, c: cm.c, h: cm.c, cpp: cm.cpp, cc: cm.cpp, hpp: cm.cpp,
    java: cm.java, cs: cm.csharp, kt: cm.kotlin, js: cm.javascript, mjs: cm.javascript, cjs: cm.javascript,
    jsx: cm.javascript, json: cm.json, ts: cm.typescript, tsx: cm.typescript, diff: cm.diff, patch: cm.diff,
    properties: cm.properties, ini: cm.properties, cfg: cm.properties, conf: cm.properties, env: cm.properties,
    lua: cm.lua, pl: cm.perl, rb: cm.ruby, ps1: cm.powerShell, proto: cm.protobuf, service: cm.properties,
  };
  return byExt[ext] || null;
}

// Тема под цвета интерфейса: всё через CSS-переменные из style.css.
function theme(cm) {
  const view = cm.EditorView.theme({
    '&': { height: '100%', backgroundColor: 'var(--bg-deep)', color: 'var(--text)', fontSize: '13px' },
    '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.55' },
    '.cm-content': { caretColor: 'var(--text)' },
    '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--text)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': { backgroundColor: 'rgba(56, 139, 253, .35)' },
    '.cm-gutters': { backgroundColor: 'var(--bg-deep)', color: 'var(--muted)', border: 'none' },
    '.cm-activeLine': { backgroundColor: 'rgba(110, 118, 129, .1)' },
    '.cm-activeLineGutter': { backgroundColor: 'rgba(110, 118, 129, .1)', color: 'var(--text)' },
    '.cm-matchingBracket': { backgroundColor: 'rgba(63, 185, 80, .25)', outline: 'none' },
    '.cm-selectionMatch': { backgroundColor: 'rgba(210, 153, 34, .2)' },
    '.cm-searchMatch': { backgroundColor: 'rgba(210, 153, 34, .35)' },
    '.cm-searchMatch-selected': { backgroundColor: 'rgba(210, 153, 34, .6)' },
    '.cm-panels': { backgroundColor: 'var(--panel)', color: 'var(--text)', borderColor: 'var(--line)', fontSize: '13px' },
    '.cm-panel.cm-search': { padding: '6px 10px' },
    '.cm-panel input, .cm-panel button, .cm-panel label': { fontSize: '13px' },
    '.cm-panels input, .cm-panels button': { fontFamily: 'var(--sans)' },
    '.cm-textfield': { backgroundColor: 'var(--bg)', border: '1px solid var(--line)', borderRadius: '4px', color: 'var(--text)' },
    '.cm-button': { backgroundImage: 'none', backgroundColor: 'var(--ctl)', border: '1px solid var(--ctl-line)', borderRadius: '4px', color: 'var(--text)' },
    '.cm-foldPlaceholder': { backgroundColor: 'var(--ctl)', border: 'none', color: 'var(--muted)' },
  }, { dark: true });
  const tg = cm.tags;
  const hl = cm.HighlightStyle.define([
    { tag: [tg.keyword, tg.controlKeyword, tg.moduleKeyword, tg.operatorKeyword], color: '#ff7b72' },
    { tag: [tg.string, tg.special(tg.string), tg.regexp], color: '#a5d6ff' },
    { tag: [tg.comment, tg.lineComment, tg.blockComment], color: '#8b949e', fontStyle: 'italic' },
    { tag: [tg.number, tg.bool, tg.atom, tg.null], color: '#79c0ff' },
    { tag: [tg.typeName, tg.className, tg.namespace], color: '#ffa657' },
    { tag: [tg.function(tg.variableName), tg.definition(tg.variableName), tg.definition(tg.propertyName)], color: '#d2a8ff' },
    { tag: [tg.propertyName, tg.attributeName], color: '#79c0ff' },
    { tag: [tg.tagName, tg.heading], color: '#7ee787' },
    { tag: [tg.meta, tg.processingInstruction, tg.annotation], color: '#8b949e' },
    { tag: tg.inserted, color: '#7ee787', backgroundColor: 'rgba(63, 185, 80, .15)' },
    { tag: tg.deleted, color: '#ffa198', backgroundColor: 'rgba(255, 123, 114, .15)' },
    { tag: tg.invalid, color: '#ff7b72' },
  ]);
  return [view, cm.syntaxHighlighting(hl)];
}

// cmAdapter и textAdapter дают редактору одинаковый интерфейс.
function cmAdapter(cm, parent, content, path, onChange) {
  const lang = languageFor(cm, path);
  const view = new cm.EditorView({
    parent,
    state: cm.EditorState.create({
      doc: content,
      extensions: [
        cm.lineNumbers(), cm.highlightActiveLineGutter(), cm.highlightSpecialChars(), cm.history(),
        cm.foldGutter(), cm.drawSelection(), cm.dropCursor(), cm.indentOnInput(), cm.bracketMatching(),
        cm.rectangularSelection(), cm.highlightActiveLine(), cm.highlightSelectionMatches(), cm.search({ top: true }),
        cm.indentUnit.of(/\.(go|mk)$|makefile$/i.test(path) ? '\t' : '  '),
        cm.EditorState.phrases.of(phrases('cm.')),
        cm.keymap.of([...cm.defaultKeymap, ...cm.searchKeymap, ...cm.historyKeymap, ...cm.foldKeymap, cm.indentWithTab,
          { key: 'Mod-g', run: cm.gotoLine }]),
        lang ? cm.StreamLanguage.define(lang) : [],
        theme(cm),
        cm.EditorView.updateListener.of((u) => { if (u.docChanged) onChange(); }),
      ],
    }),
  });
  let clean = view.state.doc;
  return {
    text: () => view.state.doc.toString(),
    dirty: () => !view.state.doc.eq(clean),
    markClean: () => { clean = view.state.doc; },
    setText: (text) => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } }),
    focus: () => view.focus(),
    selection: () => {
      const r = view.state.selection.main;
      return view.state.sliceDoc(r.from, r.to);
    },
    goLine: (line) => {
      const n = Math.max(1, Math.min(line, view.state.doc.lines));
      const l = view.state.doc.line(n);
      view.dispatch({ selection: { anchor: l.from, head: l.to }, effects: cm.EditorView.scrollIntoView(l.from, { y: 'center' }) });
      view.focus();
    },
    destroy: () => view.destroy(),
  };
}

function textAdapter(parent, content, path, onChange) {
  const area = h('textarea', { class: 'editor', spellcheck: false, autocapitalize: 'off', autocomplete: 'off', wrap: 'off', 'aria-label': path });
  area.value = content;
  let clean = content;
  area.addEventListener('input', onChange);
  area.addEventListener('keydown', (e) => {
    if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      area.setRangeText('\t', area.selectionStart, area.selectionEnd, 'end');
      onChange();
    }
  });
  parent.append(area);
  return {
    text: () => area.value,
    dirty: () => area.value !== clean,
    markClean: () => { clean = area.value; },
    setText: (text) => { area.value = text; onChange(); },
    focus: () => area.focus(),
    selection: () => area.value.slice(area.selectionStart, area.selectionEnd),
    goLine: (line) => {
      const lines = area.value.split('\n');
      let pos = 0;
      for (let i = 0; i < Math.min(line - 1, lines.length); i++) pos += lines[i].length + 1;
      area.focus();
      area.setSelectionRange(pos, pos + (lines[line - 1]?.length || 0));
      area.scrollTop = Math.max(0, (line - 5) * 13 * 1.55);
    },
    destroy: () => {},
  };
}

// --- черновики несохранённой правки ---

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
  let ed = null;
  let draftTimer = null;
  let destroyed = false;

  const dirty = () => !!ed && ed.dirty();

  const setInfo = () => {
    saveBtn.disabled = !dirty();
    info.textContent = dirty() ? t('file.unsaved') : '';
    info.className = dirty() ? 'dirty' : 'muted';
  };

  const onChange = () => {
    setInfo();
    clearTimeout(draftTimer);
    draftTimer = setTimeout(() => writeDraft(path, dirty() ? { version, content: ed.text(), at: Date.now() } : null), 400);
  };

  // show открывает текст с диска; notice это полоса над редактором (например, про черновик).
  const show = async (content, notice) => {
    const cm = await loadCM();
    if (destroyed) return;
    ed?.destroy();
    const box = h('div', { style: { flex: '1', minHeight: '0' } });
    mount(el, notice, box);
    ed = cm ? cmAdapter(cm, box, content, path, onChange) : textAdapter(box, content, path, onChange);
    setInfo();
    if (route.line) ed.goLine(route.line);
    else ed.focus();
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
    const d = readDraft(path);
    if (d && d.content !== res.content) {
      const text = d.version === version ? t('file.draftFound', { at: fullStamp(d.at / 1000) }) : t('file.draftStale', { at: fullStamp(d.at / 1000) });
      const bar = h('div', { class: 'notice field' }, h('span', { class: 'grow' }, text),
        h('button', { class: 'btn small', onclick: () => { bar.remove(); ed.setText(d.content); } }, t('file.restore')),
        h('button', { class: 'btn small', onclick: () => { bar.remove(); writeDraft(path, null); } }, t('file.dropDraft')));
      await show(res.content, bar);
      return;
    }
    await show(res.content);
  };

  const save = async (force = false) => {
    if (!ed) return;
    const content = ed.text();
    try {
      const res = await api.put('/api/file', { content, version: force ? '' : version }, { repo: state.repo.id, wt: wtParam(), path });
      version = res.version;
      ed.markClean();
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
        { label: t('file.reload'), run: (close) => { close(); writeDraft(path, { version, content: ed.text(), at: Date.now() }); load(); } },
        { label: t('file.overwrite'), danger: true, run: (close) => { close(); save(true); } },
      ],
    });
  };

  const view = {
    el,
    actions: [info, saveBtn,
      h('button', { class: 'btn small', title: t('history.fileHistory'), onclick: () => act.nav({ view: 'history', path, commit: '' }) }, t('history.short'))],
    save: () => save(),
    selectedText: () => ed?.selection?.() || '',
    dirty,
    goLine: (n) => ed?.goLine(n),
    destroy: () => {
      destroyed = true;
      clearTimeout(draftTimer);
      if (dirty()) writeDraft(path, { version, content: ed.text(), at: Date.now() });
      ed?.destroy();
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

// Быстрое открытие файла (Ctrl+P): нечёткий поиск по путям всей вкладки.
// Список файлов приходит с сервера один раз и обновляется в фоне; поиск идёт в браузере,
// поэтому результат меняется сразу при наборе. «путь:строка» открывает файл на строке.
import { h, dialog, toast } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam, on } from '../store.js';
import * as act from '../actions.js';

const lists = new Map(); // папка вкладки -> {files, truncated, at}
const recentFiles = new Map(); // папка вкладки -> последние открытые пути
const MAX_SHOWN = 60;
const FRESH_MS = 30000;

on('route', () => {
  const r = state.route;
  if (r.view !== 'file' || !r.path || !state.wt) return;
  const list = recentFiles.get(state.wt.path) || [];
  recentFiles.set(state.wt.path, [r.path, ...list.filter((p) => p !== r.path)].slice(0, 20));
});
on('changed', () => {
  for (const v of lists.values()) v.at = 0; // новые и удалённые файлы: перечитать при открытии
});

async function loadFiles(force) {
  const key = state.wt.path;
  const cached = lists.get(key);
  if (cached && !force && Date.now() - cached.at < FRESH_MS) return cached;
  const res = await api.get('/api/files', { repo: state.repo.id, wt: wtParam() });
  const v = { files: res.files, truncated: !!res.truncated, at: Date.now() };
  lists.set(key, v);
  return v;
}

// match ищет буквы запроса в пути по порядку. Очки выше за совпадения в имени файла,
// подряд идущие буквы и начала слов. Возвращает {score, hits} или null.
export function match(path, query) {
  const q = query.toLowerCase();
  const p = path.toLowerCase();
  const nameStart = path.lastIndexOf('/') + 1;
  // сначала пробуем найти запрос в имени файла: так «main» находит main.go, а не m/a/i/n
  const inName = p.indexOf(q, nameStart);
  if (inName >= 0) {
    const hits = [];
    for (let i = 0; i < q.length; i++) hits.push(inName + i);
    return { score: 1000 + (inName === nameStart ? 200 : 0) - path.length, hits };
  }
  const hits = [];
  let score = 0;
  let pi = 0;
  let run = 0;
  for (let qi = 0; qi < q.length; qi++) {
    const c = q[qi];
    if (c === ' ') continue;
    const found = p.indexOf(c, pi);
    if (found < 0) return null;
    const prev = path[found - 1];
    const boundary = found === 0 || prev === '/' || prev === '_' || prev === '-' || prev === '.' ||
      (path[found] !== p[found] && prev === prev.toLowerCase());
    run = found === pi && hits.length ? run + 1 : 0;
    score += 1 + run * 3 + (boundary ? 4 : 0) + (found >= nameStart ? 2 : 0);
    hits.push(found);
    pi = found + 1;
  }
  return { score: score * 10 - path.length, hits };
}

function highlight(text, hits, offset) {
  const set = new Set(hits.map((x) => x - offset).filter((x) => x >= 0 && x < text.length));
  if (!set.size) return text;
  const out = [];
  let buf = '';
  for (let i = 0; i < text.length; i++) {
    if (set.has(i)) {
      if (buf) out.push(buf);
      buf = '';
      out.push(h('mark', {}, text[i]));
    } else buf += text[i];
  }
  if (buf) out.push(buf);
  return out;
}

export function quickOpen() {
  if (!state.wt) return;
  const input = h('input', { type: 'text', placeholder: t('quick.placeholder'), 'aria-label': t('quick.placeholder'), spellcheck: false, autocomplete: 'off' });
  const results = h('div', { class: 'results', role: 'listbox' });
  const note = h('div', { class: 'muted quick-note' });
  let data = lists.get(state.wt.path) || null;
  let items = [];
  let sel = 0;
  let d = null;

  const parseQuery = () => {
    const raw = input.value.trim();
    const m = /^(.*?):(\d+)$/.exec(raw);
    return m ? { q: m[1], line: Number(m[2]) } : { q: raw, line: 0 };
  };

  const render = () => {
    const { q } = parseQuery();
    if (!data) {
      results.replaceChildren(h('div', { class: 'empty' }, t('common.loading')));
      return;
    }
    if (!q) {
      const recent = (recentFiles.get(state.wt.path) || []).filter((p) => data.files.includes(p));
      items = (recent.length ? recent : data.files.slice(0, MAX_SHOWN)).map((path) => ({ path, hits: [] }));
    } else {
      const scored = [];
      for (const path of data.files) {
        const m = match(path, q);
        if (m) scored.push({ path, ...m });
      }
      scored.sort((a, b) => b.score - a.score || a.path.localeCompare(b.path));
      items = scored.slice(0, MAX_SHOWN);
    }
    sel = Math.min(sel, Math.max(0, items.length - 1));
    results.replaceChildren(...items.map((it, i) => {
      const slash = it.path.lastIndexOf('/');
      const name = it.path.slice(slash + 1);
      return h('button', { class: i === sel ? 'on' : null, role: 'option', 'aria-selected': String(i === sel), onclick: () => choose(i) },
        h('span', { class: 'ellipsis' }, h('span', {}, highlight(name, it.hits, slash + 1)),
          slash > 0 ? h('span', { class: 'muted' }, '  ', highlight(it.path.slice(0, slash), it.hits, 0)) : null));
    }));
    if (!items.length) results.append(h('div', { class: 'empty' }, t('quick.nothing')));
    results.children[sel]?.scrollIntoView({ block: 'nearest' });
    note.textContent = data.truncated ? t('quick.truncated', { n: data.files.length }) : q ? '' : t('quick.hint');
  };

  const choose = (i) => {
    const it = items[i];
    if (!it) return;
    d.close();
    act.nav({ view: 'file', path: it.path, line: parseQuery().line });
  };

  input.addEventListener('input', () => { sel = 0; render(); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { sel = Math.min(sel + 1, items.length - 1); render(); e.preventDefault(); }
    if (e.key === 'ArrowUp') { sel = Math.max(sel - 1, 0); render(); e.preventDefault(); }
    if (e.key === 'Enter') { choose(sel); e.preventDefault(); }
  });
  d = dialog({ title: t('quick.title'), body: [input, results, note] });
  d.el.classList.add('palette');
  render();
  input.focus();
  loadFiles(false).then((v) => { data = v; render(); }, (err) => {
    d.close();
    toast(err.message, { bad: true });
  });
}

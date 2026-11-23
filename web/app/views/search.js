// Поиск по проекту (Ctrl+Shift+F): git grep по файлам вкладки, результаты по файлам,
// щелчок по строке открывает файл на ней. Запрос и результаты запоминаются по вкладке.
import { h, mount } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam } from '../store.js';
import * as act from '../actions.js';
import { currentEditor } from './editor.js';

const saved = new Map(); // папка вкладки -> {q, files, regex, case, word, res, error}
let prefill = '';

// setSearchPrefill задаёт запрос для следующего открытия поиска (выделенный текст в редакторе).
export function setSearchPrefill(text) {
  prefill = text && !/[\r\n]/.test(text) && text.length <= 200 ? text : '';
}

export function editorSelection() {
  return currentEditor()?.selectedText?.() || '';
}

// matcher строит регулярное выражение для подсветки в строке результата. Сервер ищет
// своим git grep; здесь только подсветка, поэтому при расхождении (редкие классы ERE)
// строка просто показывается без выделения.
function matcher(o) {
  try {
    let src = o.regex ? o.q : o.q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    if (o.word) src = `(?<![\\p{L}\\p{N}_])(?:${src})(?![\\p{L}\\p{N}_])`;
    return new RegExp(src, 'gu' + (o.case ? '' : 'i'));
  } catch {
    return null;
  }
}

function marked(text, re) {
  if (!re) return text;
  const out = [];
  let last = 0;
  re.lastIndex = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (!m[0]) { re.lastIndex++; continue; }
    if (m.index > last) out.push(text.slice(last, m.index));
    out.push(h('mark', {}, m[0]));
    last = m.index + m[0].length;
    if (out.length > 200) break;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export function searchView() {
  const key = state.wt.path;
  const o = saved.get(key) || { q: '', files: '', regex: false, case: false, word: false, res: null, error: '' };
  saved.set(key, o);
  if (prefill) {
    o.q = prefill;
    o.res = null;
    prefill = '';
  }
  const el = h('div', { class: 'search' });
  const form = h('div', { class: 'search-form' });
  const out = h('div', { class: 'search-results', 'aria-live': 'polite' });
  const q = h('input', { type: 'search', value: o.q, placeholder: t('search.placeholder'), 'aria-label': t('search.placeholder'), spellcheck: false, autocomplete: 'off' });
  const files = h('input', { type: 'text', value: o.files, placeholder: t('search.filesPh'), 'aria-label': t('search.files'), spellcheck: false, autocomplete: 'off' });
  const collapsed = new Set();
  let seq = 0;
  let timer = null;

  const toggle = (name, label, title) => {
    const b = h('button', {
      class: 'btn small toggle' + (o[name] ? ' on' : ''), title, 'aria-label': title, 'aria-pressed': String(o[name]),
      onclick: () => {
        o[name] = !o[name];
        b.classList.toggle('on', o[name]);
        b.setAttribute('aria-pressed', String(o[name]));
        run();
      },
    }, label);
    return b;
  };

  const render = () => {
    if (o.error) return mount(out, h('div', { class: 'notice' }, o.error));
    if (!o.res) return mount(out, h('div', { class: 'empty' }, o.q ? t('common.loading') : t('search.intro')));
    const res = o.res;
    if (!res.matches.length) return mount(out, h('div', { class: 'empty' }, t('search.nothing')));
    const re = matcher(o);
    const groups = [];
    let cur = null;
    for (const m of res.matches) {
      if (!cur || cur.path !== m.path) groups.push(cur = { path: m.path, list: [] });
      cur.list.push(m);
    }
    mount(out,
      h('div', { class: 'muted search-sum' }, t('search.summary', { n: res.matches.length, files: res.files }),
        res.truncated ? ' · ' + t('search.truncated') : ''),
      groups.map((g) => {
        const slash = g.path.lastIndexOf('/');
        const open = !collapsed.has(g.path);
        return h('div', { class: 'sgroup' },
          h('button', {
            class: 'sfile', 'aria-expanded': String(open), title: g.path,
            onclick: () => { if (open) collapsed.add(g.path); else collapsed.delete(g.path); render(); },
          }, h('span', { class: 'ico', 'aria-hidden': 'true' }, open ? '▾' : '▸'),
          h('span', { class: 'ellipsis grow' }, h('b', {}, g.path.slice(slash + 1)), slash > 0 ? h('span', { class: 'muted' }, '  ' + g.path.slice(0, slash)) : null),
          h('span', { class: 'badge' }, String(g.list.length))),
          open ? g.list.map((m) => h('button', {
            class: 'sline', title: g.path + ':' + m.line,
            onclick: () => act.nav({ view: 'file', path: m.path, line: m.line }),
          }, h('span', { class: 'ln muted' }, String(m.line)), h('span', { class: 'txt' }, marked(m.text, re)))) : null);
      }));
  };

  // keep: обновить результаты, не стирая старые на время запроса (возврат на экран)
  const run = async (keep = false) => {
    clearTimeout(timer);
    o.q = q.value;
    o.files = files.value;
    o.error = '';
    if (!o.q.trim()) {
      o.res = null;
      seq++;
      return render();
    }
    const my = ++seq;
    if (!keep) {
      o.res = null;
      render();
    }
    try {
      const res = await api.get('/api/search', {
        repo: state.repo.id, wt: wtParam(), q: o.q, files: o.files.trim(), regex: o.regex, case: o.case, word: o.word,
      });
      if (my !== seq) return;
      o.res = res;
      if (!keep) collapsed.clear();
    } catch (err) {
      if (my !== seq) return;
      o.error = err.message;
    }
    render();
  };
  const later = () => {
    clearTimeout(timer);
    timer = setTimeout(run, 400);
  };
  q.addEventListener('input', later);
  files.addEventListener('input', later);
  for (const inp of [q, files]) {
    inp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); run(); }
    });
  }

  mount(form,
    h('div', { class: 'field' }, q,
      toggle('case', 'Aa', t('search.case')), toggle('word', 'W', t('search.word')), toggle('regex', '.*', t('search.regex'))),
    files);
  mount(el, form, out);
  render();
  if (o.q.trim()) run(!!o.res); // файлы могли измениться, пока экран был закрыт
  requestAnimationFrame(() => { q.focus(); q.select(); });

  return {
    el,
    focus: () => { q.focus(); q.select(); },
    setQuery: (text) => { q.value = text; run(); },
    destroy: () => clearTimeout(timer),
  };
}

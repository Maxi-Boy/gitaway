// Реестр команд. Кнопки, меню, горячие клавиши и палитра (Ctrl+K) вызывают
// действия только через него, поэтому новая возможность это одна регистрация.
import { h, dialog, toast } from './ui.js';
import { t } from './i18n.js';

const registry = new Map();
const recent = [];

// register({id, title, keys: 'Ctrl+S', run, when: () => bool, palette: true})
export function register(cmd) {
  registry.set(cmd.id, { palette: true, ...cmd });
}

export function get(id) {
  return registry.get(id);
}

export function enabled(id) {
  const c = registry.get(id);
  return !!c && (!c.when || c.when());
}

export async function run(id, ...args) {
  const c = registry.get(id);
  if (!c || (c.when && !c.when())) return;
  const i = recent.indexOf(id);
  if (i >= 0) recent.splice(i, 1);
  recent.unshift(id);
  try {
    await c.run(...args);
  } catch (err) {
    toast(err.message || String(err), { bad: true });
  }
}

// --- горячие клавиши ---

function keyOf(e) {
  const parts = [];
  if (e.ctrlKey || e.metaKey) parts.push('Ctrl');
  if (e.altKey) parts.push('Alt');
  if (e.shiftKey) parts.push('Shift');
  let k = e.key.length === 1 ? e.key.toUpperCase() : e.key;
  if (e.code?.startsWith('Key')) k = e.code.slice(3); // одинаково в любой раскладке
  parts.push(k);
  return parts.join('+');
}

addEventListener('keydown', (e) => {
  if (document.querySelector('.overlay')) return; // в открытом диалоге свои клавиши
  const k = keyOf(e);
  for (const c of registry.values()) {
    if (c.keys && c.keys.split(' ').includes(k) && (!c.when || c.when())) {
      e.preventDefault();
      run(c.id);
      return;
    }
  }
});

// --- палитра ---

export function openPalette() {
  const input = h('input', { type: 'text', placeholder: t('palette.placeholder'), 'aria-label': t('palette.placeholder') });
  const results = h('div', { class: 'results', role: 'listbox' });
  let items = [];
  let sel = 0;
  let d = null;

  const render = () => {
    const q = input.value.trim().toLowerCase();
    const all = [...registry.values()].filter((c) => c.palette && (!c.when || c.when()));
    const score = (c) => {
      const r = recent.indexOf(c.id);
      return r < 0 ? 1000 : r;
    };
    items = all
      .filter((c) => !q || q.split(/\s+/).every((w) => c.title.toLowerCase().includes(w)))
      .sort((a, b) => score(a) - score(b) || a.title.localeCompare(b.title, 'ru'));
    sel = Math.min(sel, Math.max(0, items.length - 1));
    results.replaceChildren(...items.map((c, i) => h('button', {
      class: i === sel ? 'on' : null, role: 'option',
      onclick: () => choose(i),
    }, h('span', {}, c.title), c.keys ? h('span', { class: 'muted' }, c.keys.split(' ')[0]) : null)));
    if (!items.length) results.append(h('div', { class: 'empty' }, t('palette.nothing')));
    results.children[sel]?.scrollIntoView({ block: 'nearest' });
  };
  const choose = (i) => {
    const c = items[i];
    if (!c) return;
    d.close();
    run(c.id);
  };
  input.addEventListener('input', () => { sel = 0; render(); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { sel = Math.min(sel + 1, items.length - 1); render(); e.preventDefault(); }
    if (e.key === 'ArrowUp') { sel = Math.max(sel - 1, 0); render(); e.preventDefault(); }
    if (e.key === 'Enter') { choose(sel); e.preventDefault(); }
  });
  d = dialog({ title: t('palette.title'), body: [input, results] });
  d.el.classList.add('palette');
  render();
  input.focus();
}

register({ id: 'palette', title: t('palette.title'), keys: 'Ctrl+K Ctrl+Shift+P', run: openPalette, palette: false });

// Общие элементы интерфейса: создание DOM, меню, диалоги, уведомления.
import { t, lang } from './i18n.js';

// h создаёт элемент: h('button', {class: 'btn', onclick}, 'текст', child, [дети]).
// Текст всегда вставляется как текст, не как HTML.
export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
    else if (k === 'dataset') Object.assign(el.dataset, v);
    else if (k in el && k !== 'list' && typeof v !== 'string') el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

// mount заменяет содержимое контейнера.
export function mount(el, ...children) {
  el.replaceChildren();
  append(el, children);
  // длинные хлебные крошки прокручиваются к концу: текущая папка всегда видна
  for (const c of el.querySelectorAll('.crumbs')) requestAnimationFrame(() => { c.scrollLeft = c.scrollWidth; });
}

// Иконки: простые SVG с обводкой, цвет от текста.
const ICONS = {
  gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  up: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  folderPlus: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M12 11v6M9 14h6"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-3-6.7L21 8"/><path d="M21 3v5h-5"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/>',
  chevron: '<path d="m6 9 6 6 6-6"/>',
};

export function icon(name) {
  const span = document.createElement('span');
  span.setAttribute('aria-hidden', 'true');
  span.style.display = 'inline-flex';
  // Разметка иконок задана в коде выше, пользовательских данных в ней нет.
  span.innerHTML = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">${ICONS[name] || ''}</svg>`;
  return span;
}

// --- меню ---

let openMenu = null;

export function closeMenu() {
  if (openMenu) {
    openMenu.remove();
    openMenu = null;
  }
}

// showMenu показывает меню в точке (x, y). items: {label, run, keys, danger, disabled},
// '-' это разделитель, {section: 'текст'} это подпись группы.
export function showMenu(x, y, items) {
  closeMenu();
  const menu = h('div', { class: 'menu', role: 'menu' });
  for (const it of items) {
    if (!it) continue;
    if (it === '-') { menu.append(h('hr')); continue; }
    if (it.section) { menu.append(h('div', { class: 'label' }, it.section)); continue; }
    menu.append(h('button', {
      role: 'menuitem', class: it.danger ? 'danger' : null, disabled: !!it.disabled,
      onclick: () => { closeMenu(); it.run(); },
    }, h('span', { class: 'ellipsis' }, it.label), it.keys ? h('span', { class: 'keys' }, it.keys) : null));
  }
  document.body.append(menu);
  const r = menu.getBoundingClientRect();
  menu.style.left = Math.max(4, Math.min(x, innerWidth - r.width - 4)) + 'px';
  menu.style.top = Math.max(4, Math.min(y, innerHeight - r.height - 4)) + 'px';
  openMenu = menu;
  menu.querySelector('button:not(:disabled)')?.focus();
  return menu;
}

// menuAt показывает меню под элементом (для кнопок с выпадающим списком).
export function menuAt(el, items) {
  const r = el.getBoundingClientRect();
  return showMenu(r.left, r.bottom + 4, items);
}

document.addEventListener('pointerdown', (e) => {
  if (openMenu && !openMenu.contains(e.target)) closeMenu();
}, true);
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') closeMenu();
});
addEventListener('blur', closeMenu);

// onContext вешает контекстное меню: правый клик на ПК и долгое нажатие на телефоне.
export function onContext(el, getItems) {
  el.addEventListener('contextmenu', (e) => {
    e.preventDefault();
    const items = getItems(e);
    if (items) showMenu(e.clientX, e.clientY, items);
  });
  let timer = null;
  let start = null;
  el.addEventListener('touchstart', (e) => {
    const p = e.touches[0];
    start = { x: p.clientX, y: p.clientY };
    timer = setTimeout(() => {
      timer = null;
      const items = getItems(e);
      if (items) showMenu(start.x, start.y, items);
    }, 550);
  }, { passive: true });
  const cancel = () => { if (timer) clearTimeout(timer); timer = null; };
  el.addEventListener('touchend', cancel);
  el.addEventListener('touchmove', (e) => {
    const p = e.touches[0];
    if (start && Math.hypot(p.clientX - start.x, p.clientY - start.y) > 10) cancel();
  }, { passive: true });
}

// --- диалоги ---

// dialog открывает модальное окно. Возвращает {close, el}. onClose вызывается один раз.
export function dialog({ title, body, buttons = [], wide = false, mid = false, onClose }) {
  const prev = document.activeElement;
  let closed = false;
  const close = (value) => {
    if (closed) return;
    closed = true;
    overlay.remove();
    removeEventListener('keydown', onKey, true);
    prev?.focus?.();
    onClose?.(value);
  };
  const foot = buttons.map((b) => b === 'grow' ? h('span', { class: 'grow' }) : h('button', {
    class: 'btn' + (b.primary ? ' primary' : '') + (b.danger ? ' danger' : ''),
    onclick: () => b.run ? b.run(close) : close(b.value),
  }, b.label));
  const box = h('div', { class: 'dialog' + (wide ? ' wide' : mid ? ' mid' : ''), role: 'dialog', 'aria-modal': 'true', 'aria-label': title },
    h('header', {}, title), h('div', { class: 'body' }, body), foot.length ? h('footer', {}, foot) : null);
  const overlay = h('div', { class: 'overlay', onpointerdown: (e) => { if (e.target === overlay) close(); } }, box);
  const onKey = (e) => {
    if (e.key === 'Escape') { e.stopPropagation(); close(); }
  };
  addEventListener('keydown', onKey, true);
  document.body.append(overlay);
  (box.querySelector('input, textarea, select') || box.querySelector('footer .primary') || box.querySelector('button'))?.focus();
  return { close, el: box };
}

// confirm спрашивает да/нет; danger красит кнопку подтверждения.
export function confirm(title, body, okLabel, danger = false) {
  return new Promise((resolve) => {
    dialog({
      title, body,
      buttons: [{ label: t('common.cancel'), value: false }, { label: okLabel, value: true, primary: !danger, danger }],
      onClose: (v) => resolve(v === true),
    });
  });
}

// prompt спрашивает строку. Пустой ответ или отмена дают null.
export function prompt(title, label, value = '', placeholder = '') {
  return new Promise((resolve) => {
    const input = h('input', { type: 'text', value, placeholder, 'aria-label': label });
    let d = null;
    const ok = (close) => close(input.value.trim() || null);
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') ok(d.close); });
    d = dialog({
      title,
      body: [label ? h('p', { class: 'muted' }, label) : null, input],
      buttons: [{ label: t('common.cancel'), value: null }, { label: t('common.ok'), primary: true, run: ok }],
      onClose: (v) => resolve(typeof v === 'string' ? v : null),
    });
  });
}

// --- уведомления ---

let toastBox = null;

export function toast(text, { bad = false, ms = 4000 } = {}) {
  if (!toastBox) {
    toastBox = h('div', { class: 'toasts', role: 'status', 'aria-live': 'polite' });
    document.body.append(toastBox);
  }
  const el = h('div', { class: 'toast' + (bad ? ' bad' : '') }, text);
  toastBox.append(el);
  setTimeout(() => el.remove(), bad ? ms * 2 : ms);
}

// --- форматирование ---

const rtf = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' });

// ago: "2 ч назад", "вчера"; unix в секундах.
export function ago(unix) {
  if (!unix) return '—';
  const s = Math.round(unix - Date.now() / 1000);
  const abs = Math.abs(s);
  if (abs < 60) return rtf.format(0, 'second');
  if (abs < 3600) return rtf.format(Math.round(s / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(s / 3600), 'hour');
  if (abs < 86400 * 30) return rtf.format(Math.round(s / 86400), 'day');
  if (abs < 86400 * 365) return rtf.format(Math.round(s / 86400 / 30), 'month');
  return rtf.format(Math.round(s / 86400 / 365), 'year');
}

const dtf = new Intl.DateTimeFormat(lang, { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const dtfYear = new Intl.DateTimeFormat(lang, { day: '2-digit', month: '2-digit', year: 'numeric' });

// stamp: "20.11, 08:41" в этом году, иначе "20.11.2025".
export function stamp(unix) {
  if (!unix) return '—';
  const d = new Date(unix * 1000);
  return d.getFullYear() === new Date().getFullYear() ? dtf.format(d) : dtfYear.format(d);
}

// stampYY: "22/11/26 11:00", с коротким годом; для сравнения дат в диалоге конфликта.
export function stampYY(unix) {
  if (!unix) return '—';
  const d = new Date(unix * 1000);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getDate())}/${p(d.getMonth() + 1)}/${p(d.getFullYear() % 100)} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function fullStamp(unix) {
  return unix ? new Date(unix * 1000).toLocaleString(lang) : '—';
}

export function size(n) {
  if (n == null) return '—';
  if (n < 1024) return t('size.b', { n });
  if (n < 1024 * 1024) return t('size.kb', { n: (n / 1024).toFixed(1) });
  if (n < 1024 ** 3) return t('size.mb', { n: (n / 1024 / 1024).toFixed(1) });
  return t('size.gb', { n: (n / 1024 ** 3).toFixed(1) });
}

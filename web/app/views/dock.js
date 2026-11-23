// Нижняя панель: «Терминал», «Задачи», «Проблемы», «Вывод». Терминал и вывод работают;
// задачи и проблемы заполнят сеансы задач в 0.2. Высота панели меняется перетаскиванием
// верхней границы и запоминается.
import { h, mount } from '../ui.js';
import { t, lang } from '../i18n.js';
import { state, on, emit } from '../store.js';
import { terminalPanel } from './terminal.js';
import { tasksPane } from './tasks.js';

const TABS = ['terminal', 'tasks', 'problems', 'output'];
const KEY = 'gitaway:dock';

function load() {
  try {
    return JSON.parse(localStorage.getItem(KEY)) || {};
  } catch {
    return {};
  }
}

function save(v) {
  try {
    localStorage.setItem(KEY, JSON.stringify(v));
  } catch { /* не запомнится */ }
}

const time = new Intl.DateTimeFormat(lang, { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' });

export function dock(el) {
  const saved = load();
  let tab = TABS.includes(saved.tab) ? saved.tab : 'output';
  let collapsed = !!saved.collapsed;
  let height = saved.height || 0;
  const persist = () => save({ tab, collapsed, height });

  const grip = h('div', { class: 'dock-grip', role: 'separator', 'aria-orientation': 'horizontal', 'aria-label': t('dock.resize') });
  const tabsEl = h('div', { class: 'dock-tabs', role: 'tablist' });
  // у каждой вкладки свой постоянный контейнер: терминал не пересоздаётся при переключении
  const panes = Object.fromEntries(TABS.map((name) => [name, h('div', { class: 'dock-body', role: 'tabpanel', hidden: true })]));
  mount(el, grip, tabsEl, Object.values(panes));
  const term = terminalPanel(panes.terminal);
  panes.terminal.classList.add('term-pane');
  const tasks = tasksPane(panes.tasks);
  term.preload(); // сеансы, начатые раньше: значки задач на вкладках веток видны сразу
  mount(panes.problems, h('div', { class: 'muted' }, t('dock.soon.problems')));

  const applyHeight = () => {
    el.style.height = collapsed ? '' : height ? height + 'px' : '';
  };

  // изменение высоты перетаскиванием границы
  grip.addEventListener('pointerdown', (e) => {
    if (collapsed) return;
    e.preventDefault();
    grip.setPointerCapture(e.pointerId);
    const startY = e.clientY;
    const startH = el.getBoundingClientRect().height;
    const move = (ev) => {
      height = Math.max(120, Math.min(innerHeight - 160, startH + startY - ev.clientY));
      applyHeight();
    };
    const up = () => {
      grip.removeEventListener('pointermove', move);
      grip.removeEventListener('pointerup', up);
      persist();
    };
    grip.addEventListener('pointermove', move);
    grip.addEventListener('pointerup', up);
  });

  const setTab = (name) => {
    if (tab === name && !collapsed) collapsed = true;
    else { tab = name; collapsed = false; }
    persist();
    render();
  };

  const renderOutput = () => {
    if (!state.log.length) return mount(panes.output, h('div', { class: 'muted' }, t('dock.outputEmpty')));
    mount(panes.output, state.log.map((e) => h('div', { class: 'log-entry' },
      h('div', { class: 'cmd ' + (e.ok ? 'ok' : 'bad') }, time.format(e.time) + '  ' + e.cmd),
      e.output ? h('pre', {}, e.output) : null)));
    panes.output.scrollTop = panes.output.scrollHeight;
  };

  const render = () => {
    el.classList.toggle('collapsed', collapsed);
    applyHeight();
    mount(tabsEl,
      TABS.map((name) => h('button', {
        class: 't' + (name === tab && !collapsed ? ' on' : ''), role: 'tab', 'aria-selected': String(name === tab && !collapsed),
        onclick: () => setTab(name),
      }, t('dock.' + name), name === 'output' && state.log.some((e) => !e.ok) ? ' •' : '')),
      h('span', { class: 'grow' }),
      tab === 'output' && state.log.length && !collapsed ? h('button', { class: 't', onclick: () => { state.log = []; emit('log'); } }, t('dock.clear')) : null,
      h('button', { class: 't', 'aria-label': collapsed ? t('dock.expand') : t('dock.collapse'), onclick: () => { collapsed = !collapsed; persist(); render(); } }, collapsed ? '▴' : '▾'));
    for (const name of TABS) panes[name].hidden = collapsed || name !== tab;
    if (!collapsed && tab === 'output') renderOutput();
    if (!collapsed && tab === 'terminal') term.show();
    if (!collapsed && tab === 'tasks') tasks.show();
  };

  on('log', () => { if (tab === 'output' && !collapsed) renderOutput(); else render(); });
  render();
  return {
    show: (name) => { tab = name; collapsed = false; persist(); render(); },
    newTerminal: (body) => { tab = 'terminal'; collapsed = false; persist(); render(); return term.newSession(body); },
    openSession: (info) => { tab = 'terminal'; collapsed = false; persist(); render(); return term.open(info); },
  };
}

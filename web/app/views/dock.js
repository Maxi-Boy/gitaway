// Нижняя панель: «Терминал», «Задачи», «Проблемы», «Вывод». В 0.1 работает «Вывод»
// (результаты операций); остальные вкладки заполнят сеансы и задачи в 0.2.
import { h, mount } from '../ui.js';
import { t } from '../i18n.js';
import { state, on, emit } from '../store.js';

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

const time = new Intl.DateTimeFormat('ru', { hour: '2-digit', minute: '2-digit', second: '2-digit' });

export function dock(el) {
  const saved = load();
  let tab = TABS.includes(saved.tab) ? saved.tab : 'output';
  let collapsed = !!saved.collapsed;
  const tabsEl = h('div', { class: 'dock-tabs', role: 'tablist' });
  const body = h('div', { class: 'dock-body', role: 'tabpanel', 'aria-live': 'polite' });
  mount(el, tabsEl, body);

  const setTab = (name) => {
    if (tab === name && !collapsed) collapsed = true;
    else { tab = name; collapsed = false; }
    save({ tab, collapsed });
    render();
  };

  const renderOutput = () => {
    if (!state.log.length) return h('div', { class: 'muted' }, t('dock.outputEmpty'));
    return state.log.map((e) => h('div', { class: 'log-entry' },
      h('div', { class: 'cmd ' + (e.ok ? 'ok' : 'bad') }, time.format(e.time) + '  ' + e.cmd),
      e.output ? h('pre', {}, e.output) : null));
  };

  const render = () => {
    el.classList.toggle('collapsed', collapsed);
    mount(tabsEl,
      TABS.map((name) => h('button', {
        class: 't' + (name === tab && !collapsed ? ' on' : ''), role: 'tab', 'aria-selected': String(name === tab && !collapsed),
        onclick: () => setTab(name),
      }, t('dock.' + name), name === 'output' && state.log.some((e) => !e.ok) ? ' •' : '')),
      h('span', { class: 'grow' }),
      tab === 'output' && state.log.length && !collapsed ? h('button', { class: 't', onclick: () => { state.log = []; emit('log'); } }, t('dock.clear')) : null,
      h('button', { class: 't', 'aria-label': collapsed ? t('dock.expand') : t('dock.collapse'), onclick: () => { collapsed = !collapsed; save({ tab, collapsed }); render(); } }, collapsed ? '▴' : '▾'));
    if (collapsed) return mount(body);
    if (tab === 'output') {
      mount(body, renderOutput());
      body.scrollTop = body.scrollHeight;
    } else mount(body, h('div', { class: 'muted' }, t('dock.soon.' + tab)));
  };

  on('log', render);
  render();
  return {
    show: (name) => { tab = name; collapsed = false; save({ tab, collapsed }); render(); },
  };
}

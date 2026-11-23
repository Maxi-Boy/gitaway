// Терминал в нижней панели: сеансы на сервере (pty), xterm.js из web/vendor (грузится при
// первом открытии). Процесс живёт на сервере: после перезагрузки страницы или обрыва связи
// терминал переподключается и показывает накопленный вывод.
import { h, mount, toast } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, wtParam, emit } from '../store.js';
import { taskFinished } from './tasks.js';
import { cloneFinished } from './addrepo.js';

let xtermPromise = null;

function loadXterm() {
  if (!xtermPromise) {
    if (!document.querySelector('link[data-xterm]')) {
      document.head.append(h('link', { rel: 'stylesheet', href: 'vendor/xterm.css', 'data-xterm': '' }));
    }
    xtermPromise = import('../../vendor/xterm.js').catch((err) => {
      xtermPromise = null;
      throw err;
    });
  }
  return xtermPromise;
}

const THEME = {
  background: '#0b0f14', foreground: '#c9d1d9', cursor: '#c9d1d9', selectionBackground: '#264f78',
  black: '#484f58', red: '#ff7b72', green: '#3fb950', yellow: '#d29922', blue: '#58a6ff', magenta: '#bc8cff',
  cyan: '#39c5cf', white: '#b1bac4', brightBlack: '#6e7681', brightRed: '#ffa198', brightGreen: '#56d364',
  brightYellow: '#e3b341', brightBlue: '#79c0ff', brightMagenta: '#d2a8ff', brightCyan: '#56d4dd', brightWhite: '#f0f6fc',
};

const enc = new TextEncoder();
const touch = matchMedia('(pointer: coarse)').matches;

export function terminalPanel(el) {
  const tabs = h('div', { class: 'term-tabs', role: 'tablist' });
  const screens = h('div', { class: 'term-screens' });
  const keys = touch ? h('div', { class: 'term-keys', 'aria-label': t('term.keys') }) : null;
  mount(el, tabs, screens, keys);
  const terms = new Map(); // id -> {info, term, fit, box, ws, closed}
  let active = null;
  let ctrl = false; // «залипающий» Ctrl для клавиатуры телефона
  let loaded = false;

  // publish сообщает остальному интерфейсу о сеансах: значки на вкладках веток, список задач
  const publish = () => {
    state.sessions = [...terms.values()].map((s) => s.info);
    emit('sessions');
  };

  const label = (info) => {
    const name = ({ task: '▶ ', clone: '⇣ ' }[info.kind] || '') + (info.title || info.id.slice(0, 6));
    if (info.running) return name;
    if (info.kind === 'task' || info.kind === 'clone') return [name, h('span', { class: info.exit_code === 0 ? 'ok' : 'bad' }, info.exit_code === 0 ? ' ✓' : ' ✗ ' + info.exit_code)];
    return name + ' ' + t('term.exited', { code: info.exit_code });
  };

  const renderTabs = () => {
    publish();
    const list = [...terms.values()].map(({ info }) => h('button', {
      class: 't' + (info.id === active ? ' on' : ''), role: 'tab', 'aria-selected': String(info.id === active), title: info.dir,
      onclick: () => activate(info.id),
    }, label(info),
    h('span', {
      class: 'x', role: 'button', 'aria-label': t('term.close'), title: t('term.close'),
      onclick: (e) => { e.stopPropagation(); closeSession(info.id); },
    }, '×')));
    mount(tabs, list, h('button', { class: 't', title: t('term.new') + ' (Ctrl+`)', onclick: () => newSession() }, '+ ' + t('term.newShort')));
    if (!terms.size) mount(screens, h('div', { class: 'muted', style: { padding: '8px 16px' } }, t('term.empty')));
  };

  const activate = (id) => {
    active = id;
    for (const [k, s] of terms) s.box.hidden = k !== id;
    renderTabs();
    const s = terms.get(id);
    if (s) requestAnimationFrame(() => { fit(s); s.term.focus(); });
  };

  const fit = (s) => {
    if (!s || s.box.hidden || !s.box.offsetWidth) return;
    try {
      s.fit.fit();
    } catch { /* панель скрыта */ }
  };

  const send = (s, data) => {
    if (s.ws?.readyState === WebSocket.OPEN) s.ws.send(typeof data === 'string' ? enc.encode(data) : data);
  };

  const connect = (s) => {
    const url = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/sessions/ws?id=' + encodeURIComponent(s.info.id);
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    s.ws = ws;
    let first = true;
    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'resize', cols: s.term.cols, rows: s.term.rows }));
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        const m = JSON.parse(ev.data);
        if (m.type === 'exit') {
          s.info.running = false;
          s.info.exit_code = m.code;
          s.closed = true;
          if (s.info.kind === 'shell' || !s.info.kind) s.term.write('\r\n\x1b[2m' + t('term.exitedLine', { code: m.code }) + '\x1b[0m\r\n');
          else if (s.watched && s.info.kind === 'task') taskFinished(s.info); // уведомлять только о том, что шло на глазах
          else if (s.watched && s.info.kind === 'clone') cloneFinished(s.info);
          s.watched = false;
          renderTabs();
        }
        return;
      }
      if (first) { s.term.reset(); first = false; } // переподключение: экран заново из буфера сервера
      s.term.write(new Uint8Array(ev.data));
    };
    ws.onclose = () => {
      if (s.ws !== ws || s.closed || !terms.has(s.info.id)) return;
      setTimeout(() => { if (terms.has(s.info.id) && !s.closed) connect(s); }, 1500);
    };
  };

  const attach = async (info) => {
    const { Terminal, FitAddon } = await loadXterm();
    if (terms.has(info.id)) return terms.get(info.id);
    const box = h('div', { class: 'term-screen', hidden: true });
    screens.querySelector('.muted')?.remove();
    screens.append(box);
    const term = new Terminal({
      fontFamily: getComputedStyle(document.body).getPropertyValue('--mono'), fontSize: 13, cursorBlink: true,
      scrollback: 5000, theme: THEME, allowProposedApi: false,
    });
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(box);
    const s = { info, term, fit: fitAddon, box, ws: null, closed: !info.running, watched: info.running };
    term.onData((d) => {
      if (ctrl && d.length === 1) {
        const c = d.toUpperCase().charCodeAt(0);
        if (c >= 64 && c <= 95) d = String.fromCharCode(c - 64); // Ctrl+буква
        ctrl = false;
        renderKeys();
      }
      send(s, d);
    });
    term.onResize(({ cols, rows }) => {
      if (s.ws?.readyState === WebSocket.OPEN) s.ws.send(JSON.stringify({ type: 'resize', cols, rows }));
    });
    terms.set(info.id, s);
    connect(s);
    return s;
  };

  const newSession = async (body) => {
    const req = body || (state.repo && state.wt ? { repo: state.repo.id, wt: wtParam() } : { dir: state.fsLoc?.dir || '' });
    try {
      await loadXterm();
      const info = await api.post('/api/sessions', { ...req, cols: 120, rows: 30 });
      await attach(info);
      activate(info.id);
    } catch (err) {
      toast(err.status === 429 ? t('term.tooMany') : err.message || t('term.loadFailed'), { bad: true });
    }
  };

  const closeSession = async (id) => {
    const s = terms.get(id);
    try {
      await api.del('/api/sessions', { id });
    } catch (err) {
      if (err.status !== 404) return toast(err.message, { bad: true });
    }
    if (s) {
      s.closed = true;
      s.ws?.close();
      s.term.dispose();
      s.box.remove();
      terms.delete(id);
    }
    if (active === id) active = [...terms.keys()].pop() || null;
    if (active) activate(active);
    else renderTabs();
  };

  // сеансы, начатые раньше (другая вкладка браузера, до перезагрузки)
  const load = async () => {
    if (loaded) return;
    loaded = true;
    try {
      const list = await api.get('/api/sessions');
      if (list.length) await loadXterm();
      for (const info of list) await attach(info);
      if (list.length) activate(list[list.length - 1].id);
      else renderTabs();
    } catch (err) {
      loaded = false;
      toast(err.message || t('term.loadFailed'), { bad: true });
    }
  };

  // клавиши, которых нет на экранной клавиатуре телефона
  const KEYS = [['Esc', '\x1b'], ['Tab', '\t'], ['Ctrl', null], ['←', '\x1b[D'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['→', '\x1b[C'],
    ['|', '|'], ['~', '~'], ['/', '/'], ['-', '-']];
  const renderKeys = () => {
    if (!keys) return;
    mount(keys, KEYS.map(([label, seq]) => h('button', {
      class: 'btn small' + (seq === null && ctrl ? ' on' : ''), 'aria-pressed': seq === null ? String(ctrl) : null,
      onpointerdown: (e) => e.preventDefault(), // фокус остаётся в терминале, клавиатура не прячется
      onclick: () => {
        const s = terms.get(active);
        if (seq === null) { ctrl = !ctrl; renderKeys(); return; }
        if (s) send(s, seq);
        s?.term.focus();
      },
    }, label)));
  };
  renderKeys();

  new ResizeObserver(() => fit(terms.get(active))).observe(screens);
  renderTabs();

  // open показывает уже запущенный сеанс (задачу): подключает и делает активным
  const open = async (info) => {
    try {
      await attach(info);
      activate(info.id);
    } catch (err) {
      toast(err.message || t('term.loadFailed'), { bad: true });
    }
  };

  return {
    show: () => { load(); fit(terms.get(active)); terms.get(active)?.term.focus(); },
    preload: load,
    newSession,
    open,
  };
}

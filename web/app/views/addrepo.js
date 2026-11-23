// Добавление репозитория: клонирование с GitHub через deploy-ключ (или публичный по HTTPS)
// и подключение папки, которая уже есть на сервере.
import { h, mount, dialog, toast } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, emit } from '../store.js';
import { go } from '../router.js';
import * as act from '../actions.js';
import { copyText } from './top.js';

const pendingClones = new Map(); // id сеанса -> папка назначения

// parseRepo повторяет разбор сервера настолько, чтобы предложить имя папки.
function repoName(url) {
  const m = /([A-Za-z0-9._-]+?)(?:\.git)?\/?$/.exec(url.trim().replace(/\/(tree|blob)\/.*$/, ''));
  return m ? m[1] : '';
}

function defaultRoot() {
  return state.settings?.default_folder || state.me?.roots?.[0] || '';
}

export function addRepoDialog(mode = 'github') {
  const body = h('div', { class: 'form' });
  let d = null;
  let current = mode;

  const tabs = () => h('div', { class: 'seg', role: 'tablist', style: { alignSelf: 'flex-start' } },
    h('button', { class: current === 'github' ? 'on' : null, role: 'tab', onclick: () => { current = 'github'; githubStep(); } }, t('add.github')),
    h('button', { class: current === 'local' ? 'on' : null, role: 'tab', onclick: () => { current = 'local'; localStep(); } }, t('add.local')));

  // шаг 1: адрес на GitHub и папка на сервере
  const githubStep = (prev = {}) => {
    const url = h('input', { type: 'text', value: prev.url || '', placeholder: t('add.urlPh'), 'aria-label': t('add.url'), spellcheck: false });
    const dest = h('input', { type: 'text', value: prev.dest || '', 'aria-label': t('add.dest'), spellcheck: false });
    const pub = h('input', { type: 'checkbox', checked: !!prev.public });
    let destTouched = !!prev.dest;
    dest.addEventListener('input', () => { destTouched = true; });
    url.addEventListener('input', () => {
      if (!destTouched) dest.value = repoName(url.value) ? defaultRoot().replace(/\/$/, '') + '/' + repoName(url.value) : '';
    });
    const next = async () => {
      const v = { url: url.value.trim(), dest: dest.value.trim(), public: pub.checked };
      if (!v.url || !v.dest) return toast(t('add.needUrlDest'), { bad: true });
      if (v.public) return startClone({ url: v.url, public: true, dest: v.dest });
      try {
        const key = await api.post('/api/github/key', { url: v.url });
        keyStep(v, key);
      } catch (err) {
        toast(err.message, { bad: true });
      }
    };
    url.addEventListener('keydown', (e) => { if (e.key === 'Enter') next(); });
    mount(body, tabs(),
      h('label', { class: 'form-row' }, h('span', { class: 'muted' }, t('add.url')), url),
      h('label', { class: 'form-row' }, h('span', { class: 'muted' }, t('add.dest')), dest,
        h('span', { class: 'muted', style: { fontSize: '12px' } }, t('add.destHint'))),
      h('label', {}, pub, t('add.public')),
      h('div', { class: 'field' }, h('span', { class: 'grow' }),
        h('button', { class: 'btn', onclick: () => d.close() }, t('common.cancel')),
        h('button', { class: 'btn primary', onclick: next }, t('add.next'))));
    url.focus();
  };

  // шаг 2: ключ, ссылка на настройки репозитория, проверка доступа
  const keyStep = (v, key) => {
    const status = h('div', { class: 'check-status', 'aria-live': 'polite' });
    const cloneBtn = h('button', { class: 'btn primary', disabled: !key.transport, onclick: () => startClone({ key_id: key.key_id, dest: v.dest }) }, t('add.clone'));
    const check = async () => {
      mount(status, h('span', { class: 'muted' }, t('add.checking')));
      try {
        const res = await api.post('/api/github/check', { key_id: key.key_id });
        if (res.ok) {
          mount(status, h('span', { class: 'ok' }, t('add.checkOk', { via: res.transport === 'ssh443' ? '443' : '22' })));
          cloneBtn.disabled = false;
          cloneBtn.focus();
        } else {
          mount(status, h('div', { class: 'bad' }, t('add.fail.' + (res.code || 'other'))),
            res.output ? h('pre', { class: 'check-out' }, res.output) : null);
        }
      } catch (err) {
        mount(status, h('div', { class: 'bad' }, err.message));
      }
    };
    const pubKey = h('textarea', { class: 'mono key-box', readonly: true, rows: 3, 'aria-label': t('add.publicKey') });
    pubKey.value = key.public_key;
    mount(body,
      h('p', {}, t('add.keyIntro', { repo: key.owner + '/' + key.repo })),
      h('div', { class: 'form-row' }, h('b', {}, t('add.step1')), pubKey,
        h('div', { class: 'field' }, h('button', { class: 'btn small', onclick: () => { pubKey.select(); copyText(key.public_key); } }, t('add.copyKey')))),
      h('div', { class: 'form-row' }, h('b', {}, t('add.step2')),
        h('div', { class: 'field' }, h('a', { class: 'btn', href: key.keys_url, target: '_blank', rel: 'noopener noreferrer' }, t('add.openGitHub'))),
        h('ul', { class: 'hint-list' }, h('li', {}, t('add.hintTitle', { title: key.title })), h('li', {}, t('add.hintKey')), h('li', {}, t('add.hintWrite')))),
      h('div', { class: 'form-row' }, h('b', {}, t('add.step3')),
        h('div', { class: 'field' }, h('button', { class: 'btn', onclick: check }, t('add.check'))), status),
      h('div', { class: 'field' },
        h('button', { class: 'btn', onclick: () => githubStep(v) }, t('add.back')), h('span', { class: 'grow' }),
        h('button', { class: 'btn', onclick: () => d.close() }, t('common.cancel')), cloneBtn));
    if (key.transport) mount(status, h('span', { class: 'ok' }, t('add.checkedBefore')));
  };

  const startClone = async (req) => {
    try {
      const res = await api.post('/api/github/clone', { ...req, cols: 120, rows: 30 });
      pendingClones.set(res.session.id, res.dest);
      d.close();
      if (innerWidth < 900) { state.pane = 'out'; emit('pane'); }
      await state.dock?.openSession(res.session);
    } catch (err) {
      toast({ exists: t('add.destExists'), not_checked: t('add.notChecked'), outside_roots: t('repo.outside') }[err.code] || err.message, { bad: true });
    }
  };

  // папка, которая уже есть на сервере
  const localStep = () => {
    const path = h('input', { type: 'text', value: defaultRoot() ? defaultRoot().replace(/\/$/, '') + '/' : '', 'aria-label': t('add.path'), spellcheck: false });
    const add = async () => {
      try {
        await act.addRepo(path.value.trim());
        d.close();
      } catch (err) {
        toast({ duplicate: t('repo.duplicate'), outside_roots: t('repo.outside') }[err.code] || err.message, { bad: true });
      }
    };
    path.addEventListener('keydown', (e) => { if (e.key === 'Enter') add(); });
    mount(body, tabs(),
      h('label', { class: 'form-row' }, h('span', { class: 'muted' }, t('add.path')), path,
        h('span', { class: 'muted', style: { fontSize: '12px' } }, t('repo.addHint', { roots: (state.me?.roots || []).join(', ') }))),
      h('div', { class: 'field' }, h('span', { class: 'grow' }),
        h('button', { class: 'btn', onclick: () => d.close() }, t('common.cancel')),
        h('button', { class: 'btn primary', onclick: add }, t('add.add'))));
    path.focus();
  };

  d = dialog({ title: t('add.title'), body, mid: true });
  if (mode === 'local') localStep();
  else githubStep();
}

// cloneFinished вызывается терминалом, когда клонирование завершилось на глазах у пользователя.
// Сервер регистрирует репозиторий сразу после выхода git: ждём его появления в списке.
export async function cloneFinished(info) {
  const dest = pendingClones.get(info.id);
  pendingClones.delete(info.id);
  if (info.exit_code !== 0) return toast(t('add.cloneFailed', { code: info.exit_code }), { bad: true });
  for (let i = 0; i < 20; i++) {
    await act.loadRepos().catch(() => {});
    const repo = state.repos.find((r) => r.path === dest) || (!dest && state.repos[state.repos.length - 1]);
    if (repo) {
      toast(t('add.cloned', { name: repo.path.split('/').pop() }));
      return go({ repo: repo.id });
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  return null;
}

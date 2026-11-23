// Экран входа. В режиме token пароль это токен из вывода при запуске сервера;
// в режиме PAM это обычный пароль учётной записи на сервере.
import { h, mount } from '../ui.js';
import { t, lang, languages, setLang } from '../i18n.js';
import { api } from '../api.js';

// langSwitch: RU | EN, выбранный язык выделен; смена перезагружает страницу.
export function langSwitch() {
  return h('div', { class: 'seg', role: 'group', 'aria-label': t('lang.title') }, languages.map((l) => h('button', {
    type: 'button', class: l === lang ? 'on' : null, 'aria-pressed': String(l === lang), title: t('lang.' + l),
    onclick: () => setLang(l),
  }, l.toUpperCase())));
}

export function loginView(el, onDone) {
  const hint = h('p', { class: 'muted' }, t('auth.hintToken'));
  api.get('/api/login').then((r) => { hint.textContent = r?.mode === 'pam' ? t('auth.hintPam') : t('auth.hintToken'); }, () => {});
  const user = h('input', { type: 'text', autocomplete: 'username', autocapitalize: 'off', spellcheck: false });
  const pass = h('input', { type: 'password', autocomplete: 'current-password' });
  const err = h('div', { class: 'err', role: 'alert' });
  const btn = h('button', { class: 'btn primary', type: 'submit' }, t('auth.login'));
  const form = h('form', {
    class: 'login',
    onsubmit: async (e) => {
      e.preventDefault();
      btn.disabled = true;
      err.textContent = '';
      try {
        const res = await api.post('/api/login', { username: user.value.trim(), password: pass.value });
        pass.value = '';
        onDone(res);
      } catch (ex) {
        err.textContent = { 401: t('auth.invalid'), 429: t('auth.tooMany'), 503: t('auth.unavailable') }[ex.status] || ex.message;
        btn.disabled = false;
        pass.focus();
      }
    },
  },
  h('div', { class: 'field' }, h('h1', { class: 'grow' }, 'gitaway'), langSwitch()),
  hint,
  h('label', {}, t('auth.user'), user),
  h('label', {}, t('auth.password'), pass),
  err, btn);
  mount(el, form);
  user.focus();
}

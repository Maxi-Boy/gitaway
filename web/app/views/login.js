// Экран входа. В режиме token пароль это токен из вывода при запуске сервера;
// в режиме PAM это обычный пароль учётной записи на сервере.
import { h, mount } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';

export function loginView(el, onDone) {
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
        err.textContent = ex.status === 401 ? t('auth.invalid') : ex.status === 429 ? t('auth.tooMany') : ex.message;
        btn.disabled = false;
        pass.focus();
      }
    },
  },
  h('h1', {}, 'gitaway'),
  h('p', { class: 'muted' }, t('auth.hint')),
  h('label', {}, t('auth.user'), user),
  h('label', {}, t('auth.password'), pass),
  err, btn);
  mount(el, form);
  user.focus();
}

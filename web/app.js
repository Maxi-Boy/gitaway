'use strict';
// Временная страница: вход, список репозиториев, ветки и кнопки Fetch/Pull/Push.
// Нужна только чтобы проверить API; настоящий интерфейс будет по макету.

const app = document.getElementById('app');
let csrf = '';

async function api(path, { method = 'GET', body } = {}) {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (csrf) headers['X-CSRF-Token'] = csrf;
  const resp = await fetch(path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const err = new Error((data.error && data.error.message) || resp.statusText);
    err.status = resp.status;
    throw err;
  }
  return data;
}

// h создаёт элемент без innerHTML, чтобы данные сервера не могли превратиться в разметку.
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else el.setAttribute(k, v);
  }
  for (const kid of kids) el.append(kid);
  return el;
}

function show(...nodes) {
  app.replaceChildren(...nodes);
}

function loginView(message) {
  const pass = h('input', { type: 'password', id: 'pw', autocomplete: 'current-password', required: '' });
  const err = h('p', { class: 'err', role: 'alert' }, message || '');
  const form = h('form', {
    onsubmit: async (e) => {
      e.preventDefault();
      try {
        const r = await api('/api/login', { method: 'POST', body: { username: '', password: pass.value } });
        csrf = r.csrf;
        await reposView();
      } catch (ex) {
        err.textContent = ex.message;
      }
    },
  }, h('label', { for: 'pw' }, 'Токен или пароль'), pass, h('button', { type: 'submit' }, 'Войти'));
  show(h('h1', {}, 'gitaway'), form, err);
}

async function logout() {
  try { await api('/api/logout', { method: 'POST', body: {} }); } catch (_) { /* уже вышли */ }
  csrf = '';
  loginView();
}

async function reposView() {
  const repos = await api('/api/repos');
  const list = h('ul', {}, ...repos.map((r) =>
    h('li', {}, h('button', { onclick: () => repoView(r) }, r.path))));
  const path = h('input', { type: 'text', placeholder: '/путь/к/репозиторию', 'aria-label': 'Путь к репозиторию' });
  const msg = h('p', { class: 'err', role: 'alert' }, '');
  const add = h('form', {
    onsubmit: async (e) => {
      e.preventDefault();
      try {
        await api('/api/repos', { method: 'POST', body: { path: path.value } });
        await reposView();
      } catch (ex) {
        msg.textContent = ex.message;
      }
    },
  }, path, h('button', { type: 'submit' }, 'Добавить'));
  show(
    h('h1', {}, 'Репозитории'),
    repos.length ? list : h('p', {}, 'Пока нет репозиториев.'),
    add, msg,
    h('button', { onclick: logout }, 'Выйти'),
  );
}

async function repoView(repo) {
  const out = h('pre', { class: 'out' }, '');
  const statusEl = h('p', {});
  const branchesEl = h('ul', {});
  const q = encodeURIComponent(repo.id);

  async function refresh() {
    const [st, bs] = await Promise.all([api('/api/status?repo=' + q), api('/api/branches?repo=' + q)]);
    statusEl.textContent = 'Ветка ' + (st.branch || '(detached)') +
      '  ↑' + st.ahead + ' ↓' + st.behind + '  изменений: ' + st.changes.length;
    branchesEl.replaceChildren(...bs.filter((b) => !b.remote).map((b) =>
      h('li', {}, (b.current ? '● ' : '') + b.name + '  ↑' + b.ahead + ' ↓' + b.behind)));
  }

  async function run(op) {
    out.textContent = '…';
    try {
      const r = await api('/api/git/' + op, { method: 'POST', body: { repo: repo.id } });
      out.textContent = r.output || 'готово';
      await refresh();
    } catch (ex) {
      out.textContent = ex.message;
    }
  }

  show(
    h('h1', {}, repo.path),
    statusEl,
    h('div', { class: 'row' },
      h('button', { onclick: () => run('fetch') }, 'Fetch'),
      h('button', { onclick: () => run('pull') }, 'Pull'),
      h('button', { onclick: () => run('push') }, 'Push')),
    branchesEl,
    out,
    h('button', { onclick: () => reposView() }, 'Назад'),
  );
  refresh().catch((ex) => { out.textContent = ex.message; });
}

(async () => {
  try {
    const me = await api('/api/me');
    csrf = me.csrf;
    await reposView();
  } catch (_) {
    loginView();
  }
})();

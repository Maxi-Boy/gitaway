// Клиент API: CSRF-токен, единый формат ошибок, повтор GET при обрыве связи
// и события состояния соединения.

let csrf = '';

export function setCSRF(token) {
  csrf = token || '';
}

// events: 'online' и 'offline' (связь с сервером), 'unauthorized' (сессия истекла).
export const events = new EventTarget();
let online = true;

function setOnline(v) {
  if (v === online) return;
  online = v;
  events.dispatchEvent(new Event(v ? 'online' : 'offline'));
}

export function isOnline() {
  return online;
}

// ApiError это ошибка от сервера: status, code из {"error":{"code","message"}} и всё тело.
export class ApiError extends Error {
  constructor(status, code, message, data) {
    super(message);
    this.status = status;
    this.code = code;
    this.data = data;
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function url(path, params) {
  if (!params) return path;
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v != null && v !== '' && v !== false) q.set(k, v === true ? '1' : String(v));
  }
  const s = q.toString();
  return s ? `${path}?${s}` : path;
}

async function request(method, path, { params, body } = {}) {
  const safe = method === 'GET';
  const opts = { method, headers: {}, credentials: 'same-origin' };
  if (!safe) opts.headers['X-CSRF-Token'] = csrf;
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  // GET безопасно повторить; изменяющий запрос не повторяем: он мог дойти до сервера.
  const attempts = safe ? 3 : 1;
  let res;
  for (let i = 0; ; i++) {
    try {
      res = await fetch(url(path, params), opts);
      break;
    } catch (err) {
      if (i + 1 >= attempts) {
        setOnline(false);
        throw new ApiError(0, 'network', err.message || 'network error');
      }
      await sleep(500 * 2 ** i);
    }
  }
  setOnline(true);
  let data = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const e = data?.error || {};
    if (res.status === 401 && path !== '/api/login') events.dispatchEvent(new Event('unauthorized'));
    throw new ApiError(res.status, e.code || 'http_' + res.status, e.message || res.statusText, data);
  }
  return data;
}

export const api = {
  get: (path, params) => request('GET', path, { params }),
  post: (path, body = {}) => request('POST', path, { body }),
  put: (path, body, params) => request('PUT', path, { body, params }),
  del: (path, params) => request('DELETE', path, { params }),
};

// Проверка связи раз в 20 секунд, пока сервер недоступен.
setInterval(() => {
  if (!online) fetch('/healthz', { cache: 'no-store' }).then((r) => setOnline(r.ok), () => {});
}, 20000);

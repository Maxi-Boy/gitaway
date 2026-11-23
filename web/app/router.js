// Маршрут в адресе: #/repo/<id>/wt/<ветка>/tree/<папка>, .../file/<путь>:<строка>,
// .../changes/<выбранный файл>, .../search, .../history[/<коммит>][/path/<путь>].
// Ветка кодируется целиком (в имени бывает "/"), путь кодируется по сегментам.
// Перезагрузка страницы, «Назад» и сохранённые ссылки восстанавливают экран из адреса.

const enc = (s) => encodeURIComponent(s);
const encPath = (p) => p.split('/').map(enc).join('/');

export function parse(hash = location.hash) {
  const r = { repo: '', wt: '', view: 'tree', path: '', line: 0, commit: '' };
  const parts = hash.replace(/^#\/?/, '').split('/');
  let i = 0;
  const next = () => parts[i++];
  const dec = (s) => {
    try {
      return decodeURIComponent(s);
    } catch {
      return '';
    }
  };
  while (i < parts.length) {
    const key = next();
    if (key === 'repo') r.repo = dec(next() || '');
    else if (key === 'wt') r.wt = dec(next() || '');
    else if (key === 'tree' || key === 'file') {
      r.view = key;
      let rest = parts.slice(i);
      i = parts.length;
      if (key === 'file' && rest.length) {
        const m = /^(.*):(\d+)$/.exec(rest[rest.length - 1]);
        if (m) {
          rest = [...rest.slice(0, -1), m[1]];
          r.line = Number(m[2]);
        }
      }
      r.path = rest.filter(Boolean).map(dec).join('/');
    } else if (key === 'search') {
      r.view = 'search';
    } else if (key === 'history') {
      r.view = 'history';
      if (/^[0-9a-f]{4,64}$/.test(parts[i] || '')) r.commit = next();
      if (parts[i] === 'path') {
        r.path = parts.slice(i + 1).filter(Boolean).map(dec).join('/');
        i = parts.length;
      }
    } else if (key === 'changes') {
      r.view = 'changes'; // необязательный путь дальше: выбранный файл
      r.path = parts.slice(i).filter(Boolean).map(dec).join('/');
      i = parts.length;
    }
  }
  return r;
}

export function format(r) {
  let s = '#/';
  if (!r.repo) return s;
  s += 'repo/' + enc(r.repo);
  if (r.wt) s += '/wt/' + enc(r.wt);
  if (r.view === 'search') return s + '/search';
  if (r.view === 'history') return s + '/history' + (r.commit ? '/' + r.commit : '') + (r.path ? '/path/' + encPath(r.path) : '');
  if (r.view === 'changes') return s + '/changes' + (r.path ? '/' + encPath(r.path) : '');
  if (r.view === 'file' && r.path) return s + '/file/' + encPath(r.path) + (r.line ? ':' + r.line : '');
  if (r.path) s += '/tree/' + encPath(r.path);
  return s;
}

// go меняет адрес; replace не добавляет шаг в историю «Назад».
export function go(r, replace = false) {
  const h = format(r);
  if (h === location.hash) return;
  if (replace) history.replaceState(null, '', h);
  else history.pushState(null, '', h);
  dispatchEvent(new HashChangeEvent('hashchange'));
}

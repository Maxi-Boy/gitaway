// Тексты интерфейса. Все строки лежат в i18n/<язык>.js; здесь только поиск и подстановка.
import ru from './i18n/ru.js';

const dict = ru;

// t('git.pull', {n: 2}) берёт строку по ключу и подставляет {n}.
// Неизвестный ключ возвращается как есть, чтобы пропуск был виден сразу.
export function t(key, vars) {
  let s = dict[key] ?? key;
  if (vars) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
  return s;
}

// phrases('cm.') возвращает тексты с этим префиксом без префикса: для библиотек,
// которые принимают свой словарь (CodeMirror: панель поиска, переход к строке).
export function phrases(prefix) {
  const out = {};
  for (const [k, v] of Object.entries(dict)) if (k.startsWith(prefix)) out[k.slice(prefix.length)] = v;
  return out;
}

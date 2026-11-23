// Тексты интерфейса: i18n/ru.js и i18n/en.js с одинаковыми ключами. Язык: выбранный
// пользователем (хранится в браузере), иначе по языку браузера (русский для ru, kk, be, uk).
import ru from './i18n/ru.js';
import en from './i18n/en.js';

const DICTS = { ru, en };
const KEY = 'gitaway:lang';

function detect() {
  try {
    const saved = localStorage.getItem(KEY);
    if (DICTS[saved]) return saved;
  } catch { /* хранилище недоступно */ }
  const langs = navigator.languages?.length ? navigator.languages : [navigator.language || 'en'];
  return langs.some((l) => /^(ru|kk|be|uk)\b/i.test(l)) ? 'ru' : 'en';
}

// lang это текущий язык ('ru' или 'en'): им же форматируются даты и числа.
export const lang = detect();
const dict = DICTS[lang];
document.documentElement.lang = lang;

// setLang запоминает язык и перезагружает страницу: все экраны строятся заново.
export function setLang(next) {
  if (!DICTS[next] || next === lang) return;
  try {
    localStorage.setItem(KEY, next);
  } catch { /* не запомнится, но смена сработает до перезагрузки */ }
  location.reload();
}

export const languages = Object.keys(DICTS);

// t('git.pull', {n: 2}) берёт строку по ключу и подставляет {n}. Если в словаре языка
// ключа нет, берётся русский текст; неизвестный ключ возвращается как есть.
export function t(key, vars) {
  let s = dict[key] ?? ru[key] ?? key;
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

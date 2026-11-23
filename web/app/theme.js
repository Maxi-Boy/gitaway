// Тема интерфейса: «как в системе», светлая или тёмная. Цвета задаёт style.css по атрибуту
// data-theme у <html>; редактор и терминал берут свои цвета отсюда и при смене обновляются
// без перезагрузки (событие gitaway:theme).
const KEY = 'gitaway:theme';
const media = matchMedia('(prefers-color-scheme: light)');

export const themes = ['auto', 'light', 'dark'];

function saved() {
  try {
    const v = localStorage.getItem(KEY);
    return themes.includes(v) ? v : 'auto';
  } catch {
    return 'auto';
  }
}

let mode = saved();

// themeMode это выбор пользователя; appliedTheme это то, что сейчас на экране.
export const themeMode = () => mode;
export const appliedTheme = () => document.documentElement.dataset.theme === 'light' ? 'light' : 'dark';

function apply() {
  const next = mode === 'light' || (mode === 'auto' && media.matches) ? 'light' : 'dark';
  if (document.documentElement.dataset.theme === next) return;
  document.documentElement.dataset.theme = next;
  dispatchEvent(new CustomEvent('gitaway:theme', { detail: next }));
}

export function setTheme(next) {
  if (!themes.includes(next)) return;
  mode = next;
  try {
    localStorage.setItem(KEY, next);
  } catch { /* не запомнится, но сработает до перезагрузки */ }
  apply();
}

media.addEventListener('change', () => { if (mode === 'auto') apply(); });
apply();

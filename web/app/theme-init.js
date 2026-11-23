// Ставит тему до отрисовки страницы (обычный скрипт в <head>, без мигания тёмной темы).
// Выбор хранится в браузере: auto (как в системе), light или dark. Логика смены в theme.js.
(function () {
  var mode = 'auto';
  try { mode = localStorage.getItem('gitaway:theme') || 'auto'; } catch (e) { /* нет хранилища */ }
  var light = mode === 'light' || (mode !== 'dark' && matchMedia('(prefers-color-scheme: light)').matches);
  document.documentElement.dataset.theme = light ? 'light' : 'dark';
})();

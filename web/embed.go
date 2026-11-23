// Package web содержит статический интерфейс, вшитый в бинарник.
package web

import "embed"

// FS это файлы интерфейса: index.html, ES-модули в app/ (без шага сборки) и собранные
// библиотеки в vendor/ (CodeMirror, xterm.js; см. tools/vendor).
//
//go:embed index.html app vendor
var FS embed.FS

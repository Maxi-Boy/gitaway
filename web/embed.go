// Package web содержит статический интерфейс, вшитый в бинарник.
package web

import "embed"

// FS это файлы интерфейса: index.html и ES-модули в app/ (без шага сборки).
//
//go:embed index.html app
var FS embed.FS

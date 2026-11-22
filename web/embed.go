// Package web содержит статический интерфейс, вшитый в бинарник.
package web

import "embed"

// FS это файлы интерфейса. Сейчас это временная страница для проверки API;
// настоящий интерфейс будет построен по макету.
//
//go:embed index.html app.js
var FS embed.FS

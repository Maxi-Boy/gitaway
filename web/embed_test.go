package web

import (
	"io/fs"
	"strings"
	"testing"
)

// Интерфейс вшивается в бинарник: страница, стили и все модули на месте.
func TestEmbeddedFiles(t *testing.T) {
	for _, name := range []string{"index.html", "app/main.js", "app/style.css", "app/i18n/ru.js", "app/views/center.js",
		"vendor/codemirror.js", "vendor/xterm.js", "vendor/xterm.css", "vendor/LICENSES.txt"} {
		if _, err := fs.Stat(FS, name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	page, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	// CSP сервера разрешает только свои скрипты: никаких внешних адресов и встроенных скриптов
	if strings.Contains(string(page), "http://") || strings.Contains(string(page), "https://") {
		t.Error("index.html must not load anything from other origins")
	}
}

package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStatic(t *testing.T) {
	big := strings.Repeat("export const x = 1;\n", 500)
	h := newStatic(fstest.MapFS{
		"index.html":     {Data: []byte("<!doctype html><title>gitaway</title>")},
		"app/main.js":    {Data: []byte(big)},
		"vendor/img.png": {Data: bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 600)},
	})
	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// корень отдаёт index.html; маленький файл не сжимается
	w := get("/", map[string]string{"Accept-Encoding": "gzip"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gitaway") || w.Header().Get("Content-Encoding") != "" {
		t.Errorf("index: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("index content type: %q", ct)
	}

	// большой js сжимается, распакованный совпадает с исходником
	w = get("/app/main.js", map[string]string{"Accept-Encoding": "deflate, gzip;q=0.8"})
	if w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() >= len(big)/3 {
		t.Fatalf("js not compressed: %v, %d bytes", w.Header(), w.Body.Len())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != big {
		t.Error("decompressed body differs")
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "javascript") || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("headers: %v", w.Header())
	}
	etag := w.Header().Get("ETag")

	// без gzip в Accept-Encoding или с q=0: исходник
	for _, ae := range []string{"", "gzip;q=0", "br"} {
		if w := get("/app/main.js", map[string]string{"Accept-Encoding": ae}); w.Header().Get("Content-Encoding") != "" || w.Body.String() != big {
			t.Errorf("Accept-Encoding %q: got encoding %q", ae, w.Header().Get("Content-Encoding"))
		}
	}

	// повторный запрос с ETag: 304 без тела
	if w := get("/app/main.js", map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("etag: %d, %d bytes", w.Code, w.Body.Len())
	}

	// двоичные файлы не сжимаются
	if w := get("/vendor/img.png", map[string]string{"Accept-Encoding": "gzip"}); w.Header().Get("Content-Encoding") != "" {
		t.Error("png must not be gzipped")
	}

	for _, p := range []string{"/nope.js", "/app", "/../etc/passwd", "/app/../../x"} {
		if w := get(p, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/app/main.js", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

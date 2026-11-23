package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// staticFile это файл интерфейса, подготовленный к отдаче: исходник, сжатая копия и ETag.
type staticFile struct {
	data  []byte
	gz    []byte // nil, если сжимать не стоит
	etag  string
	ctype string
}

// staticHandler отдаёт вшитый интерфейс: gzip для текстовых файлов (CodeMirror около 500 КБ
// сжимается примерно в 3 раза), ETag и 304 вместо повторной загрузки. Файлы готовятся
// при первом запросе и держатся в памяти: их немного и они не меняются, пока работает процесс.
type staticHandler struct {
	fsys  fs.FS
	mu    sync.Mutex
	files map[string]*staticFile
}

var compressible = map[string]bool{".html": true, ".js": true, ".css": true, ".txt": true, ".svg": true, ".json": true}

func newStatic(fsys fs.FS) *staticHandler {
	return &staticHandler{fsys: fsys, files: map[string]*staticFile{}}
}

func (h *staticHandler) get(name string) (*staticFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f, ok := h.files[name]; ok {
		return f, nil
	}
	data, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	f := &staticFile{
		data:  data,
		etag:  `"` + base64.RawURLEncoding.EncodeToString(sum[:12]) + `"`,
		ctype: mime.TypeByExtension(path.Ext(name)),
	}
	if f.ctype == "" {
		f.ctype = http.DetectContentType(data)
	}
	if compressible[path.Ext(name)] && len(data) > 1024 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(data)
		if zw.Close() == nil && buf.Len() < len(data) {
			f.gz = buf.Bytes()
		}
	}
	h.files[name] = f
	return f, nil
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(enc) == "gzip" && strings.ReplaceAll(strings.TrimSpace(q), " ", "") != "q=0" {
			return true
		}
	}
	return false
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	f, err := h.get(name)
	if err != nil { // нет файла, каталог или неверное имя: наружу просто 404
		http.NotFound(w, r)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", f.ctype)
	hd.Set("ETag", f.etag)
	hd.Set("Cache-Control", "no-cache") // браузер хранит копию, но сверяется по ETag (ответ 304)
	hd.Set("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.data
	if f.gz != nil && acceptsGzip(r) {
		hd.Set("Content-Encoding", "gzip")
		body = f.gz
	}
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

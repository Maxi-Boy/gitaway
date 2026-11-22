// Прототип 2: терминал в браузере (pty + WebSocket + xterm.js).
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"syscall"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

//go:embed index.html
var indexHTML []byte

type server struct {
	token string
	dir   string
}

func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	got := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Accept по умолчанию проверяет, что Origin совпадает с Host (защита от CSWSH).
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Println("accept:", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "closing")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell)
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd) // pty.Start создаёт новую сессию (Setsid), pid == pgid
	if err != nil {
		log.Println("pty:", err)
		return
	}
	defer func() {
		_ = ptmx.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP) // как при закрытии терминала
		_ = cmd.Wait()
	}()

	go func() { // pty -> браузер
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for { // браузер -> pty
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := ptmx.Write(data); err != nil {
				return
			}
			continue
		}
		var m struct {
			Type string `json:"type"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &m) == nil && m.Type == "resize" && m.Cols > 0 && m.Rows > 0 {
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: m.Cols, Rows: m.Rows})
		}
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8450", "адрес и порт")
	dir := flag.String("dir", ".", "рабочий каталог оболочки")
	flag.Parse()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	s := &server{token: hex.EncodeToString(b), dir: *dir}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("/ws", s.handleWS)

	fmt.Printf("открой: http://%s/?token=%s\n", *addr, s.token)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

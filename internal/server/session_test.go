package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type sessionInfo struct {
	ID       string `json:"id"`
	Dir      string `json:"dir"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exit_code"`
}

func (e *testEnv) dialSession(t *testing.T, ctx context.Context, id, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	u := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/api/sessions/ws?id=" + id
	return websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: e.c, HTTPHeader: h})
}

func TestTerminalSession(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh") // предсказуемая оболочка вместо оболочки раннера
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)

	code, data := e.do(t, "POST", "/api/sessions", map[string]any{"repo": id, "cols": 100, "rows": 30}, true)
	if code != http.StatusCreated {
		t.Fatalf("start: %d %s", code, data)
	}
	var info sessionInfo
	if err := json.Unmarshal(data, &info); err != nil || info.ID == "" || info.Dir != dir || !info.Running {
		t.Fatalf("info: %s", data)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := e.dialSession(t, ctx, info.ID, e.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":90,"rows":20}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("echo gitaway-$((40+2)); pwd\n")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for !strings.Contains(out.String(), "gitaway-42") || !strings.Contains(out.String(), dir) {
		typ, p, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v; got %q", err, out.String())
		}
		if typ == websocket.MessageBinary {
			out.Write(p)
		}
	}

	// второе подключение получает накопленный вывод
	c2, _, err := e.dialSession(t, ctx, info.ID, e.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := c2.Read(ctx)
	if err != nil || !strings.Contains(string(p), "gitaway-42") {
		t.Errorf("snapshot on reconnect: %q %v", p, err)
	}
	c2.CloseNow()

	if err := c.Write(ctx, websocket.MessageBinary, []byte("exit 5\n")); err != nil {
		t.Fatal(err)
	}
	for {
		typ, p, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("no exit message: %v", err)
		}
		if typ == websocket.MessageText {
			var m struct {
				Type string `json:"type"`
				Code int    `json:"code"`
			}
			if json.Unmarshal(p, &m) != nil || m.Type != "exit" || m.Code != 5 {
				t.Errorf("exit message: %s", p)
			}
			break
		}
	}

	code, data = e.do(t, "GET", "/api/sessions", nil, false)
	var list []sessionInfo
	if code != http.StatusOK || json.Unmarshal(data, &list) != nil || len(list) != 1 || list[0].Running || list[0].ExitCode != 5 {
		t.Errorf("list: %d %s", code, data)
	}
	if code, _ := e.do(t, "DELETE", "/api/sessions?id="+info.ID, nil, true); code != http.StatusOK {
		t.Errorf("delete: %d", code)
	}
	if code, _ := e.do(t, "DELETE", "/api/sessions?id="+info.ID, nil, true); code != http.StatusNotFound {
		t.Errorf("second delete: %d", code)
	}
}

func TestTerminalRejects(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	e := newEnv(t)
	e.login(t)
	code, data := e.do(t, "POST", "/api/sessions", map[string]any{"dir": e.root}, true)
	if code != http.StatusCreated {
		t.Fatalf("server folder terminal: %d %s", code, data)
	}
	var info sessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	defer e.do(t, "DELETE", "/api/sessions?id="+info.ID, nil, true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, origin := range []string{"", "http://evil.example"} {
		if _, resp, err := e.dialSession(t, ctx, info.ID, origin); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q must be rejected: %v", origin, err)
		}
	}
	if _, resp, err := e.dialSession(t, ctx, "nope", e.ts.URL); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session: %v", err)
	}

	bad := []struct {
		name string
		body map[string]any
		want int
	}{
		{"outside roots", map[string]any{"dir": t.TempDir()}, http.StatusForbidden},
		{"unknown repo", map[string]any{"repo": "nope"}, http.StatusNotFound},
		{"unknown field", map[string]any{"cmd": "rm -rf /"}, http.StatusBadRequest},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "POST", "/api/sessions", c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
	if code, _ := e.do(t, "POST", "/api/sessions", map[string]any{"dir": e.root}, false); code != http.StatusForbidden {
		t.Errorf("without CSRF: %d", code)
	}
}

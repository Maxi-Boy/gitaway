package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// readUntil читает живой вывод, пока в нём не появится want.
func readUntil(t *testing.T, got *bytes.Buffer, ch <-chan []byte, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !strings.Contains(got.String(), want) {
		select {
		case p, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed before %q; got %q", want, got.String())
			}
			got.Write(p)
		case <-deadline:
			t.Fatalf("timeout waiting for %q; got %q", want, got.String())
		}
	}
}

func TestSessionIO(t *testing.T) {
	m := NewManager()
	dir := t.TempDir()
	s, err := m.Start(Options{Title: "t", Dir: dir, Args: []string{"/bin/sh", "-c", `echo "ready in $(pwd)"; read x; echo "got:$x"; exit 3`}})
	if err != nil {
		t.Fatal(err)
	}
	snap, ch, detach := s.Attach()
	defer detach()
	got := bytes.NewBuffer(snap)
	readUntil(t, got, ch, "ready in "+dir)
	if err := s.Write([]byte("abc\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, got, ch, "got:abc")
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	if info := s.Info(); info.Running || info.ExitCode != 3 {
		t.Errorf("info after exit: %+v", info)
	}

	// переподключение после выхода: весь вывод из буфера, канал сразу закрыт
	snap, ch, _ = s.Attach()
	if !strings.Contains(string(snap), "got:abc") {
		t.Errorf("snapshot: %q", snap)
	}
	if _, ok := <-ch; ok {
		t.Error("channel of a finished session must be closed")
	}
	if list := m.List(); len(list) != 1 || list[0].ID != s.Info().ID {
		t.Errorf("list: %+v", list)
	}
}

func TestBufferLimit(t *testing.T) {
	m := NewManager()
	m.Buffer = 1000
	s, err := m.Start(Options{Dir: t.TempDir(), Args: []string{"/bin/sh", "-c", "i=0; while [ $i -lt 300 ]; do echo line$i; i=$((i+1)); done"}})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	snap, _, _ := s.Attach()
	if len(snap) > 1000 || !strings.Contains(string(snap), "line299") || strings.Contains(string(snap), "line1\r\n") {
		t.Errorf("buffer must keep only the tail: %d bytes", len(snap))
	}
}

func TestCloseAndLimit(t *testing.T) {
	m := NewManager()
	m.Max = 1
	s, err := m.Start(Options{Dir: t.TempDir(), Args: []string{"/bin/sh", "-c", "sleep 100"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(Options{Dir: t.TempDir(), Args: []string{"/bin/sh", "-c", "true"}}); !errors.Is(err, ErrLimit) {
		t.Errorf("second session: %v, want ErrLimit", err)
	}
	start := time.Now()
	if err := m.Close(s.Info().ID); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second {
		t.Error("close took too long")
	}
	if _, err := m.Get(s.Info().ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("closed session still listed: %v", err)
	}
	if err := s.Resize(80, 24); err != nil {
		t.Errorf("resize after exit must be a no-op: %v", err)
	}
}

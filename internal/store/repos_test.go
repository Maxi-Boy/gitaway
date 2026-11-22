package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReposLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); got == nil || len(got) != 0 {
		t.Fatalf("empty list must be a non-nil empty slice, got %#v", got)
	}

	a, err := s.Add("/srv/a", "https://example.com/a.git")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("/srv/a", ""); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: got %v", err)
	}
	if _, err := s.Add("/srv/b", ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get(a.ID); !ok || got.Path != "/srv/a" {
		t.Errorf("get: %+v %v", got, ok)
	}

	// данные переживают повторное открытие
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) != 2 {
		t.Fatalf("reopened: %+v", s2.List())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}

	if err := s2.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s2.Remove(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: got %v", err)
	}
	if len(s2.List()) != 1 {
		t.Fatalf("after remove: %+v", s2.List())
	}
}

func TestOpenRejectsBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error")
	}
}

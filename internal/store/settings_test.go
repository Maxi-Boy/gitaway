package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsDefaultsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	defaults := DefaultSettings("/srv/export")

	s, err := OpenSettings(path, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got != defaults {
		t.Fatalf("missing file must give defaults, got %+v", got)
	}
	if !defaults.OpenOnStart || !defaults.AskOnOverwrite || defaults.MoveByDefault {
		t.Errorf("unexpected defaults: %+v", defaults)
	}

	want := Settings{DefaultFolder: "/srv/other", OpenOnStart: false, AskOnOverwrite: true, MoveByDefault: true}
	if err := s.Set(want); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got != want {
		t.Errorf("after Set: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}

	s2, err := OpenSettings(path, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get(); got != want {
		t.Errorf("reopened: %+v, want %+v", got, want)
	}
}

func TestSettingsMissingFieldsKeepDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"move_by_default": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSettings(path, DefaultSettings("/srv/export"))
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if !got.MoveByDefault || !got.OpenOnStart || !got.AskOnOverwrite || got.DefaultFolder != "/srv/export" {
		t.Errorf("got %+v", got)
	}
}

func TestSettingsBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSettings(path, DefaultSettings("")); err == nil {
		t.Fatal("expected error")
	}
}

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Settings это настройки, которые пользователь меняет в меню шестерёнки серверной панели:
// папка по умолчанию ("" значит первый разрешённый корень), открывать ли её при запуске,
// спрашивать ли при перезаписи файлов и перемещать ли файлы вместо копирования.
type Settings struct {
	DefaultFolder  string `json:"default_folder"`
	OpenOnStart    bool   `json:"open_on_start"`
	AskOnOverwrite bool   `json:"ask_on_overwrite"`
	MoveByDefault  bool   `json:"move_by_default"`
}

// DefaultSettings возвращает значения для первого запуска.
func DefaultSettings(defaultFolder string) Settings {
	return Settings{DefaultFolder: defaultFolder, OpenOnStart: true, AskOnOverwrite: true}
}

// SettingsStore хранит настройки в JSON-файле (права 0600, атомарная запись).
type SettingsStore struct {
	mu   sync.Mutex
	path string
	cur  Settings
}

// OpenSettings читает настройки. Если файла нет, действуют defaults; поля, которых нет
// в файле (например, добавленные в новой версии), тоже берутся из defaults.
func OpenSettings(path string, defaults Settings) (*SettingsStore, error) {
	s := &SettingsStore{path: path, cur: defaults}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cur); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Get возвращает текущие настройки.
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Set сохраняет настройки на диск и только после этого делает их текущими.
func (s *SettingsStore) Set(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSONFile(s.path, v); err != nil {
		return err
	}
	s.cur = v
	return nil
}

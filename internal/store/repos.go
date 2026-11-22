// Package store хранит список добавленных репозиториев в JSON-файле (права 0600).
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	ErrDuplicate = errors.New("repository already added")
	ErrNotFound  = errors.New("repository not found")
)

// Repo это запись о репозитории. Секретов здесь нет.
type Repo struct {
	ID     string    `json:"id"`
	Path   string    `json:"path"`
	Remote string    `json:"remote,omitempty"`
	KeyID  string    `json:"key_id,omitempty"` // зарезервировано под deploy-ключ
	Added  time.Time `json:"added"`
}

// Repos это реестр репозиториев.
type Repos struct {
	mu   sync.Mutex
	path string
	list []Repo
}

// Open читает реестр; если файла нет, начинает с пустого.
func Open(path string) (*Repos, error) {
	s := &Repos{path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (s *Repos) save() error {
	data, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List возвращает копию списка.
func (s *Repos) List() []Repo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Repo{}, s.list...)
}

// Get ищет репозиторий по идентификатору.
func (s *Repos) Get(id string) (Repo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.list {
		if r.ID == id {
			return r, true
		}
	}
	return Repo{}, false
}

// Add добавляет репозиторий; тот же путь второй раз добавить нельзя.
func (s *Repos) Add(path, remote string) (Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.list {
		if r.Path == path {
			return Repo{}, ErrDuplicate
		}
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return Repo{}, err
	}
	repo := Repo{ID: hex.EncodeToString(b), Path: path, Remote: remote, Added: time.Now().UTC()}
	s.list = append(s.list, repo)
	if err := s.save(); err != nil {
		s.list = s.list[:len(s.list)-1]
		return Repo{}, err
	}
	return repo, nil
}

// Remove убирает репозиторий из реестра (файлы на диске не трогаются).
func (s *Repos) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.list {
		if r.ID != id {
			continue
		}
		old := s.list
		s.list = append(append([]Repo{}, old[:i]...), old[i+1:]...)
		if err := s.save(); err != nil {
			s.list = old
			return err
		}
		return nil
	}
	return ErrNotFound
}

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Session это данные вошедшего пользователя. CSRF это токен для защиты от межсайтовых запросов.
type Session struct {
	User    string
	CSRF    string
	Expires time.Time
}

// Store хранит сессии в памяти (после перезапуска сервера нужен новый вход).
type Store struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	m   map[string]Session
}

func NewStore(ttl time.Duration) *Store {
	return &Store{ttl: ttl, now: time.Now, m: map[string]Session{}}
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create создаёт сессию и возвращает её идентификатор (значение cookie).
func (s *Store) Create(user string) (string, Session, error) {
	id, err := randHex(32)
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := randHex(16)
	if err != nil {
		return "", Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.m { // заодно чистим просроченные
		if now.After(v.Expires) {
			delete(s.m, k)
		}
	}
	sess := Session{User: user, CSRF: csrf, Expires: now.Add(s.ttl)}
	s.m[id] = sess
	return id, sess, nil
}

// Get возвращает действующую сессию.
func (s *Store) Get(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok {
		return Session{}, false
	}
	if s.now().After(sess.Expires) {
		delete(s.m, id)
		return Session{}, false
	}
	return sess, true
}

// Delete завершает сессию.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

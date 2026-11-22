package auth

import (
	"errors"
	"testing"
	"time"
)

func TestTokenAuth(t *testing.T) {
	ta, err := NewTokenAuth()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ta.Authenticate("anyone", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong token: got %v", err)
	}
	if _, err := ta.Authenticate("", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("empty token: got %v", err)
	}
	user, err := ta.Authenticate("anyone", ta.Token())
	if err != nil || user == "" {
		t.Errorf("right token: %q, %v", user, err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewStore(time.Hour)
	s.now = func() time.Time { return now }

	id, sess, err := s.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 64 || sess.CSRF == "" {
		t.Fatalf("id=%q csrf=%q", id, sess.CSRF)
	}
	got, ok := s.Get(id)
	if !ok || got.User != "alice" || got.CSRF != sess.CSRF {
		t.Fatalf("get: %+v %v", got, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("unknown id must not be found")
	}

	now = now.Add(2 * time.Hour) // сессия просрочена
	if _, ok := s.Get(id); ok {
		t.Error("expired session must not be found")
	}

	id2, _, _ := s.Create("bob")
	s.Delete(id2)
	if _, ok := s.Get(id2); ok {
		t.Error("deleted session must not be found")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !l.Allow("ip") {
			t.Fatalf("attempt %d must be allowed", i)
		}
		l.Fail("ip")
	}
	if l.Allow("ip") {
		t.Error("must be blocked after 3 failures")
	}
	if !l.Allow("other") {
		t.Error("other keys are independent")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("ip") {
		t.Error("window expired, must be allowed again")
	}
	l.Fail("ip")
	l.Reset("ip")
	if !l.Allow("ip") {
		t.Error("reset must clear failures")
	}
}

// Package auth содержит проверку учётных данных, сессии и ограничение попыток входа.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os/user"
)

// ErrInvalidCredentials возвращается при неверном логине или пароле.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Authenticator проверяет учётные данные и возвращает имя пользователя.
// Реализации: TokenAuth (режим разработки) и PAM (см. pam_stub.go).
type Authenticator interface {
	Authenticate(username, password string) (string, error)
}

// TokenAuth принимает одноразовый токен, сгенерированный при запуске сервера.
// Это режим для разработки и первой проверки; в рабочем виде используется PAM.
type TokenAuth struct{ token string }

// NewTokenAuth создаёт случайный токен.
func NewTokenAuth() (*TokenAuth, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return &TokenAuth{token: hex.EncodeToString(b)}, nil
}

// Token возвращает токен, чтобы показать его при запуске.
func (t *TokenAuth) Token() string { return t.token }

// Authenticate сравнивает пароль с токеном за постоянное время; имя пользователя игнорируется.
func (t *TokenAuth) Authenticate(_, password string) (string, error) {
	if subtle.ConstantTimeCompare([]byte(password), []byte(t.token)) != 1 {
		return "", ErrInvalidCredentials
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username, nil
	}
	return "user", nil
}

//go:build !pam

package auth

import "errors"

// NewPAM в обычной сборке недоступен. Реализация (pam.go с тегом "pam", cgo и libpam)
// будет добавлена после проверки прототипа pamauth на RED OS.
func NewPAM(service string) (Authenticator, error) {
	return nil, errors.New("PAM support is not built in; use auth_mode \"token\" or build with -tags pam")
}

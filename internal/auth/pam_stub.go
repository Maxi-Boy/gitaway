//go:build !pam

package auth

import "errors"

// NewPAM в обычной сборке недоступен: PAM требует cgo и libpam (pam-devel). Сборка с PAM:
// make build-pam (go build -tags pam).
func NewPAM(service string) (Authenticator, error) {
	return nil, errors.New("PAM support is not built in; use auth_mode \"token\" or build with -tags pam")
}

//go:build pam

package auth

import (
	"errors"
	"fmt"
	"os/user"
	"sync"

	"github.com/msteinert/pam/v2"
)

// PAMAuth проверяет пароль системной учётной записи через PAM (сервис /etc/pam.d/<service>).
// Войти можно только под тем пользователем, от имени которого запущен gitaway: вход под
// другим именем дал бы его права, а процесс без root всё равно проверяет только свой пароль
// (pam_unix через unix_chkpwd).
type PAMAuth struct {
	service string
	user    string
	confDir string     // каталог с файлами сервисов; пусто: /etc/pam.d (тесты задают свой)
	mu      sync.Mutex // модули PAM не обязаны быть потокобезопасными: проверки по одной
}

// NewPAM создаёт проверку через сервис PAM для текущего пользователя.
func NewPAM(service string) (Authenticator, error) {
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("current user: %w", err)
	}
	if service == "" {
		service = "gitaway"
	}
	return &PAMAuth{service: service, user: u.Username}, nil
}

// User возвращает имя пользователя, под которым можно войти.
func (p *PAMAuth) User() string { return p.user }

// Authenticate проверяет пароль и состояние учётной записи (не заблокирована, не истекла).
func (p *PAMAuth) Authenticate(username, password string) (string, error) {
	if username != p.user || password == "" {
		return "", ErrInvalidCredentials
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	conv := pam.ConversationFunc(func(s pam.Style, _ string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return password, nil
		case pam.PromptEchoOn:
			return username, nil
		}
		return "", nil // информационные сообщения модулей не нужны
	})
	var t *pam.Transaction
	var err error
	if p.confDir != "" {
		t, err = pam.StartConfDir(p.service, username, conv, p.confDir)
	} else {
		t, err = pam.Start(p.service, username, conv)
	}
	if err != nil {
		return "", fmt.Errorf("pam service %q: %w", p.service, err)
	}
	defer t.End()
	if err := t.Authenticate(pam.DisallowNullAuthtok); err != nil {
		return "", errors.Join(ErrInvalidCredentials, err)
	}
	if err := t.AcctMgmt(pam.DisallowNullAuthtok); err != nil {
		return "", errors.Join(ErrInvalidCredentials, err)
	}
	return username, nil
}

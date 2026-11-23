//go:build pam

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/msteinert/pam/v2"
)

// Свои сервисы PAM в каталоге теста: pam_permit пускает всех, pam_deny никого.
// Так проверяется связка с libpam без настоящих паролей.
func TestPAMWithTestServices(t *testing.T) {
	if !pam.CheckPamHasStartConfdir() {
		t.Skip("libpam without pam_start_confdir")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("gitaway-permit", "auth required pam_permit.so\naccount required pam_permit.so\n")
	write("gitaway-deny", "auth required pam_deny.so\naccount required pam_permit.so\n")

	a, err := NewPAM("gitaway-permit")
	if err != nil {
		t.Fatal(err)
	}
	p := a.(*PAMAuth)
	p.confDir = dir
	if got, err := p.Authenticate(p.User(), "any"); err != nil || got != p.User() {
		t.Errorf("permit: %q %v", got, err)
	}
	if _, err := p.Authenticate(p.User()+"x", "any"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("another user must be refused before PAM: %v", err)
	}
	if _, err := p.Authenticate(p.User(), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("empty password: %v", err)
	}
	p.service = "gitaway-deny"
	if _, err := p.Authenticate(p.User(), "any"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("deny: %v", err)
	}
}

package server

import (
	"net/http"
	"testing"

	"github.com/Maxi-Boy/gitaway/internal/config"
)

func (e *testEnv) getFrom(t *testing.T, path, realIP string) int {
	t.Helper()
	req, err := http.NewRequest("GET", e.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realIP != "" {
		req.Header.Set("X-Real-IP", realIP)
	}
	resp, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestAllowCIDRs(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.TrustProxy = true
		c.AllowCIDRs = []string{"10.0.0.0/8", "192.168.1.5/32"}
	})
	cases := []struct {
		name, ip string
		want     int
	}{
		{"allowed network", "10.20.30.40", http.StatusOK},
		{"allowed host", "192.168.1.5", http.StatusOK},
		{"mapped IPv4", "::ffff:10.0.0.1", http.StatusOK},
		{"loopback without proxy header", "", http.StatusOK},
		{"other host", "192.168.1.6", http.StatusForbidden},
		{"public", "203.0.113.7", http.StatusForbidden},
		{"garbage", "not-an-ip", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := e.getFrom(t, "/healthz", c.ip); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
	// проверка идёт до входа: страница входа и статика тоже закрыты
	if got := e.getFrom(t, "/api/login", "203.0.113.7"); got != http.StatusForbidden {
		t.Errorf("login page: %d", got)
	}
	if got := e.getFrom(t, "/", "203.0.113.7"); got != http.StatusForbidden {
		t.Errorf("index: %d", got)
	}
}

func TestRealIPIgnoredWithoutTrustProxy(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.AllowCIDRs = []string{"10.0.0.0/8"}
	})
	// без trust_proxy заголовок не учитывается: клиент это 127.0.0.1
	if got := e.getFrom(t, "/healthz", "203.0.113.7"); got != http.StatusOK {
		t.Errorf("header must be ignored: %d", got)
	}
}

func TestEmptyAllowListAllowsAll(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.TrustProxy = true })
	if got := e.getFrom(t, "/healthz", "203.0.113.7"); got != http.StatusOK {
		t.Errorf("empty allow_cidrs: %d", got)
	}
}

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultNormalizes(t *testing.T) {
	c := Default()
	c.Roots = []string{t.TempDir()}
	c.DataDir = t.TempDir()
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadOverridesAndTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeConfig(t, `{"listen":"127.0.0.1:9000","roots":["~/work"],"session_ttl":"1h"}`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" {
		t.Errorf("listen = %q", c.Listen)
	}
	if want := filepath.Join(home, "work"); c.Roots[0] != want {
		t.Errorf("root = %q, want %q", c.Roots[0], want)
	}
	if c.SessionTTL.Duration != time.Hour {
		t.Errorf("ttl = %v", c.SessionTTL.Duration)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := writeConfig(t, `{"listn":"127.0.0.1:9000"}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"auth mode":  `{"auth_mode":"magic"}`,
		"listen":     `{"listen":"nonsense"}`,
		"empty root": `{"roots":[]}`,
		"duration":   `{"session_ttl":"soon"}`,
		"negative":   `{"session_ttl":"-1h"}`,
		"bad cidr":   `{"allow_cidrs":["10.0.0.0/33"]}`,
		"bad addr":   `{"allow_cidrs":["example.com"]}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, data)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8450": true,
		"[::1]:8450":     true,
		"localhost:8450": true,
		"0.0.0.0:8450":   false,
		"10.0.0.5:8450":  false,
	}
	for addr, want := range cases {
		c := Config{Listen: addr}
		if got := c.IsLoopback(); got != want {
			t.Errorf("%s: got %v, want %v", addr, got, want)
		}
	}
}

func TestAllowCIDRs(t *testing.T) {
	c, err := Load(writeConfig(t, `{"allow_cidrs":["10.1.2.3/8"," 192.168.1.5 ","fd00::/64"]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.5/32", "fd00::/64"}
	if len(c.AllowCIDRs) != len(want) {
		t.Fatalf("got %v, want %v", c.AllowCIDRs, want)
	}
	for i := range want {
		if c.AllowCIDRs[i] != want[i] {
			t.Errorf("got %v, want %v", c.AllowCIDRs, want)
		}
	}
}

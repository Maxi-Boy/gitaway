// Package config загружает и проверяет настройки приложения.
//
// Формат файла сейчас JSON (достаточно стандартной библиотеки). Если позже выберем
// TOML или YAML, меняется только этот пакет.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Duration это time.Duration, который читается из JSON строкой вида "12h".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"12h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// Config это все настройки приложения.
type Config struct {
	Listen        string   `json:"listen"`         // адрес и порт, по умолчанию только loopback
	Roots         []string `json:"roots"`          // разрешённые корни для репозиториев и файлов
	DefaultFolder string   `json:"default_folder"` // папка по умолчанию для серверной панели
	DataDir       string   `json:"data_dir"`       // каталог данных (repos.json, ключи)
	AuthMode      string   `json:"auth_mode"`      // "token" (разработка) или "pam"
	PAMService    string   `json:"pam_service"`    // имя файла в /etc/pam.d
	SessionTTL    Duration `json:"session_ttl"`
	TrustProxy    bool     `json:"trust_proxy"` // доверять X-Forwarded-Proto и X-Real-IP от nginx
	AllowCIDRs    []string `json:"allow_cidrs"` // откуда можно подключаться; пусто: отовсюду
}

// Default возвращает настройки по умолчанию.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Listen:     "127.0.0.1:8450",
		Roots:      []string{filepath.Join(home, "projects")},
		DataDir:    filepath.Join(home, ".config", "gitaway"),
		AuthMode:   "token",
		PAMService: "gitaway",
		SessionTTL: Duration{12 * time.Hour},
	}
}

// Load читает файл настроек поверх значений по умолчанию.
// Пустой path означает "только значения по умолчанию".
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	return c, c.Normalize()
}

// Normalize раскрывает "~", делает пути абсолютными и проверяет значения.
func (c *Config) Normalize() error {
	if c.Listen == "" {
		return errors.New("listen is empty")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if len(c.Roots) == 0 {
		return errors.New("roots is empty")
	}
	for i, r := range c.Roots {
		p, err := absPath(r)
		if err != nil {
			return err
		}
		c.Roots[i] = p
	}
	if c.DefaultFolder != "" {
		p, err := absPath(c.DefaultFolder)
		if err != nil {
			return err
		}
		c.DefaultFolder = p
	}
	if c.DataDir == "" {
		return errors.New("data_dir is empty")
	}
	p, err := absPath(c.DataDir)
	if err != nil {
		return err
	}
	c.DataDir = p
	switch c.AuthMode {
	case "token", "pam":
	default:
		return fmt.Errorf("auth_mode must be \"token\" or \"pam\", got %q", c.AuthMode)
	}
	if c.SessionTTL.Duration <= 0 {
		return errors.New("session_ttl must be positive")
	}
	nets, err := ParseCIDRs(c.AllowCIDRs)
	if err != nil {
		return err
	}
	c.AllowCIDRs = c.AllowCIDRs[:0]
	for _, n := range nets {
		c.AllowCIDRs = append(c.AllowCIDRs, n.String())
	}
	return nil
}

// ParseCIDRs разбирает allow_cidrs: сети вида "10.0.0.0/8" и отдельные адреса
// ("192.168.1.5" это одна машина). Адрес вида "10.1.2.3/8" приводится к сети.
func ParseCIDRs(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		var p netip.Prefix
		if strings.Contains(s, "/") {
			v, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("allow_cidrs: %q: %w", s, err)
			}
			p = v.Masked()
		} else {
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("allow_cidrs: %q: %w", s, err)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Zone() != "" {
			return nil, fmt.Errorf("allow_cidrs: %q: zones are not supported", s)
		}
		out = append(out, p)
	}
	return out, nil
}

// IsLoopback сообщает, что сервер слушает только локальный интерфейс.
func (c Config) IsLoopback() bool {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func absPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

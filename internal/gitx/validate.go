package gitx

import (
	"net/url"
	"regexp"
	"strings"
)

// ValidBranchName консервативно проверяет имя ветки (строже, чем git check-ref-format).
// Главное: имя не может начинаться с "-", иначе его примут за опцию git.
func ValidBranchName(name string) error {
	switch {
	case name == "":
		return invalid("empty branch name")
	case len(name) > 255:
		return invalid("branch name is too long")
	case name == "@", strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"),
		strings.HasSuffix(name, "/"), strings.HasSuffix(name, "."), strings.HasSuffix(name, ".lock"):
		return invalidf("invalid branch name %q", name)
	case strings.Contains(name, ".."), strings.Contains(name, "@{"), strings.Contains(name, "//"):
		return invalidf("invalid branch name %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return invalidf("invalid character %q in branch name", r)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return invalidf("invalid branch name %q", name)
		}
	}
	return nil
}

var remoteNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// ValidRemoteName проверяет имя remote (например, origin).
func ValidRemoteName(name string) error {
	if !remoteNameRe.MatchString(name) || len(name) > 100 {
		return invalidf("invalid remote name %q", name)
	}
	return nil
}

var scpLikeRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[^\s]+$`)

// ValidCloneURL разрешает только https://, ssh:// и scp-подобные адреса user@host:path.
// Схемы ext::, file://, git:// и адреса, начинающиеся с "-", отклоняются.
func ValidCloneURL(raw string) error {
	if raw == "" || len(raw) > 2048 {
		return invalid("invalid URL length")
	}
	if strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, " \t\r\n\x00") {
		return invalid("invalid URL")
	}
	switch {
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		if err != nil {
			return invalidf("invalid URL: %v", err)
		}
		host := u.Hostname()
		if host == "" || strings.HasPrefix(host, "-") {
			return invalid("invalid host in URL")
		}
		return nil
	case scpLikeRe.MatchString(raw):
		return nil
	}
	return invalid("only https://, ssh:// and user@host:path URLs are allowed")
}

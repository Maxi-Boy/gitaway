package github

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	good := map[string]Ref{
		"https://github.com/Maxi-Boy/gitaway":             {"Maxi-Boy", "gitaway"},
		"https://github.com/Maxi-Boy/gitaway.git":         {"Maxi-Boy", "gitaway"},
		"https://github.com/Maxi-Boy/gitaway/tree/main":   {"Maxi-Boy", "gitaway"},
		"git@github.com:Maxi-Boy/gitaway.git":             {"Maxi-Boy", "gitaway"},
		"ssh://git@github.com/Maxi-Boy/gitaway":           {"Maxi-Boy", "gitaway"},
		"ssh://git@ssh.github.com:443/a/b.c":              {"a", "b.c"},
		"  owner/repo-name_1  ":                           {"owner", "repo-name_1"},
		"github.com/x/y/":                                 {"x", "y"},
		"https://github.com/user/my.dotted.repo.git/":     {"user", "my.dotted.repo"},
		"https://www.github.com/Org-1/Repo_2":             {"Org-1", "Repo_2"},
		"git@github.com:a/b":                              {"a", "b"},
		"https://github.com/abcdefghijklmnopqrstuvwxyz/r": {"abcdefghijklmnopqrstuvwxyz", "r"},
	}
	for in, want := range good {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	bad := []string{"", "repo", "https://gitlab.com/a/b", "-o/x", "a/..", "a/.", "a b/c", "a/b c",
		"git@evil.com:a/b", "a/$(rm)", "https://github.com/a", strings.Repeat("o", 40) + "/r"}
	for _, in := range bad {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) must fail", in)
		}
	}
	r := Ref{"o", "r"}
	if r.SSHURL(SSH22) != "git@github.com:o/r.git" || r.SSHURL(SSH443) != "ssh://git@ssh.github.com:443/o/r.git" ||
		r.HTTPSURL() != "https://github.com/o/r.git" || r.KeysPage() != "https://github.com/o/r/settings/keys/new" {
		t.Errorf("urls: %s %s %s %s", r.SSHURL(SSH22), r.SSHURL(SSH443), r.HTTPSURL(), r.KeysPage())
	}
}

func TestHostKeyFingerprint(t *testing.T) {
	blob, err := base64.StdEncoding.DecodeString(strings.Fields(hostKey)[1])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	if fp := base64.RawStdEncoding.EncodeToString(sum[:]); fp != "+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU" {
		t.Errorf("pinned host key fingerprint = %s", fp)
	}
	if !strings.Contains(KnownHosts, "[ssh.github.com]:443 ssh-ed25519") {
		t.Error("ssh over 443 must be pinned too")
	}
}

func readString(t *testing.T, b []byte) ([]byte, []byte) {
	t.Helper()
	if len(b) < 4 {
		t.Fatal("short buffer")
	}
	n := binary.BigEndian.Uint32(b)
	if int(n) > len(b)-4 {
		t.Fatal("bad length")
	}
	return b[4 : 4+n], b[4+n:]
}

// Ключ разбирается обратно по формату OpenSSH и подписывает так же, как открытая часть.
func TestGenerateKey(t *testing.T) {
	priv, pub, err := GenerateKey("gitaway@host o/r")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5") || !strings.HasSuffix(pub, " gitaway@host o/r") {
		t.Errorf("public: %s", pub)
	}
	block, _ := pem.Decode(priv)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		t.Fatalf("pem: %q", priv)
	}
	b := block.Bytes
	if !bytes.HasPrefix(b, []byte("openssh-key-v1\x00")) {
		t.Fatal("magic")
	}
	b = b[len("openssh-key-v1\x00"):]
	var cipher, kdf, pubBlob, sec []byte
	cipher, b = readString(t, b)
	kdf, b = readString(t, b)
	_, b = readString(t, b)
	if string(cipher) != "none" || string(kdf) != "none" || binary.BigEndian.Uint32(b) != 1 {
		t.Fatal("header")
	}
	pubBlob, b = readString(t, b[4:])
	sec, b = readString(t, b)
	if len(b) != 0 || len(sec)%8 != 0 || !bytes.Equal(sec[:4], sec[4:8]) {
		t.Fatal("private section layout")
	}
	if base64.StdEncoding.EncodeToString(pubBlob) != strings.Fields(pub)[1] {
		t.Error("public blob differs from the public line")
	}
	rest := sec[8:]
	var kt, pk, sk, comment []byte
	kt, rest = readString(t, rest)
	pk, rest = readString(t, rest)
	sk, rest = readString(t, rest)
	comment, _ = readString(t, rest)
	if string(kt) != "ssh-ed25519" || len(pk) != 32 || len(sk) != 64 || string(comment) != "gitaway@host o/r" {
		t.Fatalf("key fields: %s %d %d %q", kt, len(pk), len(sk), comment)
	}
	msg := []byte("hello")
	if !ed25519.Verify(ed25519.PublicKey(pk), msg, ed25519.Sign(ed25519.PrivateKey(sk), msg)) {
		t.Error("private and public parts do not match")
	}

	// где есть ssh-keygen (CI), он должен прочитать ключ и вывести ту же открытую часть
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Log("ssh-keygen not installed: format check by ssh-keygen skipped")
		return
	}
	path := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(path, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(keygen, "-y", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen cannot read the key: %v %s", err, out)
	}
	if !strings.HasPrefix(string(out), strings.Join(strings.Fields(pub)[:2], " ")) {
		t.Errorf("ssh-keygen public = %s, want %s", out, pub)
	}
}

func TestSSHCommandAndClassify(t *testing.T) {
	cmd := SSHCommand("/home/u/.config/gitaway/keys/k'1", "/kh")
	for _, want := range []string{"-i '/home/u/.config/gitaway/keys/k'\\''1'", "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "BatchMode=yes", "UserKnownHostsFile='/kh'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("ssh command %q lacks %q", cmd, want)
		}
	}
	cases := map[string]string{
		"git@github.com: Permission denied (publickey).\r\nfatal: Could not read from remote repository.": FailDenied,
		"ERROR: Repository not found.\nfatal: Could not read from remote repository.":                     FailNotFound,
		"ssh: connect to host github.com port 22: Connection timed out":                                   FailNetwork,
		"ssh: Could not resolve hostname github.com: Name or service not known":                           FailNetwork,
		"Host key verification failed.":                                                                   FailHostKey,
		"something else":                                                                                  FailOther,
	}
	for out, want := range cases {
		if got := Classify(out); got != want {
			t.Errorf("Classify(%q) = %s, want %s", out, got, want)
		}
	}
}

// Package github подключает репозитории GitHub через deploy-ключи: разбор адреса,
// ключ ed25519 в формате OpenSSH (создаётся в Go, ssh-keygen не нужен), закреплённый
// ключ хоста GitHub и команда ssh для git. Токены GitHub не хранятся и не нужны.
package github

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Ref это репозиторий на GitHub.
type Ref struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

// ErrBadURL означает, что адрес не похож на репозиторий GitHub.
var ErrBadURL = errors.New("not a github.com repository address")

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// Parse понимает https://github.com/owner/repo(.git), git@github.com:owner/repo.git,
// ssh://git@github.com/owner/repo и короткую запись owner/repo.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	for _, p := range []string{"https://github.com/", "http://github.com/", "https://www.github.com/",
		"git@github.com:", "ssh://git@github.com/", "ssh://git@ssh.github.com:443/", "github.com/"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return Ref{}, ErrBadURL
	}
	// https://github.com/owner/repo/tree/main и подобные ссылки из браузера
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !ownerRe.MatchString(owner) || !repoRe.MatchString(repo) || repo == "." || repo == ".." {
		return Ref{}, ErrBadURL
	}
	return Ref{Owner: owner, Repo: repo}, nil
}

func (r Ref) String() string { return r.Owner + "/" + r.Repo }

// KeysPage это страница добавления deploy-ключа в настройках репозитория.
func (r Ref) KeysPage() string {
	return fmt.Sprintf("https://github.com/%s/%s/settings/keys/new", r.Owner, r.Repo)
}

// HTTPSURL это адрес для клонирования публичного репозитория без ключа (только чтение).
func (r Ref) HTTPSURL() string {
	return fmt.Sprintf("https://github.com/%s/%s.git", r.Owner, r.Repo)
}

// Способы подключения по SSH: обычный порт 22 или SSH через 443 (ssh.github.com),
// если порт 22 закрыт на сервере или в сети.
const (
	SSH22  = "ssh22"
	SSH443 = "ssh443"
)

// SSHURL это адрес репозитория для выбранного способа подключения.
func (r Ref) SSHURL(transport string) string {
	if transport == SSH443 {
		return fmt.Sprintf("ssh://git@ssh.github.com:443/%s/%s.git", r.Owner, r.Repo)
	}
	return fmt.Sprintf("git@github.com:%s/%s.git", r.Owner, r.Repo)
}

// hostKey это ключ хоста GitHub (ed25519), опубликованный GitHub:
// SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU. Подключение к серверу с другим
// ключом отклоняется: подменить GitHub по дороге нельзя.
const hostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// KnownHosts это содержимое файла known_hosts gitaway (свой файл, ~/.ssh не трогается).
const KnownHosts = "github.com " + hostKey + "\n[ssh.github.com]:443 " + hostKey + "\n"

// SSHCommand возвращает значение GIT_SSH_COMMAND и core.sshCommand: только этот ключ,
// только закреплённый ключ хоста, без вопросов (BatchMode).
func SSHCommand(keyPath, knownHostsPath string) string {
	return strings.Join([]string{
		"ssh", "-i", quote(keyPath),
		"-o", "IdentitiesOnly=yes",
		"-o", "UserKnownHostsFile=" + quote(knownHostsPath),
		"-o", "StrictHostKeyChecking=yes",
		"-o", "HostKeyAlgorithms=ssh-ed25519",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
	}, " ")
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Key это deploy-ключ репозитория. Закрытая часть лежит в файле data_dir/keys/<ID>.
type Key struct {
	ID        string    `json:"id"`
	Ref       Ref       `json:"ref"`
	Public    string    `json:"public"`
	Transport string    `json:"transport,omitempty"` // проверенный способ подключения
	Created   time.Time `json:"created"`
}

// GenerateKey создаёт пару ed25519: закрытый ключ в формате OpenSSH (PEM) и строку
// открытого ключа для GitHub ("ssh-ed25519 AAAA... comment").
func GenerateKey(comment string) (private []byte, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pubBlob := sshString(nil, []byte("ssh-ed25519"))
	pubBlob = sshString(pubBlob, pub)

	check := make([]byte, 4)
	if _, err := rand.Read(check); err != nil {
		return nil, "", err
	}
	var sec []byte
	sec = append(sec, check...)
	sec = append(sec, check...) // два одинаковых числа: проверка расшифровки
	sec = sshString(sec, []byte("ssh-ed25519"))
	sec = sshString(sec, pub)
	sec = sshString(sec, priv) // 64 байта: закрытая часть и открытая
	sec = sshString(sec, []byte(comment))
	for i := byte(1); len(sec)%8 != 0; i++ { // выравнивание до размера блока (без шифрования: 8)
		sec = append(sec, i)
	}

	var body bytes.Buffer
	body.WriteString("openssh-key-v1\x00")
	body.Write(sshString(nil, []byte("none"))) // без шифрования: файл защищён правами 0600
	body.Write(sshString(nil, []byte("none")))
	body.Write(sshString(nil, nil))
	_ = binary.Write(&body, binary.BigEndian, uint32(1))
	body.Write(sshString(nil, pubBlob))
	body.Write(sshString(nil, sec))

	private = pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: body.Bytes()})
	public = "ssh-ed25519 " + base64.StdEncoding.EncodeToString(pubBlob)
	if comment != "" {
		public += " " + comment
	}
	return private, public, nil
}

func sshString(dst, s []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...)
}

// Причины, по которым не удалось подключиться (для понятного сообщения в интерфейсе).
const (
	FailDenied   = "denied"    // ключ не добавлен в репозиторий
	FailNotFound = "not_found" // репозиторий не существует или скрыт
	FailNetwork  = "network"   // нет связи с GitHub по этому способу
	FailHostKey  = "host_key"  // ключ хоста не совпал: подмена или сбой
	FailOther    = "other"
)

// Classify определяет причину ошибки по выводу git и ssh.
func Classify(output string) string {
	o := strings.ToLower(output)
	switch {
	case strings.Contains(o, "host key verification failed"), strings.Contains(o, "remote host identification has changed"):
		return FailHostKey
	case strings.Contains(o, "permission denied (publickey)"):
		return FailDenied
	case strings.Contains(o, "repository not found"), strings.Contains(o, "could not read from remote repository") && strings.Contains(o, "not found"):
		return FailNotFound
	case strings.Contains(o, "timed out"), strings.Contains(o, "could not resolve hostname"),
		strings.Contains(o, "connection refused"), strings.Contains(o, "network is unreachable"),
		strings.Contains(o, "no route to host"), strings.Contains(o, "connection reset"),
		strings.Contains(o, "connection closed by remote host"):
		return FailNetwork
	}
	return FailOther
}

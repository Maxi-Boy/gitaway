// Прототип 1: проверка пароля пользователя через PAM из процесса БЕЗ root.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/user"

	"github.com/msteinert/pam/v2"
	"golang.org/x/term"
)

func authenticate(service, username, password string) error {
	t, err := pam.StartFunc(service, username, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return password, nil
		case pam.PromptEchoOn:
			return username, nil
		}
		return "", nil
	})
	if err != nil {
		return fmt.Errorf("pam start: %w", err)
	}
	if err := t.Authenticate(0); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if err := t.AcctMgmt(0); err != nil {
		return fmt.Errorf("account: %w", err)
	}
	return nil
}

func main() {
	service := flag.String("service", "gitaway-poc", "имя PAM-сервиса (файл /etc/pam.d/<имя>)")
	username := flag.String("user", "", "кого проверять (по умолчанию текущий пользователь)")
	flag.Parse()

	cur, err := user.Current()
	if err != nil {
		fmt.Println("user.Current:", err)
		os.Exit(1)
	}
	u := *username
	if u == "" {
		u = cur.Username
	}
	fmt.Printf("процесс: uid=%d euid=%d; проверяем пользователя %q; PAM-сервис %q\n",
		os.Getuid(), os.Geteuid(), u, *service)
	if os.Geteuid() == 0 {
		fmt.Println("ВНИМАНИЕ: запущено от root, для теста нужен обычный пользователь")
	}

	fmt.Print("Пароль: ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Println("чтение пароля:", err)
		os.Exit(1)
	}
	if err := authenticate(*service, u, string(pw)); err != nil {
		fmt.Println("ОШИБКА:", err)
		os.Exit(1)
	}
	fmt.Println("OK: пароль принят")
}

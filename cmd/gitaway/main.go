// Команда gitaway запускает веб-интерфейс для git и файлов на сервере.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/config"
	"github.com/Maxi-Boy/gitaway/internal/fsx"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/server"
	"github.com/Maxi-Boy/gitaway/internal/store"
	"github.com/Maxi-Boy/gitaway/internal/tasks"
	"github.com/Maxi-Boy/gitaway/web"
)

// version подставляется при сборке: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gitaway:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "", "путь к config.json (по умолчанию встроенные значения)")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	sandbox, err := fsx.NewSandbox(cfg.Roots)
	if err != nil {
		return fmt.Errorf("%w (создайте каталог или измените roots в конфигурации)", err)
	}
	repos, err := store.Open(filepath.Join(cfg.DataDir, "repos.json"))
	if err != nil {
		return err
	}
	settings, err := store.OpenSettings(filepath.Join(cfg.DataDir, "settings.json"),
		store.DefaultSettings(cfg.DefaultFolder))
	if err != nil {
		return err
	}
	myTasks, err := store.OpenDoc(filepath.Join(cfg.DataDir, "tasks.json"), []tasks.Task{})
	if err != nil {
		return err
	}
	trust, err := store.OpenDoc(filepath.Join(cfg.DataDir, "trust.json"), map[string][]string{})
	if err != nil {
		return err
	}

	var authn auth.Authenticator
	switch cfg.AuthMode {
	case "pam":
		authn, err = auth.NewPAM(cfg.PAMService)
		if err != nil {
			return err
		}
	default:
		ta, err := auth.NewTokenAuth()
		if err != nil {
			return err
		}
		authn = ta
		fmt.Printf("Токен для входа (вводится в поле пароля): %s\n", ta.Token())
	}
	if !cfg.IsLoopback() {
		log.Warn("сервер слушает не на loopback: нужен TLS (например, nginx) и ограничение доступа по сети",
			"listen", cfg.Listen)
		if len(cfg.AllowCIDRs) == 0 {
			log.Warn("allow_cidrs не задан: подключиться можно с любого адреса")
		}
	}
	if len(cfg.AllowCIDRs) > 0 {
		log.Info("доступ разрешён только из сетей", "allow_cidrs", cfg.AllowCIDRs)
		if cfg.IsLoopback() && !cfg.TrustProxy {
			log.Warn("allow_cidrs без trust_proxy: за nginx все запросы приходят с 127.0.0.1 и проверка ничего не ограничит")
		}
	}

	srv := server.New(server.Deps{
		Cfg:      cfg,
		Auth:     authn,
		Sandbox:  sandbox,
		Repos:    repos,
		Settings: settings,
		Git:      gitx.NewRunner(),
		Log:      log,
		Web:      web.FS,
		Tasks:    myTasks,
		Trust:    trust,
	})
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "version", version)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(shutdownCtx)
	srv.Close() // процессы терминалов не переживают gitaway
	return err
}

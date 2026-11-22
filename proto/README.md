# Прототипы (PoC)

Три маленьких программы, которые проверяют самые рискованные места проекта.
Проект пока без названия и лицензии, это только эксперименты.

ВАЖНО: код написан без возможности скомпилировать его на стороне автора, поэтому
возможны мелкие ошибки сборки. Если `go build` ругается, пришлите вывод ошибки.

## Подготовка (RED OS, RPM)

    sudo dnf install golang gcc pam-devel git
    go version        # нужен Go 1.21 или новее; если старше, поставьте с go.dev
    cd poc
    go mod tidy       # скачает зависимости (нужен доступ в интернет)

## 1. PAM без root (pamauth)

Проверяем, что обычный пользователь может проверить пароль своей учётки через PAM.

    sudo tee /etc/pam.d/gitaway-poc >/dev/null <<'PAM'
    auth     include   system-auth
    account  include   system-auth
    PAM
    go build -o /tmp/pamauth ./pamauth
    /tmp/pamauth                 # верный пароль -> OK
    /tmp/pamauth                 # неверный пароль -> ОШИБКА
    /tmp/pamauth -user root      # чужая учётка без root, ожидаем ОШИБКУ
    ldd /tmp/pamauth | head      # какие библиотеки подтянул cgo

Запускать от обычного пользователя, не от root. Если `include system-auth` не подходит
для вашей конфигурации, замените на `auth required pam_unix.so` и
`account required pam_unix.so`.

## 2. Терминал в браузере (term)

    go run ./term -dir ~/projects
    # с рабочего ПК: ssh -L 8450:127.0.0.1:8450 user@server
    # затем откройте адрес, который напечатала программа (с токеном)

Проверьте: ввод, цвета, `top`, `vim`, изменение размера окна, закрытие вкладки
(после закрытия в `ps aux | grep bash` не должно оставаться лишних оболочек).
Для xterm.js браузеру нужен доступ к cdn.jsdelivr.net.

## 3. Последний коммит по файлам (lastcommit)

    go run ./lastcommit -repo /путь/к/большому/репозиторию -compare
    go run ./lastcommit -repo /путь/к/большому/репозиторию -dir src -compare

Для отчёта пришлите: время ls-tree и прохода git log, результат сравнения
с наивным вариантом, а также `git -C <repo> rev-list --count HEAD`
(число коммитов) и примерный размер репозитория.

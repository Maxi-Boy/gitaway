// Прототип 3: последний коммит для каждого элемента каталога одним проходом git log.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CommitInfo struct {
	Hash, Author, Subject string
	Time                  int64
}

// trackedNames возвращает имена элементов каталога dir ("" = корень), отслеживаемых git в HEAD.
func trackedNames(ctx context.Context, repo, dir string) (map[string]bool, error) {
	args := []string{"ls-tree", "-z", "--name-only", "HEAD"}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
		args = append(args, "--", prefix)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			names[strings.TrimPrefix(p, prefix)] = true
		}
	}
	return names, nil
}

// lastCommits идёт по истории от новых коммитов к старым и останавливается,
// когда найден последний коммит для каждого имени из names.
func lastCommits(ctx context.Context, repo, dir string, names map[string]bool) map[string]CommitInfo {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{"-c", "core.quotepath=false", "log", "--no-renames", "--name-only",
		"--format=%x1e%H%x1f%an%x1f%at%x1f%s"}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
		args = append(args, "--", dir)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}

	found := map[string]CommitInfo{}
	var cur CommitInfo
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for len(found) < len(names) && sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\x1e") {
			p := strings.SplitN(line[1:], "\x1f", 4)
			if len(p) == 4 {
				ts, _ := strconv.ParseInt(p[2], 10, 64)
				cur = CommitInfo{Hash: p[0], Author: p[1], Time: ts, Subject: p[3]}
			}
			continue
		}
		if line == "" {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(line, prefix), "/", 2)[0]
		if names[name] {
			if _, ok := found[name]; !ok {
				found[name] = cur
			}
		}
	}
	cancel() // останавливаем git, когда всё найдено
	_ = cmd.Wait()
	return found
}

func main() {
	repo := flag.String("repo", ".", "путь к репозиторию")
	dir := flag.String("dir", "", "каталог внутри репозитория (пусто = корень)")
	timeout := flag.Duration("timeout", 20*time.Second, "лимит времени на проход по истории")
	compare := flag.Bool("compare", false, "сравнить с наивным git log -1 на каждый элемент (до 20 штук)")
	flag.Parse()

	d := strings.Trim(*dir, "/")
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	t0 := time.Now()
	names, err := trackedNames(ctx, *repo, d)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ls-tree:", err)
		os.Exit(1)
	}
	tList := time.Since(t0)

	t1 := time.Now()
	found := lastCommits(ctx, *repo, d, names)
	tWalk := time.Since(t1)

	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ci, ok := found[k]; ok {
			h := ci.Hash
			if len(h) > 8 {
				h = h[:8]
			}
			fmt.Printf("%-32s %s %s %-14s %s\n", k, h,
				time.Unix(ci.Time, 0).Format("2006-01-02 15:04"), ci.Author, ci.Subject)
		} else {
			fmt.Printf("%-32s (не найдено за отведённое время)\n", k)
		}
	}
	fmt.Printf("\nэлементов: %d, найдено: %d\nls-tree: %s\nпроход git log: %s\n",
		len(names), len(found), tList, tWalk)

	if *compare && len(names) > 0 {
		t2 := time.Now()
		n := 0
		for _, k := range keys {
			if n == 20 {
				break
			}
			p := k
			if d != "" {
				p = d + "/" + k
			}
			c := exec.Command("git", "log", "-1", "--format=%H", "--", p)
			c.Dir = *repo
			_, _ = c.Output()
			n++
		}
		per := time.Since(t2) / time.Duration(n)
		fmt.Printf("наивный вариант: %d элементов за %s (~%s на элемент, ~%s на все %d)\n",
			n, time.Since(t2), per, per*time.Duration(len(names)), len(names))
	}
}

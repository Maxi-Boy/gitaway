package gitx

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// CommitInfo это последний коммит, изменивший элемент каталога.
type CommitInfo struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
	Time    int64  `json:"time"`
}

// TrackedNames возвращает имена элементов каталога sub ("" это корень), отслеживаемых git в HEAD.
func (r *Runner) TrackedNames(ctx context.Context, dir, sub string) (map[string]bool, error) {
	args := []string{"ls-tree", "-z", "--name-only", "HEAD"}
	prefix := ""
	if sub != "" {
		prefix = sub + "/"
		args = append(args, "--", prefix)
	}
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, args...)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			names[strings.TrimPrefix(p, prefix)] = true
		}
	}
	return names, nil
}

// LastCommits идёт по истории от новых коммитов к старым и останавливается, когда
// для каждого имени из names найден последний коммит. Если время вышло раньше,
// возвращается то, что успели найти.
func (r *Runner) LastCommits(ctx context.Context, dir, sub string, names map[string]bool) map[string]CommitInfo {
	found := map[string]CommitInfo{}
	if len(names) == 0 {
		return found
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{"-c", "core.quotepath=false", "log", "--no-renames", "--name-only",
		"--format=%x1e%H%x1f%an%x1f%at%x1f%s"}
	prefix := ""
	if sub != "" {
		prefix = sub + "/"
		args = append(args, "--", sub)
	}
	bin := r.GitBin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	env = append(env, r.Env...)
	cmd.Env = append(env, readOnlyEnv...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return found
	}
	if err := cmd.Start(); err != nil {
		return found
	}

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
		name, _, _ := strings.Cut(strings.TrimPrefix(line, prefix), "/")
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

// CommitCache кэширует результат по паре (каталог, HEAD): пока HEAD не изменился,
// повторный проход по истории не нужен.
type CommitCache struct {
	mu sync.Mutex
	m  map[string]map[string]CommitInfo
}

func NewCommitCache() *CommitCache {
	return &CommitCache{m: map[string]map[string]CommitInfo{}}
}

// Get возвращает последние коммиты для элементов каталога sub в рабочей папке dir.
func (c *CommitCache) Get(ctx context.Context, r *Runner, dir, sub string) (map[string]CommitInfo, error) {
	head, err := r.RunEnv(ctx, dir, readOnlyEnv, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && ge.Code == 1 { // пустой репозиторий без коммитов
			return map[string]CommitInfo{}, nil
		}
		return nil, err
	}
	key := dir + "\x00" + sub + "\x00" + strings.TrimSpace(head)

	c.mu.Lock()
	if v, ok := c.m[key]; ok {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	names, err := r.TrackedNames(ctx, dir, sub)
	if err != nil {
		return nil, err
	}
	found := r.LastCommits(ctx, dir, sub, names)
	if len(found) == len(names) { // неполные результаты не кэшируем
		c.mu.Lock()
		if len(c.m) > 256 {
			c.m = map[string]map[string]CommitInfo{}
		}
		c.m[key] = found
		c.mu.Unlock()
	}
	return found, nil
}

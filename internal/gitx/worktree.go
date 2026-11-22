package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Worktree это одна рабочая папка репозитория (основная или добавленная через git worktree).
type Worktree struct {
	Path     string `json:"path"`
	Head     string `json:"head,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Main     bool   `json:"main"`
	Bare     bool   `json:"bare,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
	Prunable bool   `json:"prunable,omitempty"`
}

// ErrUnknownWorktree возвращается, если путь не входит в список worktree репозитория.
var ErrUnknownWorktree = errors.New("unknown worktree")

// Worktrees возвращает список рабочих папок; первая это основная.
func (r *Runner) Worktrees(ctx context.Context, repo string) ([]Worktree, error) {
	out, err := r.RunEnv(ctx, repo, readOnlyEnv, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

func parseWorktrees(out string) []Worktree {
	list := []Worktree{}
	var cur *Worktree
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked = true
		case "prunable":
			cur.Prunable = true
		}
	}
	flush()
	if len(list) > 0 {
		list[0].Main = true
	}
	return list
}

// AddWorktreeOptions описывает новую рабочую папку.
type AddWorktreeOptions struct {
	Path         string // абсолютный путь новой папки
	Branch       string
	CreateBranch bool   // создать ветку (git worktree add -b)
	StartPoint   string // от какой ветки или коммита создавать (только с CreateBranch)
}

// AddWorktree создаёт рабочую папку для ветки.
func (r *Runner) AddWorktree(ctx context.Context, repo string, o AddWorktreeOptions) (string, error) {
	if !filepath.IsAbs(o.Path) {
		return "", invalid("worktree path must be absolute")
	}
	if err := ValidBranchName(o.Branch); err != nil {
		return "", err
	}
	args := []string{"worktree", "add"}
	if o.CreateBranch {
		args = append(args, "-b", o.Branch, o.Path)
		if o.StartPoint != "" {
			if err := ValidBranchName(o.StartPoint); err != nil {
				return "", err
			}
			args = append(args, o.StartPoint)
		}
	} else {
		args = append(args, o.Path, o.Branch)
	}
	return r.RunCombined(ctx, repo, args...)
}

// RemoveWorktree удаляет рабочую папку, только если она есть в списке worktree.
// Основную рабочую папку удалить нельзя.
func (r *Runner) RemoveWorktree(ctx context.Context, repo, path string, force bool) (string, error) {
	list, err := r.Worktrees(ctx, repo)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	found := false
	for _, w := range list {
		if filepath.Clean(w.Path) != clean {
			continue
		}
		if w.Main {
			return "", invalid("cannot remove the main worktree")
		}
		found = true
		break
	}
	if !found {
		return "", ErrUnknownWorktree
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, clean)
	return r.RunCombined(ctx, repo, args...)
}

// SafeDirName превращает имя ветки в безопасное имя каталога (feature/login даёт feature-login).
func SafeDirName(branch string) string {
	var b strings.Builder
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.TrimLeft(b.String(), ".-")
	if s == "" {
		return "branch"
	}
	return s
}

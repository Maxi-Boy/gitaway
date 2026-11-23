package gitx

import (
	"context"
	"strconv"
	"strings"
)

// Branch описывает локальную или удалённую ветку.
type Branch struct {
	Name     string `json:"name"`
	Remote   bool   `json:"remote"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Gone     bool   `json:"gone,omitempty"`
	Current  bool   `json:"current"`
}

// Branches возвращает локальные и удалённые ветки со счётчиками ahead/behind.
func (r *Runner) Branches(ctx context.Context, dir string) ([]Branch, error) {
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, "for-each-ref",
		"--format=%(refname)%09%(refname:short)%09%(upstream:short)%09%(upstream:track)%09%(HEAD)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	list := []Branch{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		ref := f[0]
		isRemote := strings.HasPrefix(ref, "refs/remotes/")
		if isRemote && strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		b := Branch{Name: f[1], Remote: isRemote, Upstream: f[2], Current: f[4] == "*"}
		b.Ahead, b.Behind, b.Gone = parseTrack(f[3])
		list = append(list, b)
	}
	return list, nil
}

// parseTrack разбирает "[ahead 1, behind 2]", "[behind 2]", "[gone]" или "".
func parseTrack(s string) (ahead, behind int, gone bool) {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	if s == "" {
		return
	}
	if s == "gone" {
		gone = true
		return
	}
	for _, part := range strings.Split(s, ",") {
		f := strings.Fields(part)
		if len(f) != 2 {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		switch f[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return
}

// CurrentBranch возвращает имя текущей ветки ("" при detached HEAD).
func (r *Runner) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, "branch", "--show-current")
	return strings.TrimSpace(out), err
}

// Change это одна запись git status: X это индекс, Y рабочая папка ("??" это неотслеживаемый).
type Change struct {
	Path string `json:"path"`
	X    string `json:"x"`
	Y    string `json:"y"`
}

// WorktreeStatus это состояние рабочей папки.
type WorktreeStatus struct {
	Branch   string   `json:"branch"`
	Upstream string   `json:"upstream,omitempty"`
	Ahead    int      `json:"ahead"`
	Behind   int      `json:"behind"`
	Gone     bool     `json:"gone,omitempty"`
	Detached bool     `json:"detached,omitempty"`
	Merging  bool     `json:"merging,omitempty"` // идёт слияние (есть MERGE_HEAD)
	Changes  []Change `json:"changes"`
}

// Status возвращает ветку, счётчики ahead/behind и список изменений.
func (r *Runner) Status(ctx context.Context, dir string) (*WorktreeStatus, error) {
	out, err := r.RunEnv(ctx, dir, readOnlyEnv,
		"status", "--porcelain=v1", "-z", "--branch", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	st := parseStatus(out)
	if _, err := r.RunEnv(ctx, dir, readOnlyEnv, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
		st.Merging = true
	}
	return st, nil
}

func parseStatus(out string) *WorktreeStatus {
	st := &WorktreeStatus{Changes: []Change{}}
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "## ") {
			parseBranchLine(st, p[3:])
			continue
		}
		if len(p) < 4 {
			continue
		}
		c := Change{X: p[0:1], Y: p[1:2], Path: p[3:]}
		if c.X == "R" || c.X == "C" {
			i++ // следующая запись это исходный путь переименования или копии
		}
		st.Changes = append(st.Changes, c)
	}
	return st
}

func parseBranchLine(st *WorktreeStatus, s string) {
	switch {
	case strings.HasPrefix(s, "No commits yet on "):
		st.Branch = strings.TrimPrefix(s, "No commits yet on ")
		return
	case strings.HasPrefix(s, "HEAD (no branch)"):
		st.Detached = true
		return
	}
	track := ""
	if i := strings.Index(s, " ["); i >= 0 {
		track = s[i+1:]
		s = s[:i]
	}
	if i := strings.Index(s, "..."); i >= 0 {
		st.Upstream = s[i+3:]
		s = s[:i]
	}
	st.Branch = s
	st.Ahead, st.Behind, st.Gone = parseTrack(track)
}

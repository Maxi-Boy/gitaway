package copyop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Action это решение по одному конфликту.
type Action string

const (
	ActionReplace  Action = "replace"
	ActionSkip     Action = "skip"
	ActionKeepBoth Action = "keep_both" // сохранить оба: новый файл получит имя "имя (2).ext"
)

// Policy это массовое решение для конфликтов, по которым нет явного решения.
type Policy string

const (
	PolicyNone         Policy = "" // массового решения нет
	PolicyReplaceAll   Policy = "replace_all"
	PolicyReplaceNewer Policy = "replace_newer" // заменять, только если источник новее
	PolicySkipAll      Policy = "skip_all"
	PolicyKeepBothAll  Policy = "keep_both_all"
)

// ExecRequest это запрос на выполнение. Decisions задаёт решения по Rel конфликта и
// имеет приоритет над Policy.
type ExecRequest struct {
	Request
	Decisions map[string]Action
	Policy    Policy
}

// UnresolvedError возвращается, если по некоторым конфликтам нет решения.
// В этом случае на диске ничего не изменяется.
type UnresolvedError struct{ Rels []string }

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("%d conflicts need a decision", len(e.Rels))
}

// Renamed это файл, сохранённый под новым именем (решение keep_both).
type Renamed struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Failure это ошибка по одному файлу; остальные файлы продолжают обрабатываться.
type Failure struct {
	Rel   string `json:"rel"`
	Error string `json:"error"`
}

// Result это итог выполнения. Copied, Replaced, Renamed и Failed содержат пути
// относительно назначения, а Skipped относительно источника.
type Result struct {
	Copied   []string  `json:"copied"`
	Replaced []string  `json:"replaced"`
	Renamed  []Renamed `json:"renamed"`
	Skipped  []Skipped `json:"skipped"`
	Failed   []Failure `json:"failed"`
	Bytes    int64     `json:"bytes"`
}

func validateExec(req ExecRequest) error {
	switch req.Policy {
	case PolicyNone, PolicyReplaceAll, PolicyReplaceNewer, PolicySkipAll, PolicyKeepBothAll:
	default:
		return fmt.Errorf("%w: unknown policy %q", ErrInvalid, req.Policy)
	}
	for rel, a := range req.Decisions {
		switch a {
		case ActionReplace, ActionSkip, ActionKeepBoth:
		default:
			return fmt.Errorf("%w: unknown action %q for %q", ErrInvalid, a, rel)
		}
	}
	return nil
}

func decide(c Conflict, req ExecRequest) (Action, bool) {
	if a, ok := req.Decisions[c.Rel]; ok {
		return a, true
	}
	switch req.Policy {
	case PolicyReplaceAll:
		return ActionReplace, true
	case PolicyReplaceNewer:
		if c.Newer == "src" {
			return ActionReplace, true
		}
		return ActionSkip, true
	case PolicySkipAll:
		return ActionSkip, true
	case PolicyKeepBothAll:
		return ActionKeepBoth, true
	}
	return "", false
}

// Execute выполняет операцию. Если решение нужно, но не принято, возвращается
// *UnresolvedError до любых изменений. Ошибка по отдельному файлу не прерывает
// остальные, она попадает в Result.Failed. Отмена контекста прерывает операцию;
// уже записанные файлы остаются целыми (запись атомарна).
func Execute(ctx context.Context, g Guard, req ExecRequest) (*Result, error) {
	if err := validateExec(req); err != nil {
		return nil, err
	}
	plan, err := BuildPlan(g, req.Request)
	if err != nil {
		return nil, err
	}

	actions := map[string]Action{}
	same := map[string]bool{}
	var unresolved []string
	for _, c := range plan.Conflicts {
		same[c.Rel] = c.SameFile
		a, ok := decide(c, req)
		if !ok {
			unresolved = append(unresolved, c.Rel)
			continue
		}
		actions[c.Rel] = a
	}
	if len(unresolved) > 0 {
		return nil, &UnresolvedError{Rels: unresolved}
	}

	res := &Result{
		Copied: []string{}, Replaced: []string{}, Renamed: []Renamed{},
		Skipped: []Skipped{}, Failed: []Failure{},
	}
	for _, b := range plan.Blocked {
		res.Skipped = append(res.Skipped, Skipped{Rel: b.SrcRel, Reason: b.Reason})
	}
	res.Skipped = append(res.Skipped, plan.Skipped...)

	for _, d := range plan.Dirs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		target, err := resolveTarget(g, req.DstDir, d.Rel)
		if err == nil {
			err = os.MkdirAll(target, 0o755)
		}
		if err != nil {
			res.Failed = append(res.Failed, Failure{Rel: d.Rel, Error: err.Error()})
		}
	}

	for _, op := range plan.Files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		fail := func(err error) {
			res.Failed = append(res.Failed, Failure{Rel: op.Rel, Error: err.Error()})
		}
		target, err := resolveTarget(g, req.DstDir, op.Rel)
		if err != nil {
			fail(err)
			continue
		}
		replacing, renamed := false, false
		if op.Conflict {
			switch actions[op.Rel] {
			case ActionSkip:
				res.Skipped = append(res.Skipped, Skipped{Rel: op.SrcRel, Reason: ReasonUser})
				continue
			case ActionKeepBoth:
				nt, err := uniqueName(target)
				if err != nil {
					fail(err)
					continue
				}
				target, renamed = nt, true
			case ActionReplace:
				if same[op.Rel] {
					res.Skipped = append(res.Skipped, Skipped{Rel: op.SrcRel, Reason: ReasonSameFile})
					continue
				}
				replacing = true
			}
		}

		n, err := copyFileAtomic(ctx, op.src, target, op.mode, op.mtime)
		if err != nil {
			fail(err)
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			continue
		}
		res.Bytes += n
		switch {
		case renamed:
			to := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(op.Rel)), filepath.Base(target)))
			res.Renamed = append(res.Renamed, Renamed{From: op.Rel, To: to})
		case replacing:
			res.Replaced = append(res.Replaced, op.Rel)
		default:
			res.Copied = append(res.Copied, op.Rel)
		}
		if req.Move {
			if err := os.Remove(op.src); err != nil {
				fail(fmt.Errorf("copied, but the source could not be removed: %w", err))
			}
		}
	}

	if req.Move {
		// Пустые каталоги источника удаляются от вложенных к корню; непустые остаются.
		for i := len(plan.Dirs) - 1; i >= 0; i-- {
			_ = os.Remove(plan.Dirs[i].src)
		}
	}
	return res, nil
}

// ctxReader прерывает копирование большого файла при отмене контекста.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// copyFileAtomic копирует файл во временный файл рядом с целью, выставляет права и
// время изменения источника и переименовывает его на место. Цель появляется целиком
// или не появляется вовсе.
func copyFileAtomic(ctx context.Context, src, dst string, perm fs.FileMode, mtime time.Time) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, errors.New("source is not a regular file")
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".gitaway-copy-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // после успешного Rename файла уже нет, ошибка игнорируется

	n, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: in})
	if err != nil {
		tmp.Close()
		return n, err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return n, err
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	// Время изменения сохраняется: так повторное "заменить, если новее" ничего не делает.
	if err := os.Chtimes(tmpName, mtime, mtime); err != nil {
		return n, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return n, err
	}
	return n, nil
}

// uniqueName подбирает свободное имя "имя (2).ext", "имя (3).ext" и т. д.
func uniqueName(path string) (string, error) {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" { // файл вида ".bashrc"
		stem, ext = base, ""
	}
	for i := 2; i < 10000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Lstat(cand); errors.Is(err, fs.ErrNotExist) {
			return cand, nil
		}
	}
	return "", errors.New("cannot find a free file name")
}

package gitx

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotMerged означает, что git branch -d отказал: в ветке есть коммиты, которых нет
// в текущей ветке и в её upstream. Удалить можно только принудительно.
var ErrNotMerged = errors.New("branch is not fully merged")

// Merge сливает branch (локальную или удалённую, например origin/main) в текущую ветку
// рабочей папки. При конфликтах возвращает ошибку git; состояние слияния видно в Status
// (Merging), отменить можно MergeAbort. Редактора конфликтов нет: правка в редакторе
// или терминале, затем коммит.
func (r *Runner) Merge(ctx context.Context, dir, branch string) (string, error) {
	if err := ValidBranchName(branch); err != nil {
		return "", err
	}
	return r.RunCombined(ctx, dir, "merge", "--no-edit", branch)
}

// MergeAbort отменяет незавершённое слияние.
func (r *Runner) MergeAbort(ctx context.Context, dir string) (string, error) {
	return r.RunCombined(ctx, dir, "merge", "--abort")
}

// DeleteBranch удаляет локальную ветку. Без force git откажет для неслитой ветки
// (ErrNotMerged). Ветку, открытую в какой-либо рабочей папке, git не удаляет.
func (r *Runner) DeleteBranch(ctx context.Context, dir, branch string, force bool) (string, error) {
	if err := ValidBranchName(branch); err != nil {
		return "", err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	out, err := r.RunCombined(ctx, dir, "branch", flag, branch)
	if err != nil && strings.Contains(out+errOutput(err), "not fully merged") {
		return out, fmt.Errorf("%w: %s", ErrNotMerged, branch)
	}
	return out, err
}

// DeleteRemoteBranch удаляет ветку на сервере: git push --delete remote branch.
func (r *Runner) DeleteRemoteBranch(ctx context.Context, dir, remote, branch string) (string, error) {
	if err := ValidRemoteName(remote); err != nil {
		return "", err
	}
	if err := ValidBranchName(branch); err != nil {
		return "", err
	}
	return r.RunCombined(ctx, dir, "push", "--delete", remote, branch)
}

func errOutput(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Output
	}
	return ""
}

package gitx

import (
	"context"
	"strings"
)

// Fetch обновляет удалённые ветки. Пустой remote означает все remote.
func (r *Runner) Fetch(ctx context.Context, dir, remote string) (string, error) {
	args := []string{"fetch", "--prune"}
	if remote == "" {
		args = append(args, "--all")
	} else {
		if err := ValidRemoteName(remote); err != nil {
			return "", err
		}
		args = append(args, remote)
	}
	return r.RunCombined(ctx, dir, args...)
}

// Pull подтягивает изменения только как fast-forward (без неожиданных merge-коммитов).
func (r *Runner) Pull(ctx context.Context, dir string) (string, error) {
	return r.RunCombined(ctx, dir, "pull", "--ff-only")
}

// PushOptions описывает push. Пустые Remote и Branch означают "git push" по настроенному upstream.
type PushOptions struct {
	Remote         string
	Branch         string
	SetUpstream    bool
	ForceWithLease bool // единственный допустимый вид принудительной отправки
}

// Push отправляет ветку. Pull и push это отдельные операции, они никогда не выполняются вместе.
func (r *Runner) Push(ctx context.Context, dir string, o PushOptions) (string, error) {
	args := []string{"push"}
	if o.SetUpstream {
		args = append(args, "-u")
	}
	if o.ForceWithLease {
		args = append(args, "--force-with-lease")
	}
	if o.Remote != "" || o.Branch != "" {
		if err := ValidRemoteName(o.Remote); err != nil {
			return "", err
		}
		if err := ValidBranchName(o.Branch); err != nil {
			return "", err
		}
		args = append(args, o.Remote, o.Branch)
	}
	return r.RunCombined(ctx, dir, args...)
}

// Switch переключает текущую рабочую папку на ветку.
func (r *Runner) Switch(ctx context.Context, dir, branch string) (string, error) {
	if err := ValidBranchName(branch); err != nil {
		return "", err
	}
	return r.RunCombined(ctx, dir, "switch", branch)
}

// UpdateBranch обновляет ветку из её upstream. Для текущей ветки это pull --ff-only,
// для остальных fetch <remote> <ветка>:<ветка> (только fast-forward, без переключения).
// git откажется обновлять ветку, открытую в другом worktree.
func (r *Runner) UpdateBranch(ctx context.Context, dir, branch string) (string, error) {
	if err := ValidBranchName(branch); err != nil {
		return "", err
	}
	cur, err := r.CurrentBranch(ctx, dir)
	if err != nil {
		return "", err
	}
	if cur == branch {
		return r.Pull(ctx, dir)
	}
	noUpstream := invalidf("branch %q has no upstream configured", branch)
	remote, err := r.Run(ctx, dir, "config", "--get", "branch."+branch+".remote")
	if err != nil {
		return "", noUpstream
	}
	merge, err := r.Run(ctx, dir, "config", "--get", "branch."+branch+".merge")
	if err != nil {
		return "", noUpstream
	}
	remote = strings.TrimSpace(remote)
	src := strings.TrimPrefix(strings.TrimSpace(merge), "refs/heads/")
	if err := ValidRemoteName(remote); err != nil {
		return "", invalid("upstream is not a remote branch")
	}
	if err := ValidBranchName(src); err != nil {
		return "", err
	}
	return r.RunCombined(ctx, dir, "fetch", remote, src+":"+branch)
}

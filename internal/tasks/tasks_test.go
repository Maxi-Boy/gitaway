package tasks

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runScript выполняет сценарий задачи в /bin/sh и возвращает вывод и код выхода.
func runScript(t *testing.T, dir, script string) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func TestNormalize(t *testing.T) {
	ok := Task{Name: "  Сборка ", Steps: []string{" make build ", "", "  "}, Timeout: "30m"}
	if err := ok.Normalize(); err != nil || ok.Name != "Сборка" || len(ok.Steps) != 1 || ok.Steps[0] != "make build" {
		t.Errorf("normalize: %+v %v", ok, err)
	}
	bad := []Task{
		{Name: "", Steps: []string{"x"}},
		{Name: "a\nb", Steps: []string{"x"}},
		{Name: "no steps", Steps: []string{" "}},
		{Name: "relative cwd", Steps: []string{"x"}, Cwd: "build"},
		{Name: "bad timeout", Steps: []string{"x"}, Timeout: "forever"},
		{Name: "too long", Steps: []string{"x"}, Timeout: "48h"},
		{Name: "nul", Steps: []string{"a\x00b"}},
		{Name: strings.Repeat("я", MaxName+1), Steps: []string{"x"}},
	}
	for _, b := range bad {
		if err := b.Normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q must be invalid, got %v", b.Name, err)
		}
	}
	if d := (Task{Timeout: "90s"}).Duration(); d != 90*time.Second {
		t.Errorf("duration: %v", d)
	}
}

func TestExpandQuotes(t *testing.T) {
	dir := t.TempDir()
	v := Vars{Worktree: dir, Repo: "my repo", Branch: "x$(touch pwned)'y", Now: time.Date(2026, 11, 23, 9, 5, 7, 0, time.UTC)}
	step := `echo {branch} {repo} {date}-{time}; echo ${HOME:+home} {worktree} {date}{time} {unknown}`
	got := Expand(step, v)
	if !strings.Contains(got, "${HOME:+home}") || !strings.Contains(got, "{unknown}") {
		t.Errorf("shell variable must stay: %s", got)
	}
	out, code := runScript(t, dir, got)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("branch name was executed as a command")
	}
	if !strings.Contains(out, "x$(touch pwned)'y my repo 2026-11-23-090507") || !strings.Contains(out, dir+" 2026-11-23090507 {unknown}") {
		t.Errorf("output: %q", out)
	}
}

func TestScriptStopsOnError(t *testing.T) {
	dir := t.TempDir()
	task := Task{Name: "t", Steps: []string{"cd sub 2>/dev/null || mkdir sub && cd sub", "pwd", "false", "touch never"}}
	out, code := runScript(t, dir, Script(task, Vars{}))
	if code != 1 {
		t.Errorf("exit code %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "▶ 1/4: cd sub") || !strings.Contains(out, filepath.Join(dir, "sub")) || !strings.Contains(out, "✗ шаг 3") {
		t.Errorf("output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "never")); err == nil {
		t.Error("steps after a failure must not run")
	}
	if strings.Contains(out, "✓ готово") {
		t.Error("a failed task must not print success")
	}
}

func TestScriptContinueOnError(t *testing.T) {
	dir := t.TempDir()
	task := Task{Name: "t", Steps: []string{"exit 3", "touch after", "false"}, ContinueOnError: true}
	// exit в шаге завершает оболочку: это поведение оболочки, а не задачи
	out, code := runScript(t, dir, Script(task, Vars{}))
	if code != 3 {
		t.Errorf("exit in a step: code %d: %s", code, out)
	}
	task.Steps = []string{"sh -c 'exit 3'", "touch after", "false"}
	out, code = runScript(t, dir, Script(task, Vars{}))
	if code != 3 {
		t.Errorf("continue_on_error must return the first failure: %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "after")); err != nil {
		t.Error("steps after a failure must run with continue_on_error")
	}
}

func TestLoadRepoFile(t *testing.T) {
	dir := t.TempDir()
	if rt := LoadRepoFile(dir); rt.Hash != "" || len(rt.Tasks) != 0 || rt.Error != "" {
		t.Errorf("no file: %+v", rt)
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, RepoFile), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"tasks":[{"name":"Тесты","steps":["go test ./..."],"scope":"other"}]}`)
	rt := LoadRepoFile(dir)
	if rt.Error != "" || len(rt.Tasks) != 1 || rt.Tasks[0].ID != "repo:0" || rt.Tasks[0].Scope != "" || len(rt.Hash) != 64 {
		t.Errorf("repo file: %+v", rt)
	}
	first := rt.Hash
	write(`{"tasks":[{"name":"Тесты","steps":["go test -race ./..."]}]}`)
	if rt := LoadRepoFile(dir); rt.Hash == first {
		t.Error("hash must change with the file")
	}
	write(`{"tasks":[{"name":"x","steps":["rm -rf /"],"cwd":"/"}]}`)
	if rt := LoadRepoFile(dir); rt.Error == "" || len(rt.Tasks) != 0 {
		t.Errorf("absolute cwd from a repository must be refused: %+v", rt)
	}
	write(`{broken`)
	if rt := LoadRepoFile(dir); rt.Error == "" || rt.Hash == "" {
		t.Errorf("broken file: %+v", rt)
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"Makefile":     "VERSION := 1\n.PHONY: build\nbuild: deps\n\tgo build\ntest:\n\tgo test\n%.o: %.c\n\tcc\nbuild:\n",
		"go.mod":       "module x\n",
		"package.json": `{"scripts":{"lint":"eslint .","dev server":"vite"}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	names := map[string][]string{}
	for _, task := range Detect(dir) {
		names[task.Name] = task.Steps
	}
	for _, want := range []string{"make build", "make test", "go build", "go test", "go vet", "npm run lint", "npm run dev server"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	for _, not := range []string{"make VERSION", "make .PHONY", "make %.o"} {
		if _, ok := names[not]; ok {
			t.Errorf("unexpected %q", not)
		}
	}
	if s := names["npm run dev server"]; len(s) != 1 || s[0] != "npm run 'dev server'" {
		t.Errorf("script name must be quoted: %v", s)
	}
	if got := Detect(t.TempDir()); got == nil || len(got) != 0 {
		t.Errorf("empty dir: %v", got)
	}
}

func TestScriptEnglish(t *testing.T) {
	task := Task{Name: "t", Steps: []string{"true", "false"}}
	out, code := runScript(t, t.TempDir(), Script(task, Vars{Lang: "en"}))
	if code != 1 || !strings.Contains(out, "✗ step 2 failed with code 1") {
		t.Errorf("english failure line: %d %q", code, out)
	}
	task.Steps = []string{"true"}
	if out, _ := runScript(t, t.TempDir(), Script(task, Vars{Lang: "en"})); !strings.Contains(out, "✓ done") {
		t.Errorf("english done line: %q", out)
	}
	if TimeoutNote("en", time.Minute) != "[stopped: timeout 1m0s]" || !strings.Contains(TimeoutNote("", time.Minute), "таймауту") {
		t.Error("timeout notes")
	}
}

// Package fsx ограничивает доступ к файловой системе разрешёнными корнями.
// Все пути из запросов проходят через Sandbox: раскрываются symlink и проверяется,
// что результат остался внутри разрешённой области.
package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

var (
	ErrOutside     = errors.New("path is outside the allowed area")
	ErrNotAbsolute = errors.New("path must be absolute")
	ErrNotRelative = errors.New("path must be relative")
)

// Sandbox хранит реальные (без symlink) пути разрешённых корней.
type Sandbox struct{ roots []string }

// NewSandbox требует, чтобы все корни существовали.
func NewSandbox(roots []string) (*Sandbox, error) {
	if len(roots) == 0 {
		return nil, errors.New("no roots configured")
	}
	s := &Sandbox{}
	for _, r := range roots {
		real, err := filepath.EvalSymlinks(r)
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", r, err)
		}
		real, err = filepath.Abs(real)
		if err != nil {
			return nil, err
		}
		s.roots = append(s.roots, real)
	}
	return s, nil
}

// Roots возвращает копию списка корней.
func (s *Sandbox) Roots() []string { return append([]string(nil), s.roots...) }

// within сообщает, что p лежит внутри base (или равен ему).
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (s *Sandbox) inRoots(p string) bool {
	for _, r := range s.roots {
		if within(r, p) {
			return true
		}
	}
	return false
}

// resolveWithin раскрывает symlink. Если объекта нет и mustExist == false,
// раскрывается только родительский каталог, а имя добавляется как есть.
func resolveWithin(allowed func(string) bool, full string, mustExist bool) (string, error) {
	real, err := filepath.EvalSymlinks(full)
	if err == nil {
		if !allowed(real) {
			return "", ErrOutside
		}
		return real, nil
	}
	if mustExist || !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	dir, name := filepath.Split(full)
	if name == "" || name == "." || name == ".." {
		return "", ErrOutside
	}
	realDir, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	if !allowed(realDir) {
		return "", ErrOutside
	}
	return filepath.Join(realDir, name), nil
}

// Resolve возвращает реальный путь существующего объекта внутри корней.
func (s *Sandbox) Resolve(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: %q", ErrNotAbsolute, p)
	}
	return resolveWithin(s.inRoots, filepath.Clean(p), true)
}

// ResolveNew как Resolve, но последний элемент пути может ещё не существовать.
func (s *Sandbox) ResolveNew(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: %q", ErrNotAbsolute, p)
	}
	return resolveWithin(s.inRoots, filepath.Clean(p), false)
}

// ResolveUnder разрешает относительный путь rel внутри base. Результат обязан
// остаться внутри base (в том числе после раскрытия symlink). base должен быть
// реальным путём внутри корней (например, результатом Resolve).
func (s *Sandbox) ResolveUnder(base, rel string, mustExist bool) (string, error) {
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("%w: %q", ErrNotAbsolute, base)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", ErrNotRelative, rel)
	}
	base = filepath.Clean(base)
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrOutside
	}
	allowed := func(p string) bool { return within(base, p) && s.inRoots(p) }
	return resolveWithin(allowed, filepath.Join(base, clean), mustExist)
}

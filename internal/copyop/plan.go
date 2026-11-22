// Package copyop планирует и выполняет копирование и перемещение файлов между панелями.
//
// Работа идёт в два шага. BuildPlan ничего не меняет на диске: он обходит выбранные
// элементы, находит конфликты (файл уже есть в назначении) и собирает данные для
// диалога. Execute строит план заново (состояние диска могло измениться), применяет
// решения пользователя и копирует файлы атомарно.
//
// Правила безопасности:
//   - пути источника и назначения проходят через Guard (корни, symlink, "..");
//   - каталог нельзя копировать внутрь самого себя;
//   - символические ссылки и все каталоги .git не копируются;
//   - конфликты типов (файл против каталога) пропускаются, ничего не перезаписывая.
package copyop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrInvalid означает неверный запрос (пустой выбор, неизвестная политика и т. п.).
	ErrInvalid = errors.New("invalid copy request")
	// ErrIntoItself возвращается при попытке скопировать каталог внутрь него самого.
	ErrIntoItself = errors.New("cannot copy a directory into itself")
)

// Причины, по которым элемент пропущен или заблокирован.
const (
	ReasonSymlink      = "symlink"
	ReasonGitDir       = "git_dir"
	ReasonSpecial      = "special_file"
	ReasonUnreadable   = "unreadable"
	ReasonTypeConflict = "type_conflict"
	ReasonDuplicate    = "duplicate_target"
	ReasonUser         = "user"
	ReasonSameFile     = "same_file"
)

const (
	maxItems   = 5000
	maxEntries = 200000
)

// Guard разрешает путь внутри base с проверкой symlink. Его реализует *fsx.Sandbox.
type Guard interface {
	ResolveUnder(base, rel string, mustExist bool) (string, error)
}

// Request описывает операцию. SrcBase и DstDir это реальные абсолютные пути каталогов
// двух панелей; Items это имена выбранных элементов относительно SrcBase.
type Request struct {
	SrcBase string
	Items   []string
	DstDir  string
	Move    bool
}

// FileInfo это данные о файле для диалога конфликта. Времена в секундах unix;
// Birth равно 0, если файловая система не отдаёт время создания.
type FileInfo struct {
	Size  int64 `json:"size"`
	MTime int64 `json:"mtime"`
	Birth int64 `json:"birth,omitempty"`
}

// Conflict это файл, который уже есть в назначении.
type Conflict struct {
	Rel           string   `json:"rel"`     // путь относительно каталога назначения
	SrcRel        string   `json:"src_rel"` // путь относительно каталога источника
	Src           FileInfo `json:"src"`
	Dst           FileInfo `json:"dst"`
	Newer         string   `json:"newer"` // "src", "dst" или "same" (по времени изменения)
	SameFile      bool     `json:"same_file,omitempty"`
	SrcCommitTime int64    `json:"src_commit_time,omitempty"` // заполняет сервер для файлов из git
}

// FileOp это один файл к копированию.
type FileOp struct {
	Rel      string `json:"rel"`
	SrcRel   string `json:"src_rel"`
	Size     int64  `json:"size"`
	Conflict bool   `json:"conflict,omitempty"`

	src, dst string
	mode     fs.FileMode
	mtime    time.Time
}

// DirOp это каталог, который нужно создать в назначении.
type DirOp struct {
	Rel string `json:"rel"`
	src string
}

// Skipped это элемент, который не копируется. Rel задан относительно источника.
type Skipped struct {
	Rel    string `json:"rel"`
	Reason string `json:"reason"`
}

// Blocked это элемент, который нельзя скопировать без потери данных (конфликт типов
// или совпадение целевых имён). Он будет пропущен при выполнении.
type Blocked struct {
	Rel    string `json:"rel"` // путь в назначении
	SrcRel string `json:"src_rel"`
	Reason string `json:"reason"`
}

// Plan это результат планирования.
type Plan struct {
	Dirs       []DirOp    `json:"dirs"`
	Files      []FileOp   `json:"files"`
	Conflicts  []Conflict `json:"conflicts"`
	Skipped    []Skipped  `json:"skipped"`
	Blocked    []Blocked  `json:"blocked"`
	TotalBytes int64      `json:"total_bytes"`

	entries int
}

// BuildPlan строит план операции, не изменяя файловую систему.
func BuildPlan(g Guard, req Request) (*Plan, error) {
	if !filepath.IsAbs(req.SrcBase) || !filepath.IsAbs(req.DstDir) {
		return nil, fmt.Errorf("%w: directories must be absolute paths", ErrInvalid)
	}
	if fi, err := os.Stat(req.DstDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: destination is not a directory", ErrInvalid)
	}
	items, err := normalizeItems(req.Items)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Dirs: []DirOp{}, Files: []FileOp{}, Conflicts: []Conflict{},
		Skipped: []Skipped{}, Blocked: []Blocked{},
	}
	seen := map[string]bool{}
	for _, it := range items {
		if err := p.addItem(g, req, it, seen); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// normalizeItems чистит пути, убирает повторы и элементы внутри уже выбранных каталогов.
func normalizeItems(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: nothing is selected", ErrInvalid)
	}
	if len(raw) > maxItems {
		return nil, fmt.Errorf("%w: too many items (limit %d)", ErrInvalid, maxItems)
	}
	seen := map[string]bool{}
	var items []string
	for _, r := range raw {
		if r == "" || strings.ContainsRune(r, 0) {
			return nil, fmt.Errorf("%w: empty item name", ErrInvalid)
		}
		clean := filepath.Clean(filepath.FromSlash(r))
		if filepath.IsAbs(clean) {
			return nil, fmt.Errorf("%w: item must be relative: %q", ErrInvalid, r)
		}
		if clean == "." {
			return nil, fmt.Errorf("%w: select the entries of a directory, not the directory itself", ErrInvalid)
		}
		if !seen[clean] {
			seen[clean] = true
			items = append(items, clean)
		}
	}
	var out []string
	for _, it := range items {
		nested := false
		for _, other := range items {
			if other != it && strings.HasPrefix(it, other+string(filepath.Separator)) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, it)
		}
	}
	return out, nil
}

func (p *Plan) skip(rel, reason string) {
	p.Skipped = append(p.Skipped, Skipped{Rel: rel, Reason: reason})
}

func (p *Plan) block(rel, srcRel, reason string) {
	p.Blocked = append(p.Blocked, Blocked{Rel: rel, SrcRel: srcRel, Reason: reason})
}

func (p *Plan) count() error {
	p.entries++
	if p.entries > maxEntries {
		return fmt.Errorf("%w: too many files (limit %d)", ErrInvalid, maxEntries)
	}
	return nil
}

// addItem добавляет в план один выбранный элемент (файл или каталог с содержимым).
func (p *Plan) addItem(g Guard, req Request, item string, seen map[string]bool) error {
	real, err := g.ResolveUnder(req.SrcBase, item, true)
	if err != nil {
		return err
	}
	name := filepath.Base(item)
	srcRel := filepath.ToSlash(item)
	if name == ".git" {
		p.skip(srcRel, ReasonGitDir)
		return nil
	}
	lst, err := os.Lstat(filepath.Join(req.SrcBase, item))
	if err != nil {
		return err
	}
	if lst.Mode()&fs.ModeSymlink != 0 {
		p.skip(srcRel, ReasonSymlink)
		return nil
	}
	if !lst.IsDir() {
		if !lst.Mode().IsRegular() {
			p.skip(srcRel, ReasonSpecial)
			return nil
		}
		if err := p.count(); err != nil {
			return err
		}
		return p.addFile(g, req, real, srcRel, name, lst, seen)
	}
	if within(real, req.DstDir) {
		return fmt.Errorf("%w: %s", ErrIntoItself, srcRel)
	}
	return filepath.WalkDir(real, func(path string, d fs.DirEntry, walkErr error) error {
		rel, err := filepath.Rel(real, path)
		if err != nil {
			return err
		}
		sRel := filepath.ToSlash(filepath.Join(item, rel))
		dRel := filepath.ToSlash(filepath.Join(name, rel))
		if walkErr != nil {
			p.skip(sRel, ReasonUnreadable)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := p.count(); err != nil {
			return err
		}
		if d.Name() == ".git" {
			p.skip(sRel, ReasonGitDir)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			p.skip(sRel, ReasonSymlink)
		case d.IsDir():
			return p.addDir(g, req, path, sRel, dRel)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				p.skip(sRel, ReasonUnreadable)
				return nil
			}
			return p.addFile(g, req, path, sRel, dRel, info, seen)
		default:
			p.skip(sRel, ReasonSpecial)
		}
		return nil
	})
}

func (p *Plan) addDir(g Guard, req Request, srcPath, srcRel, dstRel string) error {
	target, err := resolveTarget(g, req.DstDir, dstRel)
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			p.block(dstRel, srcRel, ReasonTypeConflict)
			return fs.SkipDir
		}
		return err
	}
	if fi, err := os.Lstat(target); err == nil && !fi.IsDir() {
		p.block(dstRel, srcRel, ReasonTypeConflict)
		return fs.SkipDir
	}
	p.Dirs = append(p.Dirs, DirOp{Rel: dstRel, src: srcPath})
	return nil
}

func (p *Plan) addFile(g Guard, req Request, srcPath, srcRel, dstRel string, info fs.FileInfo, seen map[string]bool) error {
	if seen[dstRel] {
		p.block(dstRel, srcRel, ReasonDuplicate)
		return nil
	}
	seen[dstRel] = true

	target, err := resolveTarget(g, req.DstDir, dstRel)
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			p.block(dstRel, srcRel, ReasonTypeConflict)
			return nil
		}
		return err
	}
	op := FileOp{
		Rel: dstRel, SrcRel: srcRel, Size: info.Size(),
		src: srcPath, dst: target, mode: info.Mode().Perm(), mtime: info.ModTime(),
	}
	dst, err := os.Lstat(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// конфликта нет
	case err != nil:
		return err
	case dst.Mode().IsRegular():
		op.Conflict = true
		c := Conflict{
			Rel: dstRel, SrcRel: srcRel,
			Src: fileInfo(srcPath, info), Dst: fileInfo(target, dst),
			Newer: compareTimes(info.ModTime(), dst.ModTime()),
		}
		if si, err := os.Stat(srcPath); err == nil {
			c.SameFile = os.SameFile(si, dst)
		}
		p.Conflicts = append(p.Conflicts, c)
	default: // каталог, ссылка без цели, устройство и т. п.
		p.block(dstRel, srcRel, ReasonTypeConflict)
		return nil
	}
	p.Files = append(p.Files, op)
	p.TotalBytes += info.Size()
	return nil
}

func fileInfo(path string, info fs.FileInfo) FileInfo {
	fi := FileInfo{Size: info.Size(), MTime: info.ModTime().Unix()}
	if t, ok := birthTime(path); ok {
		fi.Birth = t.Unix()
	}
	return fi
}

// compareTimes сравнивает времена изменения с точностью до секунды.
func compareTimes(src, dst time.Time) string {
	s, d := src.Truncate(time.Second), dst.Truncate(time.Second)
	switch {
	case s.After(d):
		return "src"
	case d.After(s):
		return "dst"
	}
	return "same"
}

// within сообщает, что p лежит внутри base или равен ему.
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolveTarget возвращает реальный путь dstRel внутри dstDir. Части пути, которых
// ещё нет, проверять не нужно (они не могут быть ссылками), поэтому проверяется
// самая длинная существующая часть, а остаток добавляется как есть.
func resolveTarget(g Guard, dstDir, dstRel string) (string, error) {
	cur := filepath.Clean(filepath.FromSlash(dstRel))
	rest := ""
	for {
		real, err := g.ResolveUnder(dstDir, cur, true)
		if err == nil {
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		parent := filepath.Dir(cur)
		if parent == "." || parent == cur {
			real, err := g.ResolveUnder(dstDir, ".", true)
			if err != nil {
				return "", err
			}
			return filepath.Join(real, rest), nil
		}
		cur = parent
	}
}

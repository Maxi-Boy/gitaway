package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
)

// Doc это одно значение в JSON-файле (0600, атомарная запись): мои задачи, доверие
// к .gitaway.json и т.п. Пустой path: только в памяти (тесты).
type Doc[T any] struct {
	mu   sync.Mutex
	path string
	v    T
}

// OpenDoc читает файл; если его нет, значение берётся из def.
func OpenDoc[T any](path string, def T) (*Doc[T], error) {
	d := &Doc[T]{path: path, v: def}
	if path == "" {
		return d, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &d.v); err != nil {
		return nil, err
	}
	return d, nil
}

// Get возвращает копию значения: изменения копии не затрагивают документ.
func (d *Doc[T]) Get() T {
	d.mu.Lock()
	defer d.mu.Unlock()
	return clone(d.v)
}

// Update меняет значение через fn и сохраняет файл. Если fn или запись вернули
// ошибку, значение остаётся прежним.
func (d *Doc[T]) Update(fn func(v *T) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := clone(d.v)
	if err := fn(&next); err != nil {
		return err
	}
	if d.path != "" {
		if err := writeJSONFile(d.path, next); err != nil {
			return err
		}
	}
	d.v = next
	return nil
}

// clone делает глубокую копию через JSON: данные маленькие, а копия защищает от
// изменения общих срезов и словарей снаружи.
func clone[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if json.Unmarshal(data, &out) != nil {
		return v
	}
	return out
}

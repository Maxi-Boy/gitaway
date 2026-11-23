package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDoc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	d, err := OpenDoc(path, map[string][]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Update(func(v *map[string][]string) error {
		(*v)["r1"] = append((*v)["r1"], "abc")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := d.Get()
	got["r1"][0] = "changed" // копия: документ не меняется
	if d.Get()["r1"][0] != "abc" {
		t.Error("Get must return a copy")
	}
	if err := d.Update(func(v *map[string][]string) error {
		(*v)["r2"] = []string{"x"}
		return errors.New("refused")
	}); err == nil || len(d.Get()) != 1 {
		t.Error("failed update must not change the value")
	}

	again, err := OpenDoc(path, map[string][]string{})
	if err != nil || len(again.Get()["r1"]) != 1 {
		t.Errorf("reopen: %v %v", again.Get(), err)
	}
	mem, _ := OpenDoc("", []int{1})
	if err := mem.Update(func(v *[]int) error { *v = append(*v, 2); return nil }); err != nil || len(mem.Get()) != 2 {
		t.Error("in-memory doc")
	}
}

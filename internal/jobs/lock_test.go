package jobs

import "testing"

func TestKeyedLock(t *testing.T) {
	var k KeyedLock
	unlock, ok := k.TryLock("repo1")
	if !ok {
		t.Fatal("first lock must succeed")
	}
	if _, ok := k.TryLock("repo1"); ok {
		t.Fatal("second lock on the same key must fail")
	}
	if u2, ok := k.TryLock("repo2"); !ok {
		t.Fatal("other key must succeed")
	} else {
		u2()
	}
	unlock()
	unlock() // повторный вызов безопасен
	if u3, ok := k.TryLock("repo1"); !ok {
		t.Fatal("lock must be free after unlock")
	} else {
		u3()
	}
}

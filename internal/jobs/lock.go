// Package jobs содержит примитивы для длительных операций.
// Сейчас это блокировка по ключу; очередь задач с потоковым выводом добавится позже.
package jobs

import "sync"

// KeyedLock не даёт запускать две операции с одним ключом одновременно
// (например, две команды git в одном репозитории).
type KeyedLock struct {
	mu   sync.Mutex
	busy map[string]struct{}
}

// TryLock занимает ключ. Если он занят, возвращает false. unlock можно вызывать повторно.
func (k *KeyedLock) TryLock(key string) (unlock func(), ok bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.busy == nil {
		k.busy = map[string]struct{}{}
	}
	if _, taken := k.busy[key]; taken {
		return nil, false
	}
	k.busy[key] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			k.mu.Lock()
			delete(k.busy, key)
			k.mu.Unlock()
		})
	}, true
}

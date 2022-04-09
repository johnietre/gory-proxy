package main

import (
	"sync"
)

type SyncMap[K any, V any] struct {
	m sync.Map
}

func (m *SyncMap[K, V]) Delete(key K) {
	m.m.Delete(key)
}

func (m *SyncMap[K, V]) Load(key K) (value V, ok bool) {
	var v any
	if v, ok = m.m.Load(key); ok {
		value = v.(V)
	}
	return
}

func (m *SyncMap[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	var v any
	if v, loaded = m.m.LoadAndDelete(key); loaded {
		value = v.(V)
	}
	return
}

func (m *SyncMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	var v any
	if v, loaded = m.m.LoadOrStore(key, value); loaded {
		actual = v.(V)
	} else {
		actual = value
	}
	return
}

func (m *SyncMap[K, V]) Range(f func(key K, value V) bool) {
	m.m.Range(func(k, v any) bool {
		return f(k.(K), v.(V))
	})
}

func (m *SyncMap[K, V]) Store(key K, value V) {
	m.m.Store(key, value)
}

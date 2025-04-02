package safety

import "sync"

type SafeMap[K comparable, V any] struct {
	m  map[K]V
	mu sync.RWMutex
}

func NewSafeMap[K comparable, V any](m ...map[K]V) *SafeMap[K, V] {
	if len(m) == 0 {
		return &SafeMap[K, V]{
			m: make(map[K]V),
		}
	}

	return &SafeMap[K, V]{
		m: m[0],
	}
}

func (s *SafeMap[K, V]) Value(key K) (V, bool) {
	s.mu.RLock()
	v, ok := s.m[key]
	s.mu.RUnlock()

	return v, ok
}

func (s *SafeMap[K, V]) Set(key K, val V) {
	s.mu.Lock()
	s.m[key] = val
	s.mu.Unlock()
}

func (s *SafeMap[K, V]) Delete(key K) {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}

func (s *SafeMap[K, V]) Range() chan struct {
	Key K
	Val V
} {
	ch := make(chan struct {
		Key K
		Val V
	})

	go func() {
		s.mu.Lock()
		for k, v := range s.m {
			ch <- struct {
				Key K
				Val V
			}{Key: k, Val: v}
		}
		s.mu.Unlock()
		close(ch)
	}()

	return ch
}

package cron

import "context"

type testStoreBackend struct {
	files map[string][]byte
}

func (b *testStoreBackend) Read(_ context.Context, path string) ([]byte, bool, error) {
	if b.files == nil {
		return nil, false, nil
	}
	val, ok := b.files[path]
	if !ok {
		return nil, false, nil
	}
	return val, true, nil
}

func (b *testStoreBackend) Write(_ context.Context, path string, data []byte) error {
	if b.files == nil {
		b.files = map[string][]byte{}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	b.files[path] = cp
	return nil
}

func (b *testStoreBackend) List(_ context.Context, prefix string) ([]StoreEntry, error) {
	var entries []StoreEntry
	if b.files == nil {
		return entries, nil
	}
	for k, v := range b.files {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			entries = append(entries, StoreEntry{Key: k, Data: v})
		}
	}
	return entries, nil
}


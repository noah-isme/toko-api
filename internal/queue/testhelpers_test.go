package queue_test

import (
	"context"
	"database/sql"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/noah-isme/backend-toko/internal/queue"
)

type memoryStore struct {
	mu      sync.Mutex
	entries map[uuid.UUID]queue.DLQEntry
}

func newMemoryStore() *memoryStore {
	return &memoryStore{entries: make(map[uuid.UUID]queue.DLQEntry)}
}

func (m *memoryStore) InsertQueueDlq(_ context.Context, entry queue.DLQEntry) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	m.entries[entry.ID] = entry
	return entry.ID, nil
}

func (m *memoryStore) DeleteQueueDlq(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
	return nil
}

func (m *memoryStore) GetQueueDlq(_ context.Context, id uuid.UUID) (queue.DLQEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[id]
	if !ok {
		return queue.DLQEntry{}, sql.ErrNoRows
	}
	return entry, nil
}

func (m *memoryStore) ListQueueDlq(_ context.Context, kind string, limit, offset int) ([]queue.DLQEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = len(m.entries)
	}
	entries := make([]queue.DLQEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		if kind != "" && entry.Kind != kind {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	if offset >= len(entries) {
		return []queue.DLQEntry{}, nil
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	out := make([]queue.DLQEntry, end-offset)
	copy(out, entries[offset:end])
	return out, nil
}

func (m *memoryStore) CountQueueDlq(_ context.Context, kind string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, entry := range m.entries {
		if kind != "" && entry.Kind != kind {
			continue
		}
		total++
	}
	return total, nil
}

func (m *memoryStore) QueueDlqSizeByKind(_ context.Context) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]int64)
	for _, entry := range m.entries {
		result[entry.Kind]++
	}
	return result, nil
}

func (m *memoryStore) snapshot() map[uuid.UUID]queue.DLQEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[uuid.UUID]queue.DLQEntry, len(m.entries))
	for id, entry := range m.entries {
		out[id] = entry
	}
	return out
}

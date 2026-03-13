package ring

import (
	"sync"
	"time"
)

type Snapshot struct {
	PID       int
	RSSKb     int64
	Timestamp time.Time
}

type Buffer struct {
	mu    sync.Mutex
	items []Snapshot
	size  int
	head  int
	count int
}

func New(size int) *Buffer {
	return &Buffer{
		items: make([]Snapshot, size),
		size:  size,
	}
}

func (b *Buffer) Push(s Snapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items[b.head] = s
	b.head = (b.head + 1) % b.size
	if b.count < b.size {
		b.count++
	}
}

// All returns all snapshots in chronological order (oldest first).
func (b *Buffer) All() []Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	result := make([]Snapshot, b.count)

	if b.count < b.size {
		copy(result, b.items[:b.count])
	} else {
		n := copy(result, b.items[b.head:])
		copy(result[n:], b.items[:b.head])
	}

	return result
}

// Latest returns the most recent snapshot.
func (b *Buffer) Latest() (Snapshot, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return Snapshot{}, false
	}

	idx := (b.head - 1 + b.size) % b.size
	return b.items[idx], true
}

func (b *Buffer) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

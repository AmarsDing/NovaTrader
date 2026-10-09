package biz

import (
	"context"
	"sort"
	"sync"
)

// MemStore 是测试用的内存账本。
type MemStore struct {
	mu     sync.Mutex
	orders map[string]Order
	fills  []Fill
	trails []Trail
	cash   map[string]Cash
	pos    map[string]Position
	events []Event
	snaps  []AccountView
}

func NewMemStore() *MemStore {
	return &MemStore{
		orders: map[string]Order{},
		cash:   map[string]Cash{},
		pos:    map[string]Position{},
	}
}

func (m *MemStore) LoadOrder(_ context.Context, clientID string) (Order, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[clientID]
	return o, ok, nil
}

func (m *MemStore) LoadCash(_ context.Context, book string) (Cash, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cash[book]
	return c, ok, nil
}

func (m *MemStore) LoadPosition(_ context.Context, book, symbol string) (Position, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pos[book+"|"+symbol]
	return p, ok, nil
}

func (m *MemStore) ListPositions(_ context.Context, book string) ([]Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Position
	for _, p := range m.pos {
		if p.Book == book {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}

func (m *MemStore) ListOpen(_ context.Context, book, symbol string) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for _, o := range m.orders {
		if o.Book != book || (symbol != "" && o.Symbol != symbol) {
			continue
		}
		if o.Status == StatusSubmitted || o.Status == StatusPartial {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListOrders(_ context.Context, book string, limit int) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for _, o := range m.orders {
		if o.Book == book {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return clipN(out, limit), nil
}

func (m *MemStore) ListFills(_ context.Context, book string, limit int) ([]Fill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Fill
	for _, f := range m.fills {
		if f.Book == book {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return clipN(out, limit), nil
}

func (m *MemStore) ListByStatus(_ context.Context, status string) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for _, o := range m.orders {
		if o.Status == status {
			out = append(out, o)
		}
	}
	return out, nil
}

func clipN[T any](in []T, limit int) []T {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}

func (m *MemStore) Commit(_ context.Context, batch Batch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range batch.Orders {
		m.orders[o.ClientOrderID] = o
	}
	m.fills = append(m.fills, batch.Fills...)
	m.trails = append(m.trails, batch.Trails...)
	if batch.Cash != nil {
		m.cash[batch.Cash.Book] = *batch.Cash
	}
	for _, p := range batch.Positions {
		m.pos[p.Book+"|"+p.Symbol] = p
	}
	m.events = append(m.events, batch.Events...)
	return nil
}

func (m *MemStore) SaveSnapshot(_ context.Context, _ string, view AccountView) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snaps = append(m.snaps, view)
	return nil
}

func (m *MemStore) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

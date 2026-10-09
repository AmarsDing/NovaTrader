package events

import "sync"

// Seen 是消费端的 event_id 去重表。同一条事件只应处理一次。
type Seen struct {
	mu    sync.Mutex
	limit int
	order []string
	ids   map[string]struct{}
}

func NewSeen(limit int) *Seen {
	if limit <= 0 {
		limit = 10000
	}
	return &Seen{
		limit: limit,
		ids:   make(map[string]struct{}),
	}
}

// First 在这个 event_id 第一次出现时返回 true。空编号一律视为已见过。
func (s *Seen) First(eventID string) bool {
	if eventID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[eventID]; ok {
		return false
	}
	s.ids[eventID] = struct{}{}
	s.order = append(s.order, eventID)
	if len(s.order) > s.limit {
		drop := s.order[0]
		s.order = s.order[1:]
		delete(s.ids, drop)
	}
	return true
}

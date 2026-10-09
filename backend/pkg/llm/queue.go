package llm

import (
	"container/heap"
	"context"
	"errors"
	"sync"
)

var errQueueFull = errors.New("queue full")

// limiter 限制同时进行的调用数。空位不够时按优先级、再按到达顺序放行。
type limiter struct {
	mu       sync.Mutex
	slots    int
	inUse    int
	maxQueue int
	seq      uint64
	waiters  waitHeap
}

type waiter struct {
	prio    Priority
	seq     uint64
	ready   chan struct{}
	granted bool
	index   int
}

func newLimiter(slots, maxQueue int) *limiter {
	return &limiter{slots: slots, maxQueue: maxQueue}
}

func (l *limiter) acquire(ctx context.Context, prio Priority) error {
	l.mu.Lock()
	if l.inUse < l.slots && len(l.waiters) == 0 {
		l.inUse++
		l.mu.Unlock()
		return nil
	}
	if len(l.waiters) >= l.maxQueue {
		l.mu.Unlock()
		return errQueueFull
	}
	l.seq++
	w := &waiter{prio: prio, seq: l.seq, ready: make(chan struct{})}
	heap.Push(&l.waiters, w)
	l.mu.Unlock()

	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		l.mu.Lock()
		if w.granted {
			l.mu.Unlock()
			l.release()
			return ctx.Err()
		}
		heap.Remove(&l.waiters, w.index)
		l.mu.Unlock()
		return ctx.Err()
	}
}

// release 把空位直接交给排在最前的等待者；没有等待者时归还空位。
func (l *limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.waiters) > 0 {
		w := heap.Pop(&l.waiters).(*waiter)
		w.granted = true
		close(w.ready)
		return
	}
	l.inUse--
}

func (l *limiter) load() (inUse, queued int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inUse, len(l.waiters)
}

type waitHeap []*waiter

func (h waitHeap) Len() int { return len(h) }
func (h waitHeap) Less(i, j int) bool {
	if h[i].prio != h[j].prio {
		return h[i].prio < h[j].prio
	}
	return h[i].seq < h[j].seq
}
func (h waitHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *waitHeap) Push(x any) {
	w := x.(*waiter)
	w.index = len(*h)
	*h = append(*h, w)
}
func (h *waitHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	w.index = -1
	return w
}

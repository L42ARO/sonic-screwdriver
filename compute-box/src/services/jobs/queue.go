package jobs

import (
	"sync"

	"sonic-screwdriver/compute-box/src/store"
)

// queue is one machine's waiting jobs, first in first out.
type queue struct {
	mu    sync.Mutex
	cond  *sync.Cond
	items []*store.Job
}

func newQueue() *queue {
	q := &queue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) push(j *store.Job) {
	q.mu.Lock()
	q.items = append(q.items, j)
	q.mu.Unlock()
	q.cond.Signal()
}

// pop blocks until a job is waiting and returns it.
func (q *queue) pop() *store.Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 {
		q.cond.Wait()
	}
	j := q.items[0]
	q.items = q.items[1:]
	return j
}

func (q *queue) ids() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.items))
	for i, j := range q.items {
		out[i] = j.ID
	}
	return out
}

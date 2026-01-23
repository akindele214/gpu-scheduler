package scheduler

import (
	"fmt"
	"sync"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

type FIFOQueue struct {
	jobs    []*types.Job
	maxSize int
	mu      sync.Mutex
}

func NewFIFOQueue(maxSize int) *FIFOQueue {
	return &FIFOQueue{
		maxSize: maxSize,
		jobs:    make([]*types.Job, maxSize),
	}
}

func (q *FIFOQueue) Enqueue(job *types.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.jobs) >= q.maxSize {
		return fmt.Errorf("queue is full (max: %d)", q.maxSize)
	}

	q.jobs = append(q.jobs, job)
	return nil
}
func (q *FIFOQueue) Dequeue() (*types.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.jobs) == 0 {
		return nil, fmt.Errorf("queue is empty")
	}
	nextJob := q.jobs[0]
	q.jobs = q.jobs[1:]
	return nextJob, nil
}
func (q *FIFOQueue) Peek() (*types.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.jobs) == 0 {
		return nil, fmt.Errorf("queue is empty")
	}

	return q.jobs[0], nil
}

func (q *FIFOQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

func (q *FIFOQueue) Remove(jobID uuid.UUID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, job := range q.jobs {
		if job.ID == jobID {
			// Remove by slicing around the element
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
			return true
		}
	}
	return false
}

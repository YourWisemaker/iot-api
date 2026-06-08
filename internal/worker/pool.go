// Package worker implements a bounded worker pool for processing jobs
// concurrently with backpressure.
package worker

import (
	"context"
	"sync"
	"sync/atomic"
)

// Job is a unit of work executed by a worker.
type Job func(ctx context.Context)

// Pool is a fixed-size pool of goroutines that consume jobs from a queue.
type Pool struct {
	workers   int
	queue     chan Job
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	processed int64
	dropped   int64
}

// NewPool creates a pool with the given number of workers and queue capacity.
// If workers <= 0 it defaults to 1. If queueSize <= 0 it defaults to workers.
func NewPool(workers, queueSize int) *Pool {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = workers
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{
		workers: workers,
		queue:   make(chan Job, queueSize),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start launches the worker goroutines. It is safe to call once.
func (p *Pool) Start() {
	p.once.Do(func() {
		for i := 0; i < p.workers; i++ {
			p.wg.Add(1)
			go p.run()
		}
	})
}

func (p *Pool) run() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job, ok := <-p.queue:
			if !ok {
				return
			}
			job(p.ctx)
			atomic.AddInt64(&p.processed, 1)
		}
	}
}

// Submit enqueues a job, blocking if the queue is full until space frees up
// or the pool is stopped. Returns false if the pool has been stopped.
func (p *Pool) Submit(job Job) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.queue <- job:
		return true
	}
}

// TrySubmit enqueues a job without blocking. It returns false (and increments
// the dropped counter) if the queue is full or the pool is stopped.
func (p *Pool) TrySubmit(job Job) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.queue <- job:
		return true
	default:
		atomic.AddInt64(&p.dropped, 1)
		return false
	}
}

// Stop signals workers to finish in-flight jobs and waits for them to exit.
func (p *Pool) Stop() {
	p.cancel()
	p.wg.Wait()
}

// Processed returns the number of jobs completed.
func (p *Pool) Processed() int64 { return atomic.LoadInt64(&p.processed) }

// Dropped returns the number of jobs dropped by TrySubmit.
func (p *Pool) Dropped() int64 { return atomic.LoadInt64(&p.dropped) }

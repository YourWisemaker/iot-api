package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolProcessesAllJobs(t *testing.T) {
	p := NewPool(4, 16)
	p.Start()
	defer p.Stop()

	const n = 100
	var counter int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		ok := p.Submit(func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			wg.Done()
		})
		if !ok {
			t.Fatal("submit rejected unexpectedly")
		}
	}
	wg.Wait()

	if got := atomic.LoadInt64(&counter); got != n {
		t.Fatalf("expected %d jobs, got %d", n, got)
	}
	if p.Processed() != n {
		t.Fatalf("expected processed %d, got %d", n, p.Processed())
	}
}

func TestPoolConcurrentSubmit(t *testing.T) {
	p := NewPool(8, 256)
	p.Start()
	defer p.Stop()

	const producers = 10
	const perProducer = 50
	var counter int64
	var wg sync.WaitGroup

	wg.Add(producers * perProducer)
	for i := 0; i < producers; i++ {
		go func() {
			for j := 0; j < perProducer; j++ {
				p.Submit(func(ctx context.Context) {
					atomic.AddInt64(&counter, 1)
					wg.Done()
				})
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&counter); got != producers*perProducer {
		t.Fatalf("expected %d, got %d", producers*perProducer, got)
	}
}

func TestSubmitAfterStop(t *testing.T) {
	p := NewPool(2, 2)
	p.Start()
	p.Stop()

	if p.Submit(func(ctx context.Context) {}) {
		t.Fatal("expected submit to fail after stop")
	}
}

func TestTrySubmitDropsWhenFull(t *testing.T) {
	// Single worker, queue of 1, blocking jobs to force the queue full.
	p := NewPool(1, 1)
	p.Start()
	defer p.Stop()

	release := make(chan struct{})
	// Occupy the worker.
	p.Submit(func(ctx context.Context) { <-release })
	// Fill the queue slot.
	p.Submit(func(ctx context.Context) { <-release })

	// Give the worker a moment to pick up the first job.
	time.Sleep(50 * time.Millisecond)

	if p.TrySubmit(func(ctx context.Context) {}) {
		t.Fatal("expected TrySubmit to drop when full")
	}
	if p.Dropped() == 0 {
		t.Fatal("expected dropped counter to increment")
	}
	close(release)
}

func TestDefaultsApplied(t *testing.T) {
	p := NewPool(0, 0)
	if p.workers != 1 {
		t.Fatalf("expected default 1 worker, got %d", p.workers)
	}
	if cap(p.queue) != 1 {
		t.Fatalf("expected default queue cap 1, got %d", cap(p.queue))
	}
}

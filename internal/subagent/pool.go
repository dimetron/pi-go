package subagent

import (
	"context"
	"fmt"
)

// Pool manages concurrency for subagent processes using a buffered channel semaphore.
type Pool struct {
	sem  chan struct{}
	size int
}

// NewPool creates a concurrency pool that allows at most maxConcurrent active agents.
func NewPool(maxConcurrent int) *Pool {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Pool{
		sem:  make(chan struct{}, maxConcurrent),
		size: maxConcurrent,
	}
}

// Acquire blocks until a slot is available or the context is cancelled.
func (p *Pool) Acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("pool acquire cancelled: %w", ctx.Err())
	}
}

// Release returns a slot to the pool. Must be called after Acquire.
func (p *Pool) Release() {
	<-p.sem
}

// Size returns the maximum concurrency limit.
func (p *Pool) Size() int {
	return p.size
}

// Available returns the number of currently available slots.
func (p *Pool) Available() int {
	return p.size - len(p.sem)
}

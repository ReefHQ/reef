package main

import (
	"context"
	"reef/internal/monitoring"
	"sync/atomic"
	"time"
)

// The policy generation and enabled bit share one atomic word. A sample from a
// disabled/replaced policy can never be reused after a rapid disable/enable.
type monitorResult struct {
	generation uint64
	value      monitoring.Sample
}
type monitorCollector struct {
	state  atomic.Uint64
	latest atomic.Pointer[monitorResult]
	wake   chan struct{}
}

func newMonitorCollector(ctx context.Context, read func(context.Context) monitoring.Sample) *monitorCollector {
	c := &monitorCollector{wake: make(chan struct{}, 1)}
	go func() {
		var sequence uint64
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.wake:
			case <-timer.C:
			}
			generation := c.state.Load()
			if generation&1 != 0 {
				sample := read(ctx)
				sequence++
				sample.Sequence = sequence
				c.latest.Store(&monitorResult{generation: generation, value: sample})
			}
			timer.Reset(monitoring.SampleInterval)
		}
	}()
	return c
}
func (c *monitorCollector) set(enabled bool) {
	for {
		old := c.state.Load()
		if (old&1 != 0) == enabled {
			return
		}
		next := (old + 2) &^ uint64(1)
		if enabled {
			next |= 1
		}
		if c.state.CompareAndSwap(old, next) {
			select {
			case c.wake <- struct{}{}:
			default:
			}
			return
		}
	}
}
func (c *monitorCollector) sample() *monitoring.Sample {
	generation := c.state.Load()
	if generation&1 == 0 {
		return nil
	}
	sample := c.latest.Load()
	if sample == nil || sample.generation != generation {
		return nil
	}
	return &sample.value
}

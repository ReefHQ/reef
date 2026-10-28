package main

import (
	"context"
	"reef/internal/monitoring"
	"testing"
)

func TestMonitorPolicyGenerationInvalidatesOldSamples(t *testing.T) {
	c := &monitorCollector{wake: make(chan struct{}, 1)}
	if c.sample() != nil {
		t.Fatal("disabled sample")
	}
	c.set(true)
	generation := c.state.Load()
	c.latest.Store(&monitorResult{generation: generation, value: monitoring.Sample{Sequence: 1}})
	if c.sample() == nil {
		t.Fatal("current sample missing")
	}
	c.set(false)
	if c.sample() != nil {
		t.Fatal("disabled sample retained")
	}
	c.set(true)
	if c.sample() != nil {
		t.Fatal("old enabled period reused")
	}
	c.latest.Store(&monitorResult{generation: c.state.Load(), value: monitoring.Sample{Sequence: 2}})
	if c.sample().Sequence != 2 {
		t.Fatal("new generation missing")
	}
}
func TestDisabledCollectorCancelsWithoutInspection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newMonitorCollector(ctx, func(context.Context) monitoring.Sample {
		t.Error("inspection while disabled")
		return monitoring.Sample{}
	})
}

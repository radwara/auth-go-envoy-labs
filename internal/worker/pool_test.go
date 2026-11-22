package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	decision "authzlab/api/decision/v1"
)

func TestPoolBoundsActiveAndQueuedJobs(t *testing.T) {
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	pool := New(2, 1, 4, func(ctx context.Context, _ Input) (*decision.CheckResponse, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return &decision.CheckResponse{Allowed: true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer pool.Close()
	for i := 0; i < 2; i++ {
		if _, err := pool.Submit(Input{}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	queued, err := pool.Submit(Input{})
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" {
		t.Fatalf("status = %s", queued.Status)
	}
	if _, err := pool.Submit(Input{}); !errors.Is(err, ErrFull) {
		t.Fatalf("submit error = %v", err)
	}
	close(release)
	pool.Close()
	job, ok := pool.Get(queued.ID)
	if !ok || job.Status != "succeeded" || job.Result == nil || !job.Result.Allowed {
		t.Fatalf("queued job after shutdown = %+v", job)
	}
}

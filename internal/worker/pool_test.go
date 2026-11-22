package worker

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
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

func TestPoolEvictsOnlyTerminalJobsAtRetentionCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		pool := New(1, 2, 2, func(_ context.Context, input Input) (*decision.CheckResponse, error) {
			if input.Subject == "active" {
				<-release
			}
			return &decision.CheckResponse{Allowed: true}, nil
		})
		defer pool.Close()
		defer close(release)

		completed, err := pool.Submit(Input{Subject: "completed"})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if job, ok := pool.Get(completed.ID); !ok || job.Status != "succeeded" {
			t.Fatalf("completed job before reaching capacity = %+v, found %t", job, ok)
		}
		active, err := pool.Submit(Input{Subject: "active"})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, ok := pool.Get(completed.ID); !ok {
			t.Fatal("completed job was evicted before reaching capacity")
		}

		queued, err := pool.Submit(Input{Subject: "queued"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := pool.Get(completed.ID); ok {
			t.Fatal("completed job was retained after submitting at capacity")
		}
		if _, err := pool.Submit(Input{}); !errors.Is(err, ErrFull) {
			t.Fatalf("submit with only non-terminal jobs at capacity = %v, want ErrFull", err)
		}
		if job, ok := pool.Get(active.ID); !ok || job.Status != "running" {
			t.Fatalf("running job at capacity = %+v, found %t", job, ok)
		}
		if job, ok := pool.Get(queued.ID); !ok || job.Status != "queued" {
			t.Fatalf("queued job at capacity = %+v, found %t", job, ok)
		}
	})
}

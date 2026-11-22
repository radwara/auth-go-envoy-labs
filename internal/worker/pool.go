package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	decision "authzlab/api/decision/v1"
	"authzlab/internal/policy"
)

var (
	ErrFull   = errors.New("job capacity reached")
	ErrClosed = errors.New("job pool closed")
)

type Input = policy.Match

type Result struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	Replica string `json:"replica"`
}

type Job struct {
	ID     uint64  `json:"id"`
	Status string  `json:"status"`
	Input  Input   `json:"input"`
	Result *Result `json:"result,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type Pool struct {
	mu        sync.Mutex
	workers   sync.WaitGroup
	queue     chan Job
	jobs      map[uint64]Job
	capacity  int
	completed []uint64
	closed    bool
	next      uint64
	check     func(context.Context, Input) (*decision.CheckResponse, error)
}

func New(workers, queueSize, capacity int, check func(context.Context, Input) (*decision.CheckResponse, error)) *Pool {
	p := &Pool{queue: make(chan Job, queueSize), jobs: make(map[uint64]Job), capacity: capacity, check: check}
	for i := 0; i < workers; i++ {
		p.workers.Add(1)
		go p.run()
	}
	return p
}

func (p *Pool) Submit(input Input) (Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return Job{}, ErrClosed
	}
	if len(p.jobs) >= p.capacity && len(p.completed) == 0 {
		return Job{}, ErrFull
	}
	job := Job{ID: p.next + 1, Status: "queued", Input: input}
	select {
	case p.queue <- job:
		if len(p.jobs) >= p.capacity {
			delete(p.jobs, p.completed[0])
			p.completed = p.completed[1:]
		}
		p.next = job.ID
		p.jobs[job.ID] = job
		return job, nil
	default:
		return Job{}, ErrFull
	}
}

func (p *Pool) Get(id uint64) (Job, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	job, ok := p.jobs[id]
	if job.Result != nil {
		result := *job.Result
		job.Result = &result
	}
	return job, ok
}

func (p *Pool) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.queue)
	}
	p.mu.Unlock()
	p.workers.Wait()
}

func (p *Pool) run() {
	defer p.workers.Done()
	for job := range p.queue {
		p.mu.Lock()
		job.Status = "running"
		p.jobs[job.ID] = job
		p.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		response, err := p.check(ctx, job.Input)
		cancel()
		if err != nil {
			job.Status = "failed"
			job.Error = "decision service unavailable"
		} else {
			job.Status = "succeeded"
			job.Result = &Result{Allowed: response.GetAllowed(), Reason: response.GetReason(), Replica: response.GetReplica()}
		}

		p.mu.Lock()
		p.jobs[job.ID] = job
		p.completed = append(p.completed, job.ID)
		p.mu.Unlock()
	}
}

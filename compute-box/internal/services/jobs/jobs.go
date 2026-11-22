// Package jobs accepts operation runs, queues them per machine, runs them one at a time
// with the brain, and streams their progress.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/brain"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// How long a queued job waits for its machine to come back before failing.
const reconnectGrace = 2 * time.Minute

type Service struct {
	store      *store.Store
	hub        *hub.Hub
	brain      brain.Brain
	screensDir string
	jobTimeout time.Duration
	maxSteps   int

	streams streams
	mu      sync.Mutex
	queues  map[string]*queue
}

type Options struct {
	ScreensDir string
	JobTimeout time.Duration
	MaxSteps   int
}

func New(s *store.Store, h *hub.Hub, b brain.Brain, opt Options) *Service {
	return &Service{
		store: s, hub: h, brain: b,
		screensDir: opt.ScreensDir, jobTimeout: opt.JobTimeout, maxSteps: opt.MaxSteps,
		streams: streams{byID: map[string]*stream{}},
		queues:  map[string]*queue{},
	}
}

// Recover fails jobs a previous process left running and re-queues waiting ones.
func (s *Service) Recover(ctx context.Context) error {
	if n, err := s.store.InterruptRunning(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Printf("jobs: %d job(s) were running when the compute-box stopped; marked failed", n)
	}
	queued, err := s.store.ListJobs(ctx, s.store.UserID, "", []string{store.StatusQueued}, 1000)
	if err != nil {
		return err
	}
	for i := len(queued) - 1; i >= 0; i-- { // oldest first
		j := queued[i]
		s.streams.create(j.ID).publish(Event{"queued", map[string]any{"job_id": j.ID}}, false)
		s.queueFor(j.Machine).push(j)
	}
	return nil
}

// Submission is a request to run an operation.
type Submission struct {
	Project        string
	Operation      string
	Machine        string
	Input          json.RawMessage
	IdempotencyKey string
}

// Submit validates and queues a job. With an idempotency key that was used before, it
// returns the original job instead of creating another.
func (s *Service) Submit(ctx context.Context, sub Submission) (*store.Job, error) {
	userID := s.store.UserID
	if sub.IdempotencyKey != "" {
		if j, err := s.store.JobByIdempotencyKey(ctx, userID, sub.IdempotencyKey); err != nil {
			return nil, err
		} else if j != nil {
			if j.Project != sub.Project || j.Operation != sub.Operation {
				return nil, utils.NewError(http.StatusConflict, "idempotency_conflict",
					"this Idempotency-Key was already used for a different operation")
			}
			return j, nil
		}
	}
	def, err := s.store.GetOperation(ctx, userID, sub.Project, sub.Operation)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, utils.NewError(http.StatusNotFound, "unknown_operation",
			"operation "+sub.Operation+" does not exist in project "+sub.Project)
	}
	if sub.Machine == "" {
		return nil, utils.NewError(http.StatusUnprocessableEntity, "invalid_input", "machine is required")
	}
	if len(sub.Input) == 0 {
		sub.Input = json.RawMessage("{}")
	}
	if err := utils.Validate("input", def.Input, sub.Input); err != nil {
		return nil, utils.NewError(http.StatusUnprocessableEntity, "invalid_input", err.Error())
	}
	if !s.hub.Online(sub.Machine) {
		return nil, utils.NewError(http.StatusServiceUnavailable, "machine_offline", sub.Machine+" is not connected")
	}

	j := &store.Job{
		ID: utils.NewID("job"), UserID: userID,
		Project: sub.Project, Operation: sub.Operation, Machine: sub.Machine,
		Input: sub.Input, Definition: *def, Status: store.StatusQueued,
		IdempotencyKey: sub.IdempotencyKey, CreatedAt: utils.Now(),
	}
	if err := s.store.CreateJob(ctx, j); err != nil {
		return nil, err
	}
	s.streams.create(j.ID).publish(Event{"queued", map[string]any{"job_id": j.ID}}, false)
	s.queueFor(j.Machine).push(j)
	log.Printf("jobs: %s queued: %s/%s on %s", j.ID, j.Project, j.Operation, j.Machine)
	return j, nil
}

// Subscribe returns a job's events so far and a channel of the rest. For a job that
// finished before this process started, it rebuilds the events from the database.
func (s *Service) Subscribe(ctx context.Context, jobID string) ([]Event, <-chan Event, func(), error) {
	if st := s.streams.get(jobID); st != nil {
		h, ch, cancel := st.subscribe()
		return h, ch, cancel, nil
	}
	j, err := s.store.GetJob(ctx, s.store.UserID, jobID)
	if err != nil {
		return nil, nil, nil, err
	}
	if j == nil {
		return nil, nil, nil, utils.NewError(http.StatusNotFound, "not_found", "no job "+jobID)
	}
	steps, err := s.store.ListSteps(ctx, jobID)
	if err != nil {
		return nil, nil, nil, err
	}
	events := []Event{{"queued", map[string]any{"job_id": j.ID}}}
	for _, st := range steps {
		events = append(events, Event{"step", map[string]any{"job_id": j.ID, "n": st.N, "description": st.Description}})
	}
	if j.Done() {
		events = append(events, finalEvent(j))
	}
	ch := make(chan Event)
	close(ch)
	return events, ch, func() {}, nil
}

// QueuedFor lists the job ids waiting on a machine, oldest first.
func (s *Service) QueuedFor(machine string) []string {
	return s.queueFor(machine).ids()
}

func (s *Service) queueFor(machine string) *queue {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[machine]
	if q == nil {
		q = newQueue()
		s.queues[machine] = q
		go s.work(machine, q)
	}
	return q
}

// work runs one machine's jobs, one at a time, forever.
func (s *Service) work(machine string, q *queue) {
	for {
		j := q.pop()
		s.run(j)
	}
}

func (s *Service) run(j *store.Job) {
	st := s.streams.get(j.ID)
	if st == nil {
		st = s.streams.create(j.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.jobTimeout)
	defer cancel()

	// The machine may have dropped while the job waited; give it a moment to come back.
	waitCtx, waitCancel := context.WithTimeout(ctx, reconnectGrace)
	online := s.hub.WaitOnline(waitCtx, j.Machine)
	waitCancel()
	if !online {
		s.finish(j, st, nil, &brain.Error{Code: "machine_offline", Message: j.Machine + " is not connected"})
		return
	}

	model := s.brain.Model()
	if err := s.store.StartJob(ctx, j.ID, model); err != nil {
		log.Printf("jobs: %s: %v", j.ID, err)
	}
	st.publish(Event{"started", map[string]any{"job_id": j.ID, "model": model}}, false)
	s.hub.Tell(j.Machine, map[string]string{"type": "job_start", "job_id": j.ID, "operation": j.Operation})
	defer s.hub.Tell(j.Machine, map[string]string{"type": "job_end", "job_id": j.ID})
	log.Printf("jobs: %s started with %s", j.ID, model)

	screen, _ := s.hub.ScreenOf(j.Machine)
	programs, err := s.store.ProjectPrograms(ctx, j.Project)
	if err != nil {
		s.finish(j, st, nil, err)
		return
	}
	installed, err := s.store.MachinePrograms(ctx, j.Machine)
	if err != nil {
		s.finish(j, st, nil, err)
		return
	}
	env := &env{svc: s, job: j, stream: st, programs: programs, installed: installed}
	output, err := s.brain.Run(ctx, brain.Task{
		JobID: j.ID, Project: j.Project, Definition: j.Definition, Input: j.Input,
		Screen: screen, MaxSteps: s.maxSteps,
	}, env)
	if err == nil {
		if verr := utils.Validate("output", j.Definition.Output, output); verr != nil {
			err = &brain.Error{Code: "output_invalid", Message: verr.Error()}
		}
	}
	s.finish(j, st, output, err)
}

// finish records the outcome and ends the stream.
func (s *Service) finish(j *store.Job, st *stream, output json.RawMessage, err error) {
	j.Output = output
	j.Status = store.StatusSucceeded
	if err != nil {
		j.Status = store.StatusFailed
		j.Output = nil
		var be *brain.Error
		switch {
		case errors.As(err, &be):
			j.ErrorCode, j.ErrorMessage = be.Code, be.Message
		case errors.Is(err, hub.ErrDisconnected), errors.Is(err, hub.ErrOffline):
			j.ErrorCode, j.ErrorMessage = "machine_disconnected", j.Machine+" disconnected during the job"
			j.OutcomeUnknown = true // a write may or may not have happened
		case errors.Is(err, context.DeadlineExceeded):
			j.ErrorCode, j.ErrorMessage = "timeout", "the job ran longer than "+s.jobTimeout.String()
			j.OutcomeUnknown = true
		default:
			j.ErrorCode, j.ErrorMessage = "agent_gave_up", err.Error()
		}
	}
	if serr := s.store.FinishJob(context.Background(), j); serr != nil {
		log.Printf("jobs: %s: saving result: %v", j.ID, serr)
	}
	if fresh, _ := s.store.GetJob(context.Background(), j.UserID, j.ID); fresh != nil {
		j = fresh
	}
	st.publish(finalEvent(j), true)
	s.streams.forgetLater(j.ID)
	log.Printf("jobs: %s %s %s (%d steps, $%.5f)", j.ID, j.Status, j.ErrorCode, j.Steps, j.CostUSD)
}

func finalEvent(j *store.Job) Event {
	if j.Status == store.StatusSucceeded {
		return Event{"succeeded", map[string]any{
			"job_id": j.ID, "output": j.Output, "steps": j.Steps, "cost_usd": j.CostUSD, "model": j.Model}}
	}
	return Event{j.Status, map[string]any{
		"job_id":          j.ID,
		"error":           map[string]any{"code": j.ErrorCode, "message": j.ErrorMessage},
		"outcome_unknown": j.OutcomeUnknown, "steps": j.Steps, "cost_usd": j.CostUSD,
	}}
}

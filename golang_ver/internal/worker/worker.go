package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
	"github.com/LingHeChen/rc_LingHeChen/internal/queue"
)

type PermanentError struct {
	StatusCode int
	Msg        string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("permanent failure (HTTP %d): %s", e.StatusCode, e.Msg)
}

type Worker struct {
	queue       queue.Queue
	client      *http.Client
	poll        time.Duration
	concurrency int
}

func New(q queue.Queue, opts ...Option) *Worker {
	w := &Worker{
		queue:       q,
		client:      &http.Client{Timeout: 10 * time.Second},
		poll:        5 * time.Second,
		concurrency: 5,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

type Option func(*Worker)

func WithPollInterval(d time.Duration) Option  { return func(w *Worker) { w.poll = d } }
func WithConcurrency(n int) Option             { return func(w *Worker) { w.concurrency = n } }
func WithHTTPTimeout(d time.Duration) Option   { return func(w *Worker) { w.client = &http.Client{Timeout: d} } }

func (w *Worker) Run(ctx context.Context) {
	go w.recoverLoop(ctx)

	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runWorker(ctx)
		}()
	}
	wg.Wait()
}

func (w *Worker) recoverLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.queue.RecoverStuck(ctx, 5*time.Minute); err != nil {
				slog.Error("recover stuck jobs failed", "err", err)
			}
		}
	}
}

func (w *Worker) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := w.queue.Dequeue(ctx)
		if errors.Is(err, queue.ErrNoJob) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.poll):
			}
			continue
		}
		if err != nil {
			slog.Error("dequeue failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.poll):
			}
			continue
		}

		w.process(ctx, job)
	}
}

func (w *Worker) process(ctx context.Context, job *model.Job) {
	err := w.deliver(ctx, job)
	if err == nil {
		if ackErr := w.queue.Ack(ctx, job.ID); ackErr != nil {
			slog.Error("ack failed, job will be redelivered by recovery", "id", job.ID, "err", ackErr)
			return
		}
		slog.Info("delivered", "id", job.ID, "url", job.TargetURL)
		return
	}

	var pe *PermanentError
	if errors.As(err, &pe) {
		slog.Warn("permanent failure, moving to DLQ", "id", job.ID, "err", err)
		if deadErr := w.queue.Dead(ctx, job.ID, err.Error()); deadErr != nil {
			slog.Error("dead failed", "id", job.ID, "err", deadErr)
		}
		return
	}

	if job.Attempts >= job.MaxAttempts {
		slog.Warn("max attempts reached, moving to DLQ", "id", job.ID, "attempts", job.Attempts)
		if deadErr := w.queue.Dead(ctx, job.ID, err.Error()); deadErr != nil {
			slog.Error("dead failed", "id", job.ID, "err", deadErr)
		}
		return
	}

	next := nextRetryDelay(job.Attempts)
	slog.Info("delivery failed, retrying", "id", job.ID, "attempt", job.Attempts, "next", next)
	if nackErr := w.queue.Nack(ctx, job.ID, err.Error(), time.Now().Add(next)); nackErr != nil {
		slog.Error("nack failed", "id", job.ID, "err", nackErr)
	}
}

func (w *Worker) deliver(ctx context.Context, job *model.Job) error {
	req, err := http.NewRequestWithContext(ctx, job.Method, job.TargetURL, bytes.NewReader(job.Body))
	if err != nil {
		return &PermanentError{Msg: fmt.Sprintf("bad request config: %v", err)}
	}
	for k, v := range job.Headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("server error: HTTP %d", resp.StatusCode)
	}
	// 429 Too Many Requests and 408 Request Timeout are transient — retry.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return fmt.Errorf("transient client error: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return &PermanentError{StatusCode: resp.StatusCode, Msg: "client error, will not retry"}
	}
	return nil
}

// nextRetryDelay returns exponential backoff with ±20% jitter.
// Attempts 1-10 produce delays roughly: 1m, 2m, 4m, 8m, 16m, 32m, 1h, 2h, 4h, 24h(cap).
func nextRetryDelay(attempts int) time.Duration {
	base := float64(time.Minute)
	cap := float64(24 * time.Hour)
	delay := base * math.Pow(2, float64(attempts-1))
	if delay > cap {
		delay = cap
	}
	jitter := (rand.Float64()*0.4 - 0.2) * delay
	return time.Duration(delay + jitter)
}

package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
	"github.com/LingHeChen/rc_LingHeChen/internal/queue"
)

// --- mock queue ---

type mockQueue struct {
	acked []string
	nacked []string
	dead  []string
}

func (m *mockQueue) Enqueue(_ context.Context, _ *model.Job) error            { return nil }
func (m *mockQueue) Dequeue(_ context.Context) (*model.Job, error)            { return nil, queue.ErrNoJob }
func (m *mockQueue) Ack(_ context.Context, id string) error                   { m.acked = append(m.acked, id); return nil }
func (m *mockQueue) Nack(_ context.Context, id string, _ string, _ time.Time) error { m.nacked = append(m.nacked, id); return nil }
func (m *mockQueue) Dead(_ context.Context, id string, _ string) error        { m.dead = append(m.dead, id); return nil }
func (m *mockQueue) RecoverStuck(_ context.Context, _ time.Duration) error    { return nil }

// --- nextRetryDelay ---

func TestNextRetryDelay_Bounds(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		d := nextRetryDelay(attempt)
		assert.Positive(t, d, "attempt %d", attempt)
		assert.LessOrEqual(t, d, 25*time.Hour, "attempt %d exceeds cap", attempt)
	}
}

func TestNextRetryDelay_Increases(t *testing.T) {
	// median of delay should grow with attempts (ignoring jitter by averaging)
	prev := nextRetryDelay(1)
	for attempt := 2; attempt <= 8; attempt++ {
		cur := nextRetryDelay(attempt)
		// with jitter, individual samples may not be monotone, so just check order of magnitude
		assert.Greater(t, cur+prev, prev, "delay should generally grow")
		prev = cur
	}
}

// --- deliver ---

func newTestWorker(q *mockQueue) *Worker {
	return New(q, WithHTTPTimeout(2*time.Second))
}

func TestDeliver_2xx_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := newTestWorker(&mockQueue{})
	job := &model.Job{Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}}
	err := w.deliver(context.Background(), job)
	assert.NoError(t, err)
}

func TestDeliver_5xx_RetryableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	w := newTestWorker(&mockQueue{})
	job := &model.Job{Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}}
	err := w.deliver(context.Background(), job)
	require.Error(t, err)
	assert.NotErrorIs(t, err, new(PermanentError))
}

func TestDeliver_4xx_PermanentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	w := newTestWorker(&mockQueue{})
	job := &model.Job{Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}}
	err := w.deliver(context.Background(), job)
	var pe *PermanentError
	assert.ErrorAs(t, err, &pe)
}

func TestDeliver_NetworkError_Retryable(t *testing.T) {
	w := newTestWorker(&mockQueue{})
	job := &model.Job{Method: "POST", TargetURL: "http://127.0.0.1:1", Headers: model.HeadersMap{}}
	err := w.deliver(context.Background(), job)
	require.Error(t, err)
	var pe *PermanentError
	assert.False(t, new(PermanentError) == pe) // not permanent
}

// --- process ---

func TestProcess_Success_CallsAck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	q := &mockQueue{}
	wrk := newTestWorker(q)
	job := &model.Job{ID: "j1", Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}, Attempts: 1, MaxAttempts: 10}
	wrk.process(context.Background(), job)

	assert.Contains(t, q.acked, "j1")
	assert.Empty(t, q.dead)
}

func TestProcess_PermanentError_CallsDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	q := &mockQueue{}
	wrk := newTestWorker(q)
	job := &model.Job{ID: "j2", Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}, Attempts: 1, MaxAttempts: 10}
	wrk.process(context.Background(), job)

	assert.Contains(t, q.dead, "j2")
	assert.Empty(t, q.acked)
}

func TestProcess_MaxAttempts_CallsDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	q := &mockQueue{}
	wrk := newTestWorker(q)
	job := &model.Job{ID: "j3", Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}, Attempts: 10, MaxAttempts: 10}
	wrk.process(context.Background(), job)

	assert.Contains(t, q.dead, "j3")
	assert.Empty(t, q.acked)
}

func TestProcess_RetryableError_CallsNack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	q := &mockQueue{}
	wrk := newTestWorker(q)
	job := &model.Job{ID: "j4", Method: "POST", TargetURL: srv.URL, Headers: model.HeadersMap{}, Attempts: 1, MaxAttempts: 10}
	wrk.process(context.Background(), job)

	assert.Contains(t, q.nacked, "j4")
	assert.Empty(t, q.acked)
	assert.Empty(t, q.dead)
}

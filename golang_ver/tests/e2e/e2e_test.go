//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/LingHeChen/rc_LingHeChen/internal/api"
	"github.com/LingHeChen/rc_LingHeChen/internal/model"
	pgqueue "github.com/LingHeChen/rc_LingHeChen/internal/queue/postgres"
	"github.com/LingHeChen/rc_LingHeChen/internal/vendor"
	"github.com/LingHeChen/rc_LingHeChen/internal/worker"
)

func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	return db
}

func TestFullDeliveryFlow(t *testing.T) {
	db := openDB(t)
	gin.SetMode(gin.TestMode)

	// Target server that records received calls.
	var callCount atomic.Int32
	var capturedBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	q := pgqueue.New(db)
	vs := vendor.NewStore(db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wrk := worker.New(q,
		worker.WithPollInterval(100*time.Millisecond),
		worker.WithConcurrency(2),
		worker.WithHTTPTimeout(5*time.Second),
	)
	go wrk.Run(ctx)

	// Create vendor pointing at the test target.
	vendorName := "e2e-test-vendor"
	err := vs.Create(ctx, &model.VendorConfig{
		Name:      vendorName,
		TargetURL: target.URL,
		Method:    "POST",
		Headers:   model.HeadersMap{"Content-Type": "application/json"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { vs.Delete(context.Background(), vendorName) })

	// Set up the HTTP router.
	r := gin.New()
	api.New(q, vs).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// POST a notification.
	body := `{"body":{"event":"user.registered","user_id":"u42"}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/notifications/"+vendorName, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	// Wait for worker to deliver (up to 5s).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if callCount.Load() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.EqualValues(t, 1, callCount.Load(), "target should have received exactly one call")
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(capturedBody), &parsed))
	assert.Equal(t, "user.registered", parsed["event"])
}

func TestFullDeliveryFlow_WithBodyTemplate(t *testing.T) {
	db := openDB(t)
	gin.SetMode(gin.TestMode)

	var capturedBody string
	var callCount atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	q := pgqueue.New(db)
	vs := vendor.NewStore(db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wrk := worker.New(q, worker.WithPollInterval(100*time.Millisecond), worker.WithConcurrency(2))
	go wrk.Run(ctx)

	vendorName := "e2e-tpl-vendor"
	err := vs.Create(ctx, &model.VendorConfig{
		Name:         vendorName,
		TargetURL:    target.URL,
		Method:       "POST",
		Headers:      model.HeadersMap{"Content-Type": "application/json"},
		BodyTemplate: `{"crm_id":"{{.user_id}}","status":"paid"}`,
	})
	require.NoError(t, err)
	t.Cleanup(func() { vs.Delete(context.Background(), vendorName) })

	r := gin.New()
	api.New(q, vs).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	body := `{"body":{"user_id":"u99"}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/notifications/"+vendorName, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if callCount.Load() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.EqualValues(t, 1, callCount.Load())
	assert.Contains(t, capturedBody, `"crm_id":"u99"`)
	assert.Contains(t, capturedBody, `"status":"paid"`)
}

func TestIdempotencyKeyIsScopedByVendor(t *testing.T) {
	db := openDB(t)
	q := pgqueue.New(db)
	ctx := context.Background()
	sharedKey := uuid.NewString()

	jobs := []*model.Job{
		{
			ID: uuid.NewString(), VendorName: "crm", IdempotencyKey: sharedKey,
			TargetURL: "https://crm.example.com", Method: "POST", Headers: model.HeadersMap{},
			MaxAttempts: 10,
		},
		{
			ID: uuid.NewString(), VendorName: "inventory", IdempotencyKey: sharedKey,
			TargetURL: "https://inventory.example.com", Method: "POST", Headers: model.HeadersMap{},
			MaxAttempts: 10,
		},
	}

	for _, job := range jobs {
		require.NoError(t, q.Enqueue(ctx, job))
	}
	t.Cleanup(func() {
		db.WithContext(context.Background()).Where("id IN ?", []string{jobs[0].ID, jobs[1].ID}).
			Delete(&model.Job{})
	})

	var count int64
	require.NoError(t, db.Model(&model.Job{}).
		Where("idempotency_key = ?", sharedKey).
		Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
	"github.com/LingHeChen/rc_LingHeChen/internal/queue"
	"github.com/LingHeChen/rc_LingHeChen/internal/vendor"
)

func init() { gin.SetMode(gin.TestMode) }

// --- mocks ---

type mockQueue struct{}

func (m *mockQueue) Enqueue(_ context.Context, _ *model.Job) error            { return nil }
func (m *mockQueue) Dequeue(_ context.Context) (*model.Job, error)            { return nil, queue.ErrNoJob }
func (m *mockQueue) Ack(_ context.Context, _ string) error                    { return nil }
func (m *mockQueue) Nack(_ context.Context, _ string, _ string, _ time.Time) error { return nil }
func (m *mockQueue) Dead(_ context.Context, _ string, _ string) error         { return nil }
func (m *mockQueue) RecoverStuck(_ context.Context, _ time.Duration) error    { return nil }

type mockVendorStore struct {
	store map[string]*model.VendorConfig
}

func newMockVendorStore(vs ...*model.VendorConfig) *mockVendorStore {
	m := &mockVendorStore{store: make(map[string]*model.VendorConfig)}
	for _, v := range vs {
		m.store[v.Name] = v
	}
	return m
}

func (m *mockVendorStore) Create(_ context.Context, v *model.VendorConfig) error {
	v.ID = "test-id"
	m.store[v.Name] = v
	return nil
}
func (m *mockVendorStore) Get(_ context.Context, name string) (*model.VendorConfig, error) {
	v, ok := m.store[name]
	if !ok {
		return nil, vendor.ErrNotFound
	}
	return v, nil
}
func (m *mockVendorStore) List(_ context.Context) ([]*model.VendorConfig, error) {
	var result []*model.VendorConfig
	for _, v := range m.store {
		result = append(result, v)
	}
	return result, nil
}
func (m *mockVendorStore) Update(_ context.Context, name string, v *model.VendorConfig) error {
	if _, ok := m.store[name]; !ok {
		return vendor.ErrNotFound
	}
	m.store[name] = v
	return nil
}
func (m *mockVendorStore) Delete(_ context.Context, name string) error {
	if _, ok := m.store[name]; !ok {
		return vendor.ErrNotFound
	}
	delete(m.store, name)
	return nil
}

func newTestRouter(vs *mockVendorStore) *gin.Engine {
	r := gin.New()
	New(&mockQueue{}, vs).Register(r)
	return r
}

// --- renderBody tests ---

func TestRenderBody_NoTemplate(t *testing.T) {
	raw := json.RawMessage(`{"a":1}`)
	out, err := renderBody("", raw)
	require.NoError(t, err)
	assert.Equal(t, []byte(raw), out)
}

func TestRenderBody_WithTemplate(t *testing.T) {
	raw := json.RawMessage(`{"user_id":"u123","amount":49.99}`)
	tpl := `{"crm_id":"{{.user_id}}","total":{{.amount}}}`
	out, err := renderBody(tpl, raw)
	require.NoError(t, err)
	assert.Equal(t, `{"crm_id":"u123","total":49.99}`, string(out))
}

func TestRenderBody_NonObjectBody_ReturnsError(t *testing.T) {
	_, err := renderBody("{{.field}}", json.RawMessage(`"not an object"`))
	assert.Error(t, err)
}

func TestRenderBody_EmptyBody_WithTemplate(t *testing.T) {
	out, err := renderBody("hello", nil)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(out))
}

// --- validateTemplate tests ---

func TestValidateTemplate_Empty(t *testing.T) {
	assert.NoError(t, validateTemplate(""))
}

func TestValidateTemplate_Valid(t *testing.T) {
	assert.NoError(t, validateTemplate(`{"id":"{{.id}}"}`))
}

func TestValidateTemplate_Invalid(t *testing.T) {
	assert.Error(t, validateTemplate(`{{.unclosed`))
}

// --- HTTP handler tests ---

func TestEnqueue_VendorNotFound(t *testing.T) {
	r := newTestRouter(newMockVendorStore())
	body := `{"body":{"event":"test"}}`
	req := httptest.NewRequest(http.MethodPost, "/notifications/unknown", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestEnqueue_Success_Returns202(t *testing.T) {
	vs := newMockVendorStore(&model.VendorConfig{
		Name:      "crm",
		TargetURL: "https://example.com",
		Method:    "POST",
		Headers:   model.HeadersMap{"Content-Type": "application/json"},
	})
	r := newTestRouter(vs)
	body := `{"body":{"event":"paid"}}`
	req := httptest.NewRequest(http.MethodPost, "/notifications/crm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["id"])
	assert.NotEmpty(t, resp["idempotency_key"])
}

func TestEnqueue_IdempotencyKey_FromHeader(t *testing.T) {
	vs := newMockVendorStore(&model.VendorConfig{
		Name: "crm", TargetURL: "https://example.com", Method: "POST", Headers: model.HeadersMap{},
	})
	r := newTestRouter(vs)
	req := httptest.NewRequest(http.MethodPost, "/notifications/crm", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "my-key-123")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "my-key-123", resp["idempotency_key"])
}

func TestCreateVendor_InvalidTemplate_Returns400(t *testing.T) {
	r := newTestRouter(newMockVendorStore())
	body := `{"name":"bad","target_url":"https://x.com","body_tpl":"{{unclosed"}`
	req := httptest.NewRequest(http.MethodPost, "/vendors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateVendor_Success(t *testing.T) {
	r := newTestRouter(newMockVendorStore())
	body := `{"name":"crm","target_url":"https://crm.example.com","method":"POST","headers":{"Authorization":"Bearer x"}}`
	req := httptest.NewRequest(http.MethodPost, "/vendors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestDeleteVendor_NotFound(t *testing.T) {
	r := newTestRouter(newMockVendorStore())
	req := httptest.NewRequest(http.MethodDelete, "/vendors/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

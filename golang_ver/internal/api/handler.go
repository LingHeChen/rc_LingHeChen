package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
	"github.com/LingHeChen/rc_LingHeChen/internal/queue"
	"github.com/LingHeChen/rc_LingHeChen/internal/vendor"
)

type Handler struct {
	queue       queue.Queue
	vendors     vendor.Storer
	maxAttempts int
}

func New(q queue.Queue, vs vendor.Storer) *Handler {
	return &Handler{queue: q, vendors: vs, maxAttempts: 10}
}

func (h *Handler) Register(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.POST("/vendors", h.createVendor)
	r.GET("/vendors", h.listVendors)
	r.GET("/vendors/:name", h.getVendor)
	r.PUT("/vendors/:name", h.updateVendor)
	r.DELETE("/vendors/:name", h.deleteVendor)

	r.POST("/notifications/:vendor_name", h.enqueue)
}

// --- Vendor config management ---

type vendorBody struct {
	TargetURL    string            `json:"target_url" binding:"required"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	BodyTemplate string            `json:"body_tpl"`
}

func (h *Handler) createVendor(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
		vendorBody
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTemplate(req.BodyTemplate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid body_tpl: %v", err)})
		return
	}
	v := &model.VendorConfig{
		Name:         req.Name,
		TargetURL:    req.TargetURL,
		Method:       methodOrDefault(req.Method),
		Headers:      headersOrEmpty(req.Headers),
		BodyTemplate: req.BodyTemplate,
	}
	if err := h.vendors.Create(c.Request.Context(), v); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, v)
}

func (h *Handler) listVendors(c *gin.Context) {
	vs, err := h.vendors.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if vs == nil {
		vs = []*model.VendorConfig{}
	}
	c.JSON(http.StatusOK, vs)
}

func (h *Handler) getVendor(c *gin.Context) {
	v, err := h.vendors.Get(c.Request.Context(), c.Param("name"))
	if errors.Is(err, vendor.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *Handler) updateVendor(c *gin.Context) {
	var req vendorBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTemplate(req.BodyTemplate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid body_tpl: %v", err)})
		return
	}
	err := h.vendors.Update(c.Request.Context(), c.Param("name"), &model.VendorConfig{
		TargetURL:    req.TargetURL,
		Method:       methodOrDefault(req.Method),
		Headers:      headersOrEmpty(req.Headers),
		BodyTemplate: req.BodyTemplate,
	})
	if errors.Is(err, vendor.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) deleteVendor(c *gin.Context) {
	err := h.vendors.Delete(c.Request.Context(), c.Param("name"))
	if errors.Is(err, vendor.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Notification delivery ---

type enqueueRequest struct {
	Body         json.RawMessage   `json:"body"`
	ExtraHeaders map[string]string `json:"extra_headers"`
}

func (h *Handler) enqueue(c *gin.Context) {
	vendorName := c.Param("vendor_name")

	v, err := h.vendors.Get(c.Request.Context(), vendorName)
	if errors.Is(err, vendor.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req enqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Merge headers: extra_headers as base, vendor headers take precedence (cannot be overridden).
	merged := make(map[string]string, len(v.Headers)+len(req.ExtraHeaders))
	maps.Copy(merged, req.ExtraHeaders)
	maps.Copy(merged, v.Headers)

	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}

	body, err := renderBody(v.BodyTemplate, req.Body)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("body_tpl render failed: %v", err)})
		return
	}

	job := &model.Job{
		ID:             uuid.NewString(),
		VendorName:     vendorName,
		IdempotencyKey: idempotencyKey,
		TargetURL:      v.TargetURL,
		Method:         v.Method,
		Headers:        merged,
		Body:           body,
		MaxAttempts:    h.maxAttempts,
		NextRetryAt:    time.Now(),
	}

	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"id":              job.ID,
		"idempotency_key": idempotencyKey,
	})
}

func methodOrDefault(m string) string {
	if m == "" {
		return http.MethodPost
	}
	return m
}

func headersOrEmpty(h map[string]string) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	return h
}

func validateTemplate(tpl string) error {
	if tpl == "" {
		return nil
	}
	_, err := template.New("").Parse(tpl)
	return err
}

// renderBody renders tpl with rawBody as the data context.
// If tpl is empty the raw body is returned unchanged.
// rawBody must be a JSON object when tpl is set.
func renderBody(tpl string, rawBody json.RawMessage) ([]byte, error) {
	if tpl == "" {
		return rawBody, nil
	}
	var data map[string]any
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &data); err != nil {
			return nil, fmt.Errorf("body must be a JSON object when body_tpl is set: %w", err)
		}
	}
	t, err := template.New("").Parse(tpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

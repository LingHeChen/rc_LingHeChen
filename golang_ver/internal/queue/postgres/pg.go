package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
	"github.com/LingHeChen/rc_LingHeChen/internal/queue"
)

type Queue struct {
	db         *gorm.DB
	skipLocked bool
}

// New creates a Queue. It auto-detects whether the backend supports
// FOR UPDATE SKIP LOCKED (PostgreSQL) or must fall back to plain
// transaction serialisation (SQLite-backed systems like Combee).
func New(db *gorm.DB) *Queue {
	var ver string
	db.Raw("SELECT version()").Scan(&ver)
	return &Queue{
		db:         db,
		skipLocked: !strings.Contains(ver, "SQLite"),
	}
}

func (q *Queue) Enqueue(ctx context.Context, job *model.Job) error {
	headers, err := json.Marshal(job.Headers)
	if err != nil {
		return err
	}
	result := q.db.WithContext(ctx).Exec(`
		INSERT INTO notification_jobs
			(id, vendor_name, idempotency_key, target_url, method, headers, body, max_attempts, next_retry_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (vendor_name, idempotency_key) DO NOTHING
	`, job.ID, job.VendorName, job.IdempotencyKey, job.TargetURL, job.Method, string(headers), job.Body, job.MaxAttempts)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Duplicate key: fetch the existing job's ID so the caller returns a real ID.
		return q.db.WithContext(ctx).
			Model(&model.Job{}).
			Where("vendor_name = ? AND idempotency_key = ?", job.VendorName, job.IdempotencyKey).
			Select("id").Scan(&job.ID).Error
	}
	return nil
}

func (q *Queue) Dequeue(ctx context.Context) (*model.Job, error) {
	var job model.Job
	err := q.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		query := tx.
			Where("status = ? AND next_retry_at <= ?", model.StatusPending, now).
			Order("next_retry_at ASC")
		if q.skipLocked {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if res := query.First(&job); res.Error != nil {
			return res.Error
		}
		return tx.Model(&job).Updates(map[string]any{
			"status":   "processing",
			"attempts": gorm.Expr("attempts + 1"),
		}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, queue.ErrNoJob
	}
	if err != nil {
		return nil, err
	}
	job.Attempts++
	return &job, nil
}

func (q *Queue) Ack(ctx context.Context, id string) error {
	return q.db.WithContext(ctx).Model(&model.Job{}).
		Where("id = ?", id).
		Update("status", model.StatusDone).Error
}

func (q *Queue) Nack(ctx context.Context, id string, errMsg string, nextRetry time.Time) error {
	return q.db.WithContext(ctx).Model(&model.Job{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        model.StatusPending,
			"last_error":    errMsg,
			"next_retry_at": nextRetry,
		}).Error
}

func (q *Queue) Dead(ctx context.Context, id string, reason string) error {
	return q.db.WithContext(ctx).Model(&model.Job{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     model.StatusDead,
			"last_error": reason,
		}).Error
}

func (q *Queue) RecoverStuck(ctx context.Context, timeout time.Duration) error {
	cutoff := time.Now().Add(-timeout)
	now := time.Now()
	return q.db.WithContext(ctx).Exec(`
		UPDATE notification_jobs
		SET status = 'pending', next_retry_at = ?, updated_at = ?
		WHERE status = 'processing' AND updated_at < ?
	`, now, now, cutoff).Error
}

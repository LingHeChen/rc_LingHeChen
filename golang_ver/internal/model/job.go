package model

import "time"

type JobStatus string

const (
	StatusPending JobStatus = "pending"
	StatusDone    JobStatus = "done"
	StatusDead    JobStatus = "dead"
)

type Job struct {
	ID             string     `gorm:"type:uuid;primaryKey"`
	VendorName     string     `gorm:"uniqueIndex:idx_jobs_vendor_idempotency;size:100;not null"`
	IdempotencyKey string     `gorm:"uniqueIndex:idx_jobs_vendor_idempotency;size:255;not null"`
	TargetURL      string     `gorm:"not null"`
	Method         string     `gorm:"size:10;not null;default:POST"`
	Headers        HeadersMap `gorm:"type:jsonb;not null;default:'{}'"`
	Body           []byte     `gorm:"type:bytea"`
	Status         JobStatus  `gorm:"size:20;not null;default:pending"`
	Attempts       int        `gorm:"not null;default:0"`
	MaxAttempts    int        `gorm:"not null;default:10"`
	NextRetryAt    time.Time  `gorm:"not null"`
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (Job) TableName() string { return "notification_jobs" }

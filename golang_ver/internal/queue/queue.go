package queue

import (
	"context"
	"errors"
	"time"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
)

var ErrNoJob = errors.New("no job available")

type Queue interface {
	Enqueue(ctx context.Context, job *model.Job) error
	Dequeue(ctx context.Context) (*model.Job, error)
	Ack(ctx context.Context, id string) error
	Nack(ctx context.Context, id string, errMsg string, nextRetry time.Time) error
	Dead(ctx context.Context, id string, reason string) error
	RecoverStuck(ctx context.Context, timeout time.Duration) error
}

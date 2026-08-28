package vendor

import (
	"context"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
)

// Storer is the interface handler depends on, allowing mock injection in tests.
type Storer interface {
	Create(ctx context.Context, v *model.VendorConfig) error
	Get(ctx context.Context, name string) (*model.VendorConfig, error)
	List(ctx context.Context) ([]*model.VendorConfig, error)
	Update(ctx context.Context, name string, v *model.VendorConfig) error
	Delete(ctx context.Context, name string) error
}

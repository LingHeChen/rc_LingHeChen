package vendor

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/LingHeChen/rc_LingHeChen/internal/model"
)

var ErrNotFound = errors.New("vendor not found")

type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, v *model.VendorConfig) error {
	v.ID = uuid.NewString()
	return s.db.WithContext(ctx).Create(v).Error
}

func (s *Store) Get(ctx context.Context, name string) (*model.VendorConfig, error) {
	var v model.VendorConfig
	err := s.db.WithContext(ctx).Where("name = ?", name).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &v, err
}

func (s *Store) List(ctx context.Context) ([]*model.VendorConfig, error) {
	var result []*model.VendorConfig
	return result, s.db.WithContext(ctx).Order("name").Find(&result).Error
}

func (s *Store) Update(ctx context.Context, name string, v *model.VendorConfig) error {
	headers, err := json.Marshal(v.Headers)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&model.VendorConfig{}).
		Where("name = ?", name).
		Updates(map[string]any{
			"target_url": v.TargetURL,
			"method":     v.Method,
			"headers":    string(headers),
			"body_tpl":   v.BodyTemplate,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, name string) error {
	result := s.db.WithContext(ctx).Where("name = ?", name).Delete(&model.VendorConfig{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

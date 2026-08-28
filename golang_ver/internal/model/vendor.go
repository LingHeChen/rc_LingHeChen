package model

import "time"

type VendorConfig struct {
	ID           string     `json:"id"         gorm:"type:uuid;primaryKey"`
	Name         string     `json:"name"        gorm:"uniqueIndex;size:100;not null"`
	TargetURL    string     `json:"target_url"  gorm:"not null"`
	Method       string     `json:"method"      gorm:"size:10;not null;default:POST"`
	Headers      HeadersMap `json:"headers"     gorm:"type:jsonb;not null;default:'{}'"`
	BodyTemplate string     `json:"body_tpl"    gorm:"column:body_tpl"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (VendorConfig) TableName() string { return "vendor_configs" }

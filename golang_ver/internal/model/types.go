package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// HeadersMap is a map[string]string that persists as JSONB.
type HeadersMap map[string]string

func (h HeadersMap) Value() (driver.Value, error) {
	b, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (h *HeadersMap) Scan(value any) error {
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("HeadersMap: unsupported scan type")
	}
	return json.Unmarshal(b, h)
}

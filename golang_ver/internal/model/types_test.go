package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeadersMap_Value(t *testing.T) {
	h := HeadersMap{"Authorization": "Bearer token", "Content-Type": "application/json"}
	v, err := h.Value()
	require.NoError(t, err)
	s, ok := v.(string)
	require.True(t, ok)
	assert.Contains(t, s, "Authorization")
	assert.Contains(t, s, "Bearer token")
}

func TestHeadersMap_Scan_bytes(t *testing.T) {
	var h HeadersMap
	err := h.Scan([]byte(`{"X-Key":"val"}`))
	require.NoError(t, err)
	assert.Equal(t, "val", h["X-Key"])
}

func TestHeadersMap_Scan_string(t *testing.T) {
	var h HeadersMap
	err := h.Scan(`{"X-Key":"val"}`)
	require.NoError(t, err)
	assert.Equal(t, "val", h["X-Key"])
}

func TestHeadersMap_Scan_unsupportedType(t *testing.T) {
	var h HeadersMap
	err := h.Scan(42)
	assert.Error(t, err)
}

func TestHeadersMap_RoundTrip(t *testing.T) {
	original := HeadersMap{"A": "1", "B": "2"}
	v, err := original.Value()
	require.NoError(t, err)

	var restored HeadersMap
	require.NoError(t, restored.Scan(v))
	assert.Equal(t, original, restored)
}

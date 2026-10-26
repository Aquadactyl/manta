package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestJsonNullString(t *testing.T) {
	// Valid string
	ns := JsonNullString{}
	ns.Valid = true
	ns.String = "test-string"

	b, err := json.Marshal(ns)
	require.NoError(t, err)
	assert.Equal(t, `"test-string"`, string(b))

	// Invalid / null string
	nsInvalid := JsonNullString{}
	b, err = json.Marshal(nsInvalid)
	require.NoError(t, err)
	assert.Equal(t, "null", string(b))

	// Unmarshal null
	var ns2 JsonNullString
	err = json.Unmarshal([]byte("null"), &ns2)
	require.NoError(t, err)
	assert.False(t, ns2.Valid)

	// Unmarshal string
	var ns3 JsonNullString
	err = json.Unmarshal([]byte(`"hello"`), &ns3)
	require.NoError(t, err)
	assert.True(t, ns3.Valid)
	assert.Equal(t, "hello", ns3.String)

	// Unmarshal invalid json
	var ns4 JsonNullString
	err = json.Unmarshal([]byte(`1234`), &ns4)
	assert.Error(t, err)
}

func TestActivityModel(t *testing.T) {
	act := Activity{
		Server: "server-uuid-1",
		Event:  "server:power.start",
		IP:     "192.168.1.1:12345",
	}

	// SetUser with string
	actPtr := act.SetUser("user-uuid-1")
	assert.True(t, actPtr.User.Valid)
	assert.Equal(t, "user-uuid-1", actPtr.User.String)

	// SetUser with empty string
	actEmpty := act.SetUser("")
	assert.False(t, actEmpty.User.Valid)

	// BeforeCreate hook
	require.NoError(t, act.BeforeCreate(&gorm.DB{}))
	assert.Equal(t, "192.168.1.1", act.IP) // port stripped
	assert.False(t, act.Timestamp.IsZero())
	assert.Equal(t, time.UTC, act.Timestamp.Location())
	assert.NotNil(t, act.Metadata)
}

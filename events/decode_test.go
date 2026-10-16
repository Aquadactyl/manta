package events

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDecodeToTypedDestinations(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		var out struct {
			Count uint64
			Data  json.RawMessage
		}
		if err := DecodeTo([]byte(`{"Count":18446744073709551615,"Data":{"a":1}}`), &out); err != nil {
			t.Fatal(err)
		}
		if out.Count != ^uint64(0) || string(out.Data) != `{"a":1}` {
			t.Fatalf("typed values lost: %#v", out)
		}
	})
	t.Run("pointer_to_pointer", func(t *testing.T) {
		var out *Event
		if err := DecodeTo([]byte(`{"Topic":"test","Data":"value"}`), &out); err != nil {
			t.Fatal(err)
		}
		if out == nil || out.Topic != "test" || out.Data != "value" {
			t.Fatalf("destination not populated: %#v", out)
		}
	})
	t.Run("interface", func(t *testing.T) {
		var out any
		if err := DecodeTo([]byte(`"value"`), &out); err != nil || out != "value" {
			t.Fatalf("interface: %v, %v", out, err)
		}
	})
	t.Run("slice", func(t *testing.T) {
		var out []int
		if err := DecodeTo([]byte(`[1,2]`), &out); err != nil || len(out) != 2 || out[1] != 2 {
			t.Fatalf("slice: %v, %v", out, err)
		}
	})
}

func TestDecodeToRetainsWrappedErrors(t *testing.T) {
	var out Event
	err := DecodeTo([]byte(`{"Topic":`), &out)
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) || !strings.Contains(err.Error(), "events: failed to decode byte slice") {
		t.Fatalf("syntax error wrapping lost: %v", err)
	}
	for _, target := range []any{nil, (*Event)(nil), Event{}} {
		var invalid *json.InvalidUnmarshalError
		if err := DecodeTo([]byte(`{}`), target); !errors.As(err, &invalid) {
			t.Fatalf("invalid destination accepted: %T, %v", target, err)
		}
	}
}

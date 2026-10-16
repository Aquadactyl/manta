package websocket

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pterodactyl/wings/events"
)

func TestDecodeEventMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		arg  string
	}{
		{"status", `{"Topic":"status","Data":"running"}`, "running"},
		{"escaped_string", `{"Topic":"console output","Data":"quote: \" \\ \n \t \u001b \uD83D\uDC1F"}`, "quote: \" \\ \n \t \x1b 🐟"},
		{"empty_string", `{"Topic":"console output","Data":""}`, ""},
		{"utf8_string", `{"Topic":"console output","Data":"雪 🐟"}`, "雪 🐟"},
		{"invalid_utf8", "{\"Topic\":\"console output\",\"Data\":\"\xff\"}", "\ufffd"},
		{"unpaired_surrogate", `{"Topic":"console output","Data":"\uD83D"}`, "\ufffd"},
		{"object", `{"Topic":"stats","Data":{"state":"running","network":{"rx_bytes":1,"tx_bytes":2},"cpu_absolute":1.25}}`, `{"state":"running","network":{"rx_bytes":1,"tx_bytes":2},"cpu_absolute":1.25}`},
		{"array", `{"Topic":"custom","Data":[1,"two",true,null,{"a":3}]}`, `[1,"two",true,null,{"a":3}]`},
		{"bool", `{"Topic":"custom","Data":false}`, "false"},
		{"number", `{"Topic":"custom","Data":1.25}`, "1.25"},
		{"null", `{"Topic":"custom","Data":null}`, "null"},
		{"missing_data", `{"Topic":"custom"}`, "null"},
		{"null_envelope", `null`, "null"},
		{"whitespace", "{\"Topic\":\"custom\",\"Data\": \n \"space\" }", "space"},
		{"byte_slice_base64", `{"Topic":"custom","Data":"aGVsbG8="}`, "aGVsbG8="},
		{"large_counters", `{"Topic":"stats","Data":{"memory_bytes":9007199254740993,"network":{"rx_bytes":18446744073709551615,"tx_bytes":9007199254740995}}}`, `{"memory_bytes":9007199254740993,"network":{"rx_bytes":18446744073709551615,"tx_bytes":9007199254740995}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message, err := decodeEventMessage([]byte(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct{ Topic string }
			if err := json.Unmarshal([]byte(tc.data), &envelope); err != nil {
				t.Fatal(err)
			}
			want := Message{Event: Event(envelope.Topic), Args: []string{tc.arg}}
			// Check the actual outer wire encoding still carries one string arg.
			wire, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			var got Message
			if err := json.Unmarshal(wire, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
		})
	}
}

func TestDecodeEventMessageRejectsInvalidEnvelope(t *testing.T) {
	for _, data := range []string{"", "{", `{"Topic":42}`, `{"Topic":"stats","Data":{]}`, `{"Data":"unterminated}`, `[]`, `{"Data":true} {}`} {
		if _, err := decodeEventMessage([]byte(data)); err == nil {
			t.Errorf("accepted invalid event %q", data)
		}
	}
}

func TestDecodeEventMessagePreservesBusPayloads(t *testing.T) {
	for _, payload := range []any{"running", "\x1b[32mquotes: \"\\\n雪", []byte("hello"), nil, true, []any{1, "two", false}, map[string]any{"nested": map[string]any{"a": 3}, "z": 2.5}} {
		data, err := json.Marshal(events.Event{Topic: "custom", Data: payload})
		if err != nil {
			t.Fatal(err)
		}
		message, err := decodeEventMessage(data)
		if err != nil {
			t.Fatal(err)
		}
		// Compare the former decode/map/marshal path semantically: JSON object
		// key ordering is not part of the Panel protocol.
		var old events.Event
		if err := events.DecodeTo(data, &old); err != nil {
			t.Fatal(err)
		}
		if str, ok := old.Data.(string); ok {
			if message.Args[0] != str {
				t.Fatalf("string payload changed: %q", message.Args[0])
			}
			continue
		}
		var got any
		if err := json.Unmarshal([]byte(message.Args[0]), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, old.Data) {
			t.Fatalf("payload changed: got %#v; want %#v", got, old.Data)
		}
	}
}

func TestDecodeEventMessageOwnsPayload(t *testing.T) {
	data := append([]byte(nil), benchmarkStatsEvent...)
	message, err := decodeEventMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	want := message.Args[0]
	for i := range data {
		data[i] = 'x'
	}
	if message.Args[0] != want || !json.Valid([]byte(message.Args[0])) {
		t.Fatal("message retained mutable input storage")
	}
}

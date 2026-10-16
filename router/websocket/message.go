package websocket

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/pterodactyl/wings/events"
)

type Event string

const (
	AuthenticationSuccessEvent = Event("auth success")
	TokenExpiringEvent         = Event("token expiring")
	TokenExpiredEvent          = Event("token expired")
	AuthenticationEvent        = Event("auth")
	SetStateEvent              = Event("set state")
	SendServerLogsEvent        = Event("send logs")
	SendCommandEvent           = Event("send command")
	SendStatsEvent             = Event("send stats")
	ErrorEvent                 = Event("daemon error")
	JwtErrorEvent              = Event("jwt error")
	ThrottledEvent             = Event("throttled")
)

type Message struct {
	// The event to perform.
	Event Event `json:"event"`

	// The data to pass along, only used by power/command currently. Other requests
	// should either omit the field or pass an empty value as it is ignored.
	Args []string `json:"args,omitempty"`
}

// decodeEventMessage converts a bus event to the Panel's string-argument format.
func decodeEventMessage(data []byte) (Message, error) {
	e := struct {
		Topic string
		Data  eventArgument
	}{Data: "null"}
	if err := events.DecodeTo(data, &e); err != nil {
		return Message{}, err
	}
	return Message{Event: Event(e.Topic), Args: []string{string(e.Data)}}, nil
}

// eventArgument owns the string argument the Panel expects. The envelope's JSON
// decoder validates the input before calling UnmarshalJSON. Non-string payloads
// are already JSON: retain those bytes without constructing a generic object
// tree or rounding integer counters through float64 for every viewer.
type eventArgument string

func (a *eventArgument) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		// Ordinary state strings can be unquoted directly. Escaped or invalid
		// UTF-8 strings still need JSON's escape/replacement handling.
		if bytes.IndexByte(data, '\\') < 0 && utf8.Valid(data) {
			*a = eventArgument(data[1 : len(data)-1])
		} else {
			return json.Unmarshal(data, (*string)(a))
		}
	} else {
		*a = eventArgument(data)
	}
	return nil
}

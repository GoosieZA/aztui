package servicebus

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// sendSpec is the JSON document the user edits in $EDITOR to compose or
// clone a message. body may be any JSON value: a string is sent verbatim as
// the message body, anything else is sent as its JSON encoding.
type sendSpec struct {
	MessageID             string          `json:"message_id,omitempty"`
	Subject               string          `json:"subject"`
	ContentType           string          `json:"content_type"`
	CorrelationID         string          `json:"correlation_id"`
	SessionID             string          `json:"session_id,omitempty"`
	To                    string          `json:"to,omitempty"`
	ApplicationProperties map[string]any  `json:"application_properties"`
	Body                  json.RawMessage `json:"body"`
}

func sendTemplate() []byte {
	spec := sendSpec{
		ContentType:           "application/json",
		ApplicationProperties: map[string]any{},
		Body:                  json.RawMessage(`{}`),
	}
	out, _ := json.MarshalIndent(spec, "", "  ")
	return append(out, '\n')
}

// specFromMessage builds an editable spec from a received message, for the
// clone-and-resend flow.
func specFromMessage(m *azservicebus.ReceivedMessage) (sendSpec, error) {
	spec := sendSpec{
		MessageID:             m.MessageID,
		Subject:               strOf(m.Subject),
		ContentType:           strOf(m.ContentType),
		CorrelationID:         strOf(m.CorrelationID),
		SessionID:             strOf(m.SessionID),
		To:                    strOf(m.To),
		ApplicationProperties: m.ApplicationProperties,
	}
	if spec.ApplicationProperties == nil {
		spec.ApplicationProperties = map[string]any{}
	}
	switch {
	case json.Valid(m.Body):
		spec.Body = json.RawMessage(m.Body)
	case utf8.Valid(m.Body):
		quoted, _ := json.Marshal(string(m.Body))
		spec.Body = json.RawMessage(quoted)
	default:
		return spec, fmt.Errorf("message body is binary and cannot be cloned in the editor")
	}
	return spec, nil
}

func (s sendSpec) toMessage() (*azservicebus.Message, error) {
	msg := &azservicebus.Message{
		ApplicationProperties: s.ApplicationProperties,
	}
	if s.MessageID != "" {
		msg.MessageID = to.Ptr(s.MessageID)
	}
	if s.Subject != "" {
		msg.Subject = to.Ptr(s.Subject)
	}
	if s.ContentType != "" {
		msg.ContentType = to.Ptr(s.ContentType)
	}
	if s.CorrelationID != "" {
		msg.CorrelationID = to.Ptr(s.CorrelationID)
	}
	if s.SessionID != "" {
		msg.SessionID = to.Ptr(s.SessionID)
	}
	if s.To != "" {
		msg.To = to.Ptr(s.To)
	}

	// A JSON string body is sent verbatim; any other JSON value is sent as-is.
	var asString string
	if err := json.Unmarshal(s.Body, &asString); err == nil {
		msg.Body = []byte(asString)
	} else {
		msg.Body = []byte(s.Body)
	}
	return msg, nil
}

func specJSON(spec sendSpec) ([]byte, error) {
	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func parseSpec(raw []byte) (sendSpec, error) {
	var spec sendSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, fmt.Errorf("invalid message json: %w", err)
	}
	if len(spec.Body) == 0 {
		spec.Body = json.RawMessage(`""`)
	}
	return spec, nil
}

func strOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

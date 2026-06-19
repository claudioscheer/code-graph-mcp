package events

import (
	"encoding/json"
	"errors"
	"fmt"
)

const Protocol = "codegraph.v1"

type EventType string

const (
	EventNode    EventType = "node"
	EventEdge    EventType = "edge"
	EventWarning EventType = "warning"
	EventSummary EventType = "summary"
)

type GraphEvent struct {
	Protocol string         `json:"protocol"`
	Type     EventType      `json:"type"`
	Label    string         `json:"label,omitempty"`
	ID       string         `json:"id,omitempty"`
	Rel      string         `json:"rel,omitempty"`
	From     string         `json:"from,omitempty"`
	To       string         `json:"to,omitempty"`
	Source   string         `json:"source,omitempty"`
	Message  string         `json:"message,omitempty"`
	Props    map[string]any `json:"props,omitempty"`
}

func DecodeLine(line []byte) (GraphEvent, error) {
	var event GraphEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return GraphEvent{}, err
	}
	return event, Validate(event)
}

func Validate(event GraphEvent) error {
	if event.Protocol != Protocol {
		return fmt.Errorf("unsupported protocol %q", event.Protocol)
	}
	switch event.Type {
	case EventNode:
		if event.Label == "" || event.ID == "" {
			return errors.New("node event requires label and id")
		}
	case EventEdge:
		if event.Rel == "" || event.From == "" || event.To == "" {
			return errors.New("edge event requires rel, from, and to")
		}
	case EventWarning:
		if event.Message == "" {
			return errors.New("warning event requires message")
		}
	case EventSummary:
		return nil
	default:
		return fmt.Errorf("unsupported event type %q", event.Type)
	}
	return nil
}

func EventNodeID(kind string, id string) string {
	if len(id) > len(kind)+1 && id[:len(kind)+1] == kind+":" {
		return id
	}
	return kind + ":" + id
}

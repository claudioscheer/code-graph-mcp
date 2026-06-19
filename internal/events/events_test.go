package events

import "testing"

func TestValidateRequiresProtocolAndShape(t *testing.T) {
	valid := GraphEvent{Protocol: Protocol, Type: EventNode, Label: "File", ID: "file:a.ts"}
	if err := Validate(valid); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}

	invalid := GraphEvent{Protocol: Protocol, Type: EventEdge, Rel: "CALLS", From: "symbol:a#x"}
	if err := Validate(invalid); err == nil {
		t.Fatal("Validate(invalid) expected error")
	}
}

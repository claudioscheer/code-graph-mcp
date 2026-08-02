package neo4jstore

import (
	"strings"
	"testing"

	"github.com/claudioscheer/code-graph-mcp/internal/events"
)

func TestNormalizeAnalysisMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults to full", in: "", want: "full"},
		{name: "full stays full", in: "full", want: "full"},
		{name: "fast stays fast", in: "fast", want: "fast"},
		{name: "unknown defaults to full", in: "partial", want: "full"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAnalysisMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeAnalysisMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRelationshipIdentityIncludesSourceLocation(t *testing.T) {
	first := events.GraphEvent{
		Rel:  "REFERENCES",
		From: "symbol:src/page.tsx#Page",
		To:   "symbol:src/session.ts#SESSION_TTL_MS",
		Props: map[string]any{
			"sourceFile": "src/page.tsx",
			"startLine":  6,
			"endLine":    6,
			"reason":     "typescript_reference_resolved",
		},
	}
	second := first
	second.Props = map[string]any{
		"sourceFile": "src/page.tsx",
		"startLine":  7,
		"endLine":    7,
		"reason":     "typescript_reference_resolved",
	}

	firstIdentity := relationshipIdentity(first)
	secondIdentity := relationshipIdentity(second)
	if firstIdentity == secondIdentity {
		t.Fatalf("relationship identities should differ for separate source locations")
	}
	if !strings.Contains(firstIdentity, "src/page.tsx") || !strings.Contains(firstIdentity, "6") {
		t.Fatalf("relationship identity %q does not include source file and line", firstIdentity)
	}
}

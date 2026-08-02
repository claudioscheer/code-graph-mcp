package mcp

import "testing"

func TestMergeHybridImpactUnionsGraphAndText(t *testing.T) {
	text := functionImpactResult{
		Symbol: "getSession",
		Definitions: []functionFileMatch{
			{Path: "packages/auth/src/session.ts", Category: "runtime", HitCount: 1, KindCounts: map[string]int{"definition": 1}},
		},
		CallSites: []functionFileMatch{
			{Path: "apps/web/src/page.tsx", Category: "runtime", HitCount: 1, KindCounts: map[string]int{"call": 1}, Owners: []string{"DashboardPage"}},
		},
		Imports: []functionFileMatch{},
		Files:   []string{"packages/auth/src/session.ts", "apps/web/src/page.tsx"},
		Counts:  map[string]int{"runtime": 2},
	}
	text.UniqueFiles = 2
	text.TotalHits = 2

	graphImpact := map[string]any{
		"hasCallGraph": true,
		"definitions": []map[string]any{
			{"path": "packages/auth/src/session.ts", "name": "getSession", "id": "symbol:packages/auth/src/session.ts#getSession"},
		},
		"callSites": []map[string]any{
			{"path": "apps/web/src/page.tsx", "owner": "DashboardPage", "kind": "CALLS", "line": 4},
			{"path": "apps/api/src/handler.ts", "owner": "handle", "kind": "CALLS", "line": 10},
		},
		"imports": []map[string]any{
			{"path": "apps/web/src/page.tsx", "kind": "import"},
		},
	}

	merged, meta := mergeHybridImpact(text, graphImpact)
	if method, _ := meta["resolutionMethod"].(string); method != "hybrid" {
		t.Fatalf("resolutionMethod=%v want hybrid; meta=%#v", meta["resolutionMethod"], meta)
	}
	if !meta["hasCallGraph"].(bool) {
		t.Fatalf("hasCallGraph=false")
	}
	if merged.UniqueFiles < 3 {
		t.Fatalf("uniqueFiles=%d want >=3 files=%v", merged.UniqueFiles, merged.Files)
	}
	foundGraphOnly := false
	for _, call := range merged.CallSites {
		if call.Path == "apps/api/src/handler.ts" {
			foundGraphOnly = true
			if len(call.Owners) == 0 || call.Owners[0] != "handle" {
				t.Fatalf("graph-only call owners=%v", call.Owners)
			}
		}
	}
	if !foundGraphOnly {
		t.Fatalf("missing graph-only call site: %#v", merged.CallSites)
	}
}

func TestMergeHybridImpactTextOnlyWhenNoGraph(t *testing.T) {
	text := functionImpactResult{
		Symbol:      "foo",
		UniqueFiles: 1,
		Files:       []string{"src/a.ts"},
		Definitions: []functionFileMatch{{Path: "src/a.ts", Category: "runtime", HitCount: 1}},
	}
	merged, meta := mergeHybridImpact(text, nil)
	if method, _ := meta["resolutionMethod"].(string); method != "text" {
		t.Fatalf("method=%v want text", meta["resolutionMethod"])
	}
	if merged.UniqueFiles != 1 {
		t.Fatalf("uniqueFiles=%d", merged.UniqueFiles)
	}
}

func TestImpactConfidence(t *testing.T) {
	if got := impactConfidence("hybrid", true, false, false, 3); got != "high" {
		t.Fatalf("hybrid call graph confidence=%s", got)
	}
	if got := impactConfidence("text", false, false, false, 2); got != "medium" {
		t.Fatalf("text confidence=%s", got)
	}
	if got := impactConfidence("hybrid", true, true, false, 3); got != "low" {
		t.Fatalf("ambiguous confidence=%s", got)
	}
	if got := impactConfidence("graph", true, false, true, 3); got != "low" {
		t.Fatalf("stale confidence=%s", got)
	}
}

func TestPathInScopeFiltersPackage(t *testing.T) {
	if !pathInScope("packages/auth/src/session.ts", "", "auth") {
		t.Fatal("expected auth package path in scope")
	}
	if pathInScope("packages/billing/src/x.ts", "", "auth") {
		t.Fatal("billing should be out of auth scope")
	}
	if !pathInScope("packages/auth/src/session.ts", "packages/auth", "") {
		t.Fatal("pathPrefix should match")
	}
	if pathInScope("packages/billing/src/x.ts", "packages/auth", "") {
		t.Fatal("pathPrefix should reject billing")
	}
}

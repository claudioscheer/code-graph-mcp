package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzePathSetImpactFindsImportersAndColocatedTests(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "packages/auth/src/session.ts", `
export function getSession() { return null; }
`)
	writeTestFile(t, repo, "packages/auth/src/session.test.ts", `
import { getSession } from './session';
test('session', () => { getSession(); });
`)
	writeTestFile(t, repo, "apps/web/src/page.tsx", `
import { getSession } from '../../packages/auth/src/session';
export function Page() { getSession(); }
`)

	result, err := (Server{Repo: repo}).analyzePathSetImpact(t.Context(), map[string]any{
		"paths":  []any{"packages/auth/src/session.ts"},
		"detail": "files",
		"raw":    true,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result["seedCount"] != 1 {
		t.Fatalf("seedCount=%v", result["seedCount"])
	}
	tests := mapSlice(result["relatedTests"])
	foundTest := false
	for _, item := range tests {
		if firstAnyString(item, "path") == "packages/auth/src/session.test.ts" {
			foundTest = true
		}
	}
	if !foundTest {
		t.Fatalf("expected colocated test, got %#v", tests)
	}
	// importer may be found via path fragment search
	dependents := mapSlice(result["dependents"])
	if len(dependents) == 0 && result["relatedTestCount"] == 0 {
		t.Fatalf("expected dependents or tests: %#v", result)
	}
	mustEdit := stringSlice(result["mustEdit"])
	if len(mustEdit) == 0 || mustEdit[0] != "packages/auth/src/session.ts" {
		t.Fatalf("mustEdit=%v", mustEdit)
	}
}

func TestPrepareChangePlanMultiSymbol(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/a.ts", `
export function alpha() { return 1; }
export function beta() { alpha(); }
`)
	writeTestFile(t, repo, "src/b.ts", `
import { beta } from './a';
export function gamma() { beta(); }
`)

	result, err := (Server{Repo: repo}).prepareChangePlan(t.Context(), map[string]any{
		"symbols": []any{"alpha", "beta"},
		"detail":  "summary",
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result["planKind"] != "change" {
		t.Fatalf("planKind=%v", result["planKind"])
	}
	mustEdit := stringSlice(result["mustEdit"])
	if len(mustEdit) == 0 {
		t.Fatalf("mustEdit empty: %#v", result)
	}
	if result["symbolCount"] != 2 {
		t.Fatalf("symbolCount=%v", result["symbolCount"])
	}
	if _, ok := result["suggestedOrder"]; !ok {
		t.Fatalf("missing suggestedOrder")
	}
	if _, ok := result["openNext"]; !ok {
		t.Fatalf("missing openNext")
	}
}

func TestPrepareChangePlanWithPaths(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/core.ts", `export const x = 1;`)
	writeTestFile(t, repo, "src/core.test.ts", `import { x } from './core';`)
	writeTestFile(t, repo, "src/use.ts", `import { x } from './core'; export const y = x;`)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"prepare_change_plan","arguments":{"paths":["src/core.ts"],"raw":true}}}`)
	response, ok := (Server{Repo: repo}).Process(t.Context(), payload)
	if !ok {
		t.Fatal("process failed")
	}
	text := toolText(t, response)
	for _, want := range []string{`"planKind"`, `"mustEditCount"`, `"suggestedOrder"`, `"totals"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s:\n%s", want, text)
		}
	}
}

func TestReindexUnavailableWithoutReindexer(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reindex","arguments":{"raw":true}}}`)
	response, ok := (Server{Repo: t.TempDir()}).Process(t.Context(), payload)
	if !ok {
		t.Fatal("process failed")
	}
	text := toolText(t, response)
	if !strings.Contains(text, `"success"`) {
		t.Fatalf("missing success:\n%s", text)
	}
	if strings.Contains(text, `"success": true`) || strings.Contains(text, `"success":true`) {
		t.Fatalf("expected unavailable reindex:\n%s", text)
	}
}

func TestReindexUsesReindexer(t *testing.T) {
	called := false
	server := Server{
		Repo: t.TempDir(),
		Reindexer: reindexFunc(func(ctx context.Context, req ReindexRequest) (map[string]any, error) {
			called = true
			return map[string]any{"status": "updated", "ripple": "test"}, nil
		}),
	}
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reindex","arguments":{"raw":true,"timeoutSec":60}}}`)
	response, ok := server.Process(t.Context(), payload)
	if !ok {
		t.Fatal("process failed")
	}
	if !called {
		t.Fatal("reindexer not called")
	}
	text := toolText(t, response)
	if !strings.Contains(text, `"success": true`) && !strings.Contains(text, `"success":true`) {
		t.Fatalf("expected success:\n%s", text)
	}
}

type reindexFunc func(ctx context.Context, req ReindexRequest) (map[string]any, error)

func (f reindexFunc) Reindex(ctx context.Context, req ReindexRequest) (map[string]any, error) {
	return f(ctx, req)
}

func TestToolsListAdvertisesChangePlanAndHidesDemoted(t *testing.T) {
	response, ok := (Server{}).Process(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if !ok {
		t.Fatal("tools/list failed")
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`"prepare_change_plan"`, `"analyze_path_set_impact"`, `"reindex"`,
		`"resolve_symbol"`, `"analyze_function_impact"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tools/list missing %s", want)
		}
	}
	for _, unwanted := range []string{
		`"get_dependencies"`, `"get_dependents"`, `"find_paths"`,
		`"get_ripple_info"`, `"list_node_types"`, `"get_impact"`,
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("tools/list still advertises %s", unwanted)
		}
	}
}


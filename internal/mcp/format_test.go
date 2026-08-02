package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatToolResultCompactsGraphNodesByDefault(t *testing.T) {
	result := map[string]any{
		"nodes": []map[string]any{
			{
				"sourceId":     "symbol:src/billing/service.ts#BillingService",
				"id":           "ripple:symbol:src/billing/service.ts#BillingService",
				"primaryLabel": "Symbol",
				"name":         "BillingService",
				"kind":         "class",
				"filePath":     "src/billing/service.ts",
				"startLine":    12,
				"labels":       []string{"GraphNode", "Symbol"},
				"language":     "typescript",
				"extractor":    "typescript",
				"confidence":   1,
			},
		},
		"returned":   1,
		"totalKnown": 1,
		"truncated":  false,
	}

	text, err := formatToolResult("search_code", result, map[string]any{})
	if err != nil {
		t.Fatalf("formatToolResult error = %v", err)
	}

	for _, want := range []string{
		"search_code returned=1 truncated=false",
		"Symbol BillingService class src/billing/service.ts:12 id=symbol:src/billing/service.ts#BillingService",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact output missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{`"nodes"`, `"labels"`, `"language"`, `"extractor"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("compact output contains raw JSON/noisy field %q:\n%s", unwanted, text)
		}
	}
}

func TestFormatToolResultKeepsRawJSONWhenRequested(t *testing.T) {
	result := map[string]any{
		"nodes": []map[string]any{
			{
				"sourceId":     "symbol:src/billing/service.ts#BillingService",
				"primaryLabel": "Symbol",
				"labels":       []string{"GraphNode", "Symbol"},
			},
		},
		"returned": 1,
	}

	text, err := formatToolResult("search_code", result, map[string]any{"format": "json"})
	if err != nil {
		t.Fatalf("formatToolResult error = %v", err)
	}

	for _, want := range []string{`"nodes"`, `"labels"`, `"sourceId"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("raw JSON output missing %q:\n%s", want, text)
		}
	}
}

func TestFormatToolResultCompactsHighLevelSections(t *testing.T) {
	result := map[string]any{
		"symbol":      "resolveTenantAccount",
		"uniqueFiles": 2,
		"totalHits":   3,
		"definitions": []map[string]any{
			{"path": "src/account.ts", "category": "runtime", "hitCount": 1},
		},
		"callSites": []map[string]any{
			{"path": "src/job.ts", "category": "runtime", "hitCount": 2, "owners": []string{"runJob", "handleX"}},
		},
		"truncated": false,
	}

	text, err := formatToolResult("analyze_function_impact", result, map[string]any{})
	if err != nil {
		t.Fatalf("formatToolResult error = %v", err)
	}

	for _, want := range []string{
		"analyze_function_impact resolveTenantAccount uniqueFiles=2 totalHits=3 truncated=false",
		"definitions (1)",
		"- src/account.ts category=runtime hitCount=1",
		"callSites (1)",
		"- src/job.ts category=runtime hitCount=2 owners=runJob,handleX",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact high-level output missing %q:\n%s", want, text)
		}
	}
	// Header scalars must not be repeated in the body.
	if strings.Count(text, "uniqueFiles=2") != 1 {
		t.Fatalf("uniqueFiles scalar printed more than once:\n%s", text)
	}
}

func TestFormatToolResultTruncatesLongLists(t *testing.T) {
	items := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		items = append(items, map[string]any{"path": "src/f.go", "hitCount": 1})
	}
	result := map[string]any{
		"symbol":    "x",
		"callSites": items,
	}
	text, err := formatToolResult("analyze_function_impact", result, map[string]any{"maxItems": 5})
	if err != nil {
		t.Fatalf("formatToolResult error = %v", err)
	}
	if !strings.Contains(text, "callSites (50)") {
		t.Fatalf("expected total count, got:\n%s", text)
	}
	if !strings.Contains(text, "... +45 more") {
		t.Fatalf("expected truncation marker, got:\n%s", text)
	}
	if strings.Count(text, "- src/f.go") != 5 {
		t.Fatalf("expected 5 items shown, got:\n%s", text)
	}
}

func TestFormatToolResultIncludesOpenedText(t *testing.T) {
	result := map[string]any{
		"path":      "src/service.ts",
		"startLine": 10,
		"endLine":   12,
		"text":      "export function run() {\n  return true;\n}",
	}

	text, err := formatToolResult("open_file_excerpt", result, map[string]any{})
	if err != nil {
		t.Fatalf("formatToolResult error = %v", err)
	}

	for _, want := range []string{
		"open_file_excerpt",
		"path=src/service.ts",
		"text:",
		"export function run() {",
		"  return true;",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact opened text missing %q:\n%s", want, text)
		}
	}
}

func TestFormatToolResultCompactsLabelCountItems(t *testing.T) {
	result := map[string]any{
		"nodeLabels": []map[string]any{
			{"label": "Symbol", "count": int64(12)},
		},
	}

	text, err := formatToolResult("list_node_types", result, map[string]any{})
	if err != nil {
		t.Fatalf("formatToolResult error = %v", err)
	}

	if !strings.Contains(text, "- Symbol count=12") {
		t.Fatalf("compact label/count output missing label and count:\n%s", text)
	}
	if strings.Contains(text, "- 1") {
		t.Fatalf("compact label/count output used index placeholder:\n%s", text)
	}
}

func TestProcessToolCallUsesCompactTextByDefault(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/client.ts", `process.env.SERVICE_LOGIN_EMAIL;`)
	writeTestFile(t, repo, "README.md", `SERVICE_LOGIN_EMAIL`)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"count_literal_files","arguments":{"query":"SERVICE_LOGIN_EMAIL"}}}`)
	response, ok := (Server{Repo: repo}).Process(t.Context(), payload)
	if !ok {
		t.Fatal("Process returned ok=false")
	}

	text := toolText(t, response)
	if !strings.Contains(text, "count_literal_files SERVICE_LOGIN_EMAIL uniqueFiles=2 totalMatches=2 truncated=false") {
		t.Fatalf("compact Process output missing summary:\n%s", text)
	}
	if strings.Contains(text, `"uniqueFiles"`) || strings.Contains(text, `"files"`) {
		t.Fatalf("Process output used raw JSON by default:\n%s", text)
	}
}

func TestProcessToolCallSupportsRawJSON(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/client.ts", `process.env.SERVICE_LOGIN_EMAIL;`)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"count_literal_files","arguments":{"query":"SERVICE_LOGIN_EMAIL","raw":true}}}`)
	response, ok := (Server{Repo: repo}).Process(t.Context(), payload)
	if !ok {
		t.Fatal("Process returned ok=false")
	}

	text := toolText(t, response)
	if !strings.Contains(text, `"uniqueFiles"`) || !strings.Contains(text, `"files"`) {
		t.Fatalf("Process raw output missing JSON fields:\n%s", text)
	}
}

func TestHelpIsShortRouter(t *testing.T) {
	response, ok := (Server{Repo: "/tmp/repo"}).Process(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"codegraph_help","arguments":{}}}`))
	if !ok {
		t.Fatal("help process failed")
	}
	text := toolText(t, response)
	if len(text) > 2500 {
		t.Fatalf("help too large: %d bytes\n%s", len(text), text)
	}
	for _, want := range []string{"router", "prepare_feature_context", "analyze_function_impact", "tokenRules"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"workflowRecipes", "recommendedWorkflow", "argumentAliases"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("help still contains bloated section %q:\n%s", unwanted, text)
		}
	}
}

func TestToolsListOmitsDeadAliases(t *testing.T) {
	response, ok := (Server{}).Process(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if !ok {
		t.Fatal("tools/list failed")
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, unwanted := range []string{`"get_impact"`, `"get_route_impact"`, `"get_related_tests"`, `"search_literal"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("tools/list still advertises %s", unwanted)
		}
	}
	for _, want := range []string{`"prepare_feature_context"`, `"analyze_function_impact"`, `"get_relations"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("tools/list missing %s", want)
		}
	}
	if len(raw) > 14000 {
		t.Fatalf("tools/list still too large: %d bytes", len(raw))
	}
}

func TestAnalyzeFunctionImpactSummaryOmitsTransitiveByDefault(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/a.ts", `
export function resolveTenantAccount() {}
export function recordMetric() { resolveTenantAccount(); }
`)
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"analyze_function_impact","arguments":{"symbol":"resolveTenantAccount"}}}`)
	response, ok := (Server{Repo: repo}).Process(t.Context(), payload)
	if !ok {
		t.Fatal("process failed")
	}
	text := toolText(t, response)
	if strings.Contains(text, "transitive") {
		t.Fatalf("expected no transitive section by default:\n%s", text)
	}
	if !strings.Contains(text, "callSites") {
		t.Fatalf("expected callSites:\n%s", text)
	}
}

func TestPrepareFeatureContextWorksWithoutNeo4j(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/account.ts", `
export function resolveTenantAccount() { return 1; }
`)
	writeTestFile(t, repo, "src/job.ts", `
import { resolveTenantAccount } from './account';
export function runJob() { resolveTenantAccount(); }
`)
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"prepare_feature_context","arguments":{"query":"resolveTenantAccount"}}}`)
	response, ok := (Server{Repo: repo}).Process(t.Context(), payload)
	if !ok {
		t.Fatal("process failed")
	}
	if errObj, has := response["error"]; has {
		t.Fatalf("unexpected error: %v", errObj)
	}
	text := toolText(t, response)
	if !strings.Contains(text, "prepare_feature_context") {
		t.Fatalf("missing tool name:\n%s", text)
	}
	if !strings.Contains(text, "planningReady=true") && !strings.Contains(text, "contextCompleteForPlanning") {
		// compact header uses planningReady
		if !strings.Contains(text, "entryPoints") && !strings.Contains(text, "directChangeFiles") {
			t.Fatalf("missing planning sections:\n%s", text)
		}
	}
	if strings.Contains(text, "sourceMatches") || strings.Contains(text, "graphMatches") {
		t.Fatalf("summary detail should omit full match dumps:\n%s", text)
	}
	if len(text) > 4000 {
		t.Fatalf("prepare_feature_context summary too large: %d bytes\n%s", len(text), text)
	}
}

func toolText(t *testing.T, response map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal response error = %v", err)
	}
	var decoded struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal response error = %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error: %v", decoded.Error)
	}
	if len(decoded.Result.Content) != 1 {
		t.Fatalf("content length = %d, want 1; raw=%s", len(decoded.Result.Content), string(encoded))
	}
	return decoded.Result.Content[0].Text
}

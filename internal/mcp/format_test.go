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
			{"path": "src/job.ts", "category": "runtime", "hitCount": 2, "owner": "runJob"},
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
		"- src/job.ts category=runtime hitCount=2 owner=runJob",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact high-level output missing %q:\n%s", want, text)
		}
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
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal response error = %v", err)
	}
	if len(decoded.Result.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(decoded.Result.Content))
	}
	return decoded.Result.Content[0].Text
}

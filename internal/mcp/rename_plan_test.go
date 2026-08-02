package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestAnalyzeRenameImpactDoesNotTruncatePathAt40(t *testing.T) {
	repo := t.TempDir()
	// More than 40 matching files would previously truncate with default limit=40.
	for i := 0; i < 55; i++ {
		writeTestFile(t, repo, "packages/app/src/f"+strconv.Itoa(i)+".ts", `import from "apps/workers/x";`)
	}
	writeTestFile(t, repo, "packages/core/utils/workers.ts", `export const worker = 1;`)

	result, err := (Server{Repo: repo}).analyzeRenameImpact(t.Context(), "apps/workers", "path", map[string]any{
		"detail": "summary",
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result["uniqueFiles"] != 55 {
		t.Fatalf("uniqueFiles=%v want 55 (default path scan must exceed 40)", result["uniqueFiles"])
	}
	if trunc, _ := result["scanTruncated"].(bool); trunc {
		t.Fatalf("scanTruncated=true with only 55 files")
	}
	if complete, _ := result["complete"].(bool); !complete {
		t.Fatalf("complete=false")
	}
}

func TestPrepareRenamePlanLayersAndMustNotTouch(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "apps/workers/package.json", `{"name":"@howdy/workers"}`)
	writeTestFile(t, repo, "apps/admin/src/q.ts", `import x from "../../workers/foo"; // apps/workers path
// apps/workers
`)
	writeTestFile(t, repo, "package.json", `"dev:workers": "turbo dev --filter=@howdy/workers"`)
	writeTestFile(t, repo, ".github/workflows/docker-deploy.yml", `
  build-workers:
  deploy-workers:
  needs: [build-workers]
  cache: workers-buildcache
  APP_DIR=workers
`)
	writeTestFile(t, repo, "packages/core/utils/workers.ts", `export function createWorker() {}`)
	writeTestFile(t, repo, "Dockerfile", `ARG APP_DIR=workers`)

	result, err := (Server{Repo: repo}).prepareRenamePlan(t.Context(), map[string]any{
		"path":        "apps/workers",
		"packageName": "@howdy/workers",
		"shortName":   "workers",
		"detail":      "files",
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result["planKind"] != "rename" {
		t.Fatalf("planKind=%v", result["planKind"])
	}
	if intValue(result["mustEditCount"]) == 0 {
		if len(mapSlice(result["identities"])) == 0 {
			t.Fatalf("empty plan: %#v", result)
		}
	}
	if totals, ok := result["totals"].(map[string]any); !ok || intValue(totals["mustEditFiles"]) == 0 {
		// fixture should have some path hits
		if intValue(result["mustEditCount"]) == 0 {
			t.Fatalf("missing totals.mustEditFiles: %#v", result["totals"])
		}
	}
	if _, ok := result["reportUsing"]; !ok {
		t.Fatalf("missing reportUsing guidance")
	}
	// mustNotTouch should include core workers util
	notTouch := stringSlice(result["mustNotTouch"])
	found := false
	for _, p := range notTouch {
		if p == "packages/core/utils/workers.ts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mustNotTouch missing core util: %v", notTouch)
	}
	// decisions present
	if len(mapSlice(result["decisions"])) < 2 {
		t.Fatalf("expected decisions: %#v", result["decisions"])
	}
	if len(stringSlice(result["successCriteria"])) == 0 {
		t.Fatalf("missing successCriteria")
	}
	// scan should be complete on tiny fixture
	if trunc, _ := result["scanTruncated"].(bool); trunc {
		t.Fatalf("scanTruncated on fixture")
	}
}

func TestPrepareRenamePlanToolListed(t *testing.T) {
	response, ok := (Server{}).Process(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if !ok {
		t.Fatal("tools/list failed")
	}
	raw, _ := json.Marshal(response)
	if !strings.Contains(string(raw), `"prepare_rename_plan"`) {
		t.Fatalf("prepare_rename_plan not advertised")
	}
}


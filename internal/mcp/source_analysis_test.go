package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchLiteralFilesClassifiesEnvReferences(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "packages/core/services/integration/client.ts", `
export function login() {
  return process.env.SERVICE_LOGIN_EMAIL;
}
`)
	writeTestFile(t, repo, "packages/core/services/integration/client.test.ts", `
process.env.SERVICE_LOGIN_EMAIL = "test@example.com";
delete process.env.SERVICE_LOGIN_EMAIL;
`)
	writeTestFile(t, repo, "scripts/reconcile-integration/reconcile-utils.ts", `
const loginEmail = process.env.SERVICE_LOGIN_EMAIL;
`)
	writeTestFile(t, repo, "README.md", `
Set SERVICE_LOGIN_EMAIL locally.
`)
	writeTestFile(t, repo, ".env", `
SERVICE_LOGIN_EMAIL=real-secret@example.com
`)
	writeTestFile(t, repo, "node_modules/ignored/index.ts", `
process.env.SERVICE_LOGIN_EMAIL;
`)

	result, err := searchLiteralFiles(repo, "SERVICE_LOGIN_EMAIL", literalSearchOptions{
		IncludeTests:    true,
		IncludeDocs:     true,
		IncludeConfig:   true,
		IncludeScripts:  true,
		IncludeHidden:   true,
		IncludeLines:    true,
		IncludeSnippets: true,
		MatchesPerFile:  4,
	})
	if err != nil {
		t.Fatalf("searchLiteralFiles error = %v", err)
	}

	if result.UniqueFiles != 5 {
		t.Fatalf("UniqueFiles = %d, want 5: %#v", result.UniqueFiles, result.Files)
	}
	if result.TotalMatches != 6 {
		t.Fatalf("TotalMatches = %d, want 6", result.TotalMatches)
	}
	assertCount(t, result.Counts, "runtime", 1)
	assertCount(t, result.Counts, "test", 1)
	assertCount(t, result.Counts, "script", 1)
	assertCount(t, result.Counts, "docs", 1)
	assertCount(t, result.Counts, "config", 1)

	envFile := findLiteralFile(t, result.Files, ".env")
	if got := envFile.Matches[0].Snippet; got != "SERVICE_LOGIN_EMAIL=<redacted>" {
		t.Fatalf("env snippet = %q, want redacted assignment", got)
	}

	runtimeFile := findLiteralFile(t, result.Files, "packages/core/services/integration/client.ts")
	if runtimeFile.Flags == nil || runtimeFile.Flags["runtimeEnvRead"] != true {
		t.Fatalf("runtime file flags = %#v, want runtimeEnvRead", runtimeFile.Flags)
	}
}

func TestSearchLiteralFilesCanOmitNonRuntimeCategories(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/runtime.ts", `process.env.SERVICE_LOGIN_EMAIL;`)
	writeTestFile(t, repo, "src/runtime.test.ts", `process.env.SERVICE_LOGIN_EMAIL = "test";`)
	writeTestFile(t, repo, "README.md", `SERVICE_LOGIN_EMAIL`)
	writeTestFile(t, repo, ".env", `SERVICE_LOGIN_EMAIL=secret`)

	result, err := searchLiteralFiles(repo, "SERVICE_LOGIN_EMAIL", literalSearchOptions{
		IncludeTests:  false,
		IncludeDocs:   false,
		IncludeConfig: false,
		IncludeHidden: false,
	})
	if err != nil {
		t.Fatalf("searchLiteralFiles error = %v", err)
	}

	if result.UniqueFiles != 1 {
		t.Fatalf("UniqueFiles = %d, want 1: %#v", result.UniqueFiles, result.Files)
	}
	if result.Files[0].Path != "src/runtime.ts" {
		t.Fatalf("file path = %q, want src/runtime.ts", result.Files[0].Path)
	}
}

func TestSearchLiteralFilesSkipsHiddenAndTmpByDefault(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/runtime.ts", `process.env.SERVICE_LOGIN_EMAIL;`)
	writeTestFile(t, repo, ".env", `SERVICE_LOGIN_EMAIL=secret`)
	writeTestFile(t, repo, "tmp/scratch.ts", `process.env.SERVICE_LOGIN_EMAIL;`)

	result, err := searchLiteralFiles(repo, "SERVICE_LOGIN_EMAIL", literalSearchOptions{})
	if err != nil {
		t.Fatalf("searchLiteralFiles error = %v", err)
	}

	if result.UniqueFiles != 1 {
		t.Fatalf("UniqueFiles = %d, want 1: %#v", result.UniqueFiles, result.Files)
	}
	if result.Files[0].Path != "src/runtime.ts" {
		t.Fatalf("file path = %q, want src/runtime.ts", result.Files[0].Path)
	}
}

func TestSearchLiteralFilesHandlesLongLines(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/generated.ts", strings.Repeat("x", 70_000)+"SERVICE_LOGIN_EMAIL")

	result, err := searchLiteralFiles(repo, "SERVICE_LOGIN_EMAIL", literalSearchOptions{})
	if err != nil {
		t.Fatalf("searchLiteralFiles error = %v", err)
	}

	if result.UniqueFiles != 1 {
		t.Fatalf("UniqueFiles = %d, want 1", result.UniqueFiles)
	}
}

func TestFindEnvUsagesReturnsOnlyRuntimeReads(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/client.ts", `
const key = "SERVICE_LOGIN_EMAIL";
const loginEmail = process.env.SERVICE_LOGIN_EMAIL;
`)
	writeTestFile(t, repo, "src/client.test.ts", `process.env.SERVICE_LOGIN_EMAIL = "test";`)
	writeTestFile(t, repo, "README.md", `SERVICE_LOGIN_EMAIL`)
	writeTestFile(t, repo, ".env", `SERVICE_LOGIN_EMAIL=secret`)

	result, err := (Server{Repo: repo}).findEnvUsages("SERVICE_LOGIN_EMAIL", map[string]any{"lines": true})
	if err != nil {
		t.Fatalf("findEnvUsages error = %v", err)
	}

	if result["uniqueFiles"] != 1 {
		t.Fatalf("uniqueFiles = %v, want 1", result["uniqueFiles"])
	}
	if result["runtimeReadCount"] != 1 {
		t.Fatalf("runtimeReadCount = %v, want 1", result["runtimeReadCount"])
	}
	files := result["files"].([]map[string]any)
	if files[0]["path"] != "src/client.ts" {
		t.Fatalf("path = %q, want src/client.ts", files[0]["path"])
	}
}

func TestCountLiteralFilesReturnsPathsOnly(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/client.ts", `process.env.SERVICE_LOGIN_EMAIL;`)
	writeTestFile(t, repo, "README.md", `SERVICE_LOGIN_EMAIL`)
	writeTestFile(t, repo, ".env", `SERVICE_LOGIN_EMAIL=secret`)

	result, err := (Server{Repo: repo}).countLiteralFiles("SERVICE_LOGIN_EMAIL", map[string]any{})
	if err != nil {
		t.Fatalf("countLiteralFiles error = %v", err)
	}

	if result["uniqueFiles"] != 2 {
		t.Fatalf("uniqueFiles = %v, want 2", result["uniqueFiles"])
	}
	files := result["files"].([]string)
	if len(files) != 2 || files[0] != "README.md" || files[1] != "src/client.ts" {
		t.Fatalf("files = %#v, want README.md and src/client.ts", files)
	}
}

func TestAnalyzeFunctionImpactClassifiesBlastRadius(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "packages/core/lib/integrationMetrics.js", `
export function resolveTenantAccount() {
  return 'unknown';
}
`)
	writeTestFile(t, repo, "packages/core/server/integration.js", `
import { resolveTenantAccount } from '../lib/integrationMetrics';

export function recordMetric() {
  resolveTenantAccount();
}
`)
	writeTestFile(t, repo, "packages/core/lib/__tests__/integrationMetrics.test.ts", `
import { resolveTenantAccount } from '../integrationMetrics';

test('account', () => {
  resolveTenantAccount();
});

const label = 'resolveTenantAccount';
`)
	writeTestFile(t, repo, "README.md", `resolveTenantAccount()`)

	result, err := analyzeFunctionImpact(repo, "resolveTenantAccount", functionImpactOptions{
		IncludeTests:   true,
		IncludeScripts: true,
		IncludeLines:   true,
	})
	if err != nil {
		t.Fatalf("analyzeFunctionImpact error = %v", err)
	}

	if result.UniqueFiles != 3 {
		t.Fatalf("UniqueFiles = %d, want 3: %#v", result.UniqueFiles, result.Files)
	}
	if len(result.Definitions) != 1 || result.Definitions[0].Path != "packages/core/lib/integrationMetrics.js" {
		t.Fatalf("definitions = %#v, want integrationMetrics.js", result.Definitions)
	}
	if len(result.Imports) != 2 {
		t.Fatalf("imports = %#v, want 2 imports", result.Imports)
	}
	if len(result.CallSites) != 2 {
		t.Fatalf("callSites = %#v, want 2 call sites", result.CallSites)
	}
	serverCall := findFunctionMatch(t, result.CallSites, "packages/core/server/integration.js")
	if len(serverCall.Owners) != 1 || serverCall.Owners[0] != "recordMetric" {
		t.Fatalf("server call owners = %#v, want recordMetric", serverCall.Owners)
	}
	if len(result.References) != 1 || result.References[0].Path != "packages/core/lib/__tests__/integrationMetrics.test.ts" {
		t.Fatalf("references = %#v, want one test reference", result.References)
	}
	if result.CallSites[0].Matches[0].Line == 0 {
		t.Fatalf("callSites = %#v, want line numbers", result.CallSites)
	}
}

func TestTransitiveFunctionImpactFollowsOwnerCallers(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/metrics.ts", `
export const resolveTenantAccount = () => 'unknown';
export const recordExternalMetric = () => {
  resolveTenantAccount();
};
export async function trackExternalCall() {
  recordExternalMetric();
}
`)
	writeTestFile(t, repo, "src/worker.ts", `
import { trackExternalCall } from './metrics';

export async function syncWorker() {
  await trackExternalCall();
}
`)

	impact, err := analyzeFunctionImpact(repo, "resolveTenantAccount", functionImpactOptions{})
	if err != nil {
		t.Fatalf("analyzeFunctionImpact error = %v", err)
	}
	transitive := (Server{Repo: repo}).transitiveFunctionImpact("resolveTenantAccount", impact, 2, 8, false, true, false)

	if len(transitive) != 2 {
		t.Fatalf("transitive = %#v, want two owner levels", transitive)
	}
	if transitive[0]["symbol"] != "recordExternalMetric" {
		t.Fatalf("first transitive symbol = %v, want recordExternalMetric", transitive[0]["symbol"])
	}
	if transitive[1]["symbol"] != "trackExternalCall" {
		t.Fatalf("second transitive symbol = %v, want trackExternalCall", transitive[1]["symbol"])
	}
}

func TestAnalyzeCallsiteContractFindsMissingRequiredPrecheck(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/runtime.ts", `
export function createAccount() {
  assertActionAllowed();
  performAction();
}

export function updateAccount() {
  performAction();
}

const destroyAccount = () => {
  assertActionAllowed();
  performAction();
}

// performAction();
`)
	writeTestFile(t, repo, "src/runtime.test.ts", `
test('calls external service', () => {
  performAction();
});
`)
	writeTestFile(t, repo, "README.md", `performAction()`)

	result, err := analyzeCallsiteContract(repo, "performAction", "assertActionAllowed", callsiteContractOptions{
		IncludeTests: true,
	})
	if err != nil {
		t.Fatalf("analyzeCallsiteContract error = %v", err)
	}

	if result.TotalCallSites != 4 {
		t.Fatalf("TotalCallSites = %d, want 4: %#v", result.TotalCallSites, result.CallSites)
	}
	if result.SatisfiedCallSites != 2 {
		t.Fatalf("SatisfiedCallSites = %d, want 2: %#v", result.SatisfiedCallSites, result.Satisfied)
	}
	if result.MissingCallSites != 2 {
		t.Fatalf("MissingCallSites = %d, want 2: %#v", result.MissingCallSites, result.Missing)
	}
	missingRuntime := findCallsiteContractMatch(t, result.Missing, "src/runtime.ts", "updateAccount")
	if missingRuntime.Line == 0 || missingRuntime.HasRequiredBeforeCall {
		t.Fatalf("missing runtime match = %#v, want unsatisfied updateAccount call", missingRuntime)
	}
	satisfiedArrow := findCallsiteContractMatch(t, result.Satisfied, "src/runtime.ts", "destroyAccount")
	if satisfiedArrow.RequiredLine == 0 {
		t.Fatalf("satisfied arrow match = %#v, want required line", satisfiedArrow)
	}
}

func TestAnalyzeCallsiteContractCanExcludeTests(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "src/runtime.ts", `
export function updateAccount() {
  performAction();
}
`)
	writeTestFile(t, repo, "src/runtime.test.ts", `
performAction();
`)

	result, err := analyzeCallsiteContract(repo, "performAction", "assertActionAllowed", callsiteContractOptions{
		IncludeTests: false,
	})
	if err != nil {
		t.Fatalf("analyzeCallsiteContract error = %v", err)
	}

	if result.TotalCallSites != 1 {
		t.Fatalf("TotalCallSites = %d, want 1: %#v", result.TotalCallSites, result.CallSites)
	}
	if result.Files[0] != "src/runtime.ts" {
		t.Fatalf("files = %#v, want only runtime file", result.Files)
	}
}

func TestRenameBucketPrefersRuntimeEnvReads(t *testing.T) {
	file := literalFileMatch{
		Path:     "scripts/reconcile-integration/reconcile-utils.ts",
		Category: "script",
		Flags:    map[string]interface{}{"runtimeEnvRead": true},
	}

	if got := renameBucket(file); got != "runtimeReads" {
		t.Fatalf("renameBucket = %q, want runtimeReads", got)
	}
}

func TestRenameBucketKeepsTestEnvReadsInTests(t *testing.T) {
	file := literalFileMatch{
		Path:     "packages/core/services/integration/client.test.ts",
		Category: "test",
		Flags:    map[string]interface{}{"runtimeEnvRead": true},
	}

	if got := renameBucket(file); got != "tests" {
		t.Fatalf("renameBucket = %q, want tests", got)
	}
}

func writeTestFile(t *testing.T, repo string, path string, contents string) {
	t.Helper()
	fullPath := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", fullPath, err)
	}
}

func assertCount(t *testing.T, counts map[string]int, category string, want int) {
	t.Helper()
	if counts[category] != want {
		t.Fatalf("counts[%q] = %d, want %d", category, counts[category], want)
	}
}

func findLiteralFile(t *testing.T, files []literalFileMatch, path string) literalFileMatch {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("file %q not found in %#v", path, files)
	return literalFileMatch{}
}

func findFunctionMatch(t *testing.T, files []functionFileMatch, path string) functionFileMatch {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("function match %q not found in %#v", path, files)
	return functionFileMatch{}
}

func findCallsiteContractMatch(t *testing.T, matches []callsiteContractMatch, path string, owner string) callsiteContractMatch {
	t.Helper()
	for _, match := range matches {
		if match.Path == path && match.Owner == owner {
			return match
		}
	}
	t.Fatalf("callsite contract match path=%q owner=%q not found in %#v", path, owner, matches)
	return callsiteContractMatch{}
}

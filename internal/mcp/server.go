package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/claudioscheer/code-graph-mcp/internal/graph"
)

type Server struct {
	Query graph.Service
	Repo  string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type toolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s Server) Serve(ctx context.Context, reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024*20)
	encoder := json.NewEncoder(writer)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		response, ok := s.Process(ctx, []byte(line))
		if !ok {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s Server) Process(ctx context.Context, payload []byte) (map[string]any, bool) {
	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		return errorResponse(nil, -32700, err.Error()), true
	}
	if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
		return nil, false
	}
	result, err := s.handle(ctx, req)
	if err != nil {
		return errorResponse(req.ID, -32603, err.Error()), true
	}
	return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}, true
}

func (s Server) handle(ctx context.Context, req request) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "codegraph", "version": "0.1.0"}}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		var params toolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
		return s.call(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported method %s", req.Method)
	}
}

func (s Server) call(ctx context.Context, params toolParams) (any, error) {
	args := map[string]any{}
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return nil, err
		}
	}
	opts := graph.Options{Depth: intArg(args, "depth", 2), Limit: intArg(args, "limit", 100), MinConfidence: floatArg(args, "minConfidence", 0.6)}
	var result any
	var err error
	switch params.Name {
	case "codegraph_help":
		result = s.help()
	case "get_ripple_info":
		result, err = s.Query.Metadata(ctx)
	case "list_node_types":
		result, err = s.Query.Types(ctx)
	case "get_index_freshness":
		result, err = s.indexFreshness(ctx)
	case "search_code":
		result, err = s.Query.Search(ctx, firstStringArg(args, "query", "q", "text", "term", "name", "path"), opts)
	case "count_literal_files":
		result, err = s.countLiteralFiles(firstStringArg(args, "query", "q", "text", "term"), args)
	case "search_literal":
		result, err = s.searchLiteral(firstStringArg(args, "query", "q", "text", "term"), args)
	case "find_env_usages":
		result, err = s.findEnvUsages(firstStringArg(args, "envName", "name", "query", "q", "text", "term"), args)
	case "analyze_rename_impact":
		result, err = s.analyzeRenameImpact(ctx, firstStringArg(args, "oldName", "query", "q", "text", "term"), stringArgDefault(args, "kind", "literal"), args)
	case "analyze_function_impact":
		result, err = s.analyzeFunctionImpact(ctx, firstStringArg(args, "symbol", "name", "query", "q", "text", "term"), args)
	case "analyze_callsite_contract":
		result, err = s.analyzeCallsiteContract(firstStringArg(args, "callee", "function", "symbol", "query", "q"), firstStringArg(args, "requiredBeforeCall", "required", "precheck", "check"), args)
	case "prepare_feature_context":
		result, err = s.prepareFeatureContext(ctx, firstStringArg(args, "query", "q", "feature", "symbol", "name", "path"), args)
	case "find_symbol":
		result, err = s.Query.FindSymbol(ctx, firstStringArg(args, "name", "query", "q", "symbol"), opts)
	case "find_file":
		result, err = s.Query.FindFile(ctx, firstStringArg(args, "path", "query", "q", "file"), opts)
	case "get_dependencies":
		opts.Direction = "forward"
		result, err = s.Query.Relations(ctx, nodeIDArg(args), opts)
	case "get_dependents":
		opts.Direction = "reverse"
		result, err = s.Query.Relations(ctx, nodeIDArg(args), opts)
	case "get_relations":
		opts.Direction = directionArg(args, "both")
		result, err = s.Query.Relations(ctx, nodeIDArg(args), opts)
	case "get_impact", "get_route_impact", "get_related_tests":
		opts.Direction = "reverse"
		result, err = s.Query.Relations(ctx, nodeIDArg(args), opts)
	case "find_paths":
		result, err = s.Query.Paths(ctx, firstStringArg(args, "fromId", "from", "sourceId", "startId", "source"), firstStringArg(args, "toId", "to", "targetId", "endId", "target"), opts)
	case "open_file_excerpt":
		result, err = s.openFile(firstStringArg(args, "path", "file", "filePath"), intArg(args, "startLine", 1), intArg(args, "endLine", 80))
	case "open_symbol_body":
		symbolID := firstStringArg(args, "symbolId", "id", "targetId", "sourceId")
		if s.Query.Ripple != "" {
			symbolID = strings.TrimPrefix(symbolID, s.Query.Ripple+":")
		}
		file, _, ok := strings.Cut(strings.TrimPrefix(symbolID, "symbol:"), "#")
		if !ok {
			return nil, fmt.Errorf("symbolId must be symbol:<path>#<name>")
		}
		result, err = s.openFile(file, 1, 200)
	default:
		err = fmt.Errorf("unknown tool %s", params.Name)
	}
	if err != nil {
		return nil, err
	}
	text, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}}, nil
}

func (s Server) help() map[string]any {
	return map[string]any{
		"purpose": "CodeGraph MCP helps investigate an indexed codebase by searching files, symbols, packages, routes, tests, and graph relationships inside the selected ripple.",
		"ripple":  s.Query.Ripple,
		"repo":    s.Repo,
		"howToChoose": []string{
			"Need starter context before feature work: use prepare_feature_context first.",
			"Need every call site for a function, hook, component, method, or exported symbol: use analyze_function_impact.",
			"Need to enforce a new pre-call check before existing calls: use analyze_callsite_contract.",
			"Need files affected by a rename: use analyze_rename_impact.",
			"Need to count exact text references: use count_literal_files.",
			"Need runtime process.env reads only: use find_env_usages.",
			"Need generic graph exploration: use search_code, then find_symbol or find_file, then get_relations.",
			"Need source text: open files only after a high-level command identifies the specific file or symbol.",
		},
		"recommendedWorkflow": []string{
			"For planning questions, prefer one high-level command before manual graph traversal.",
			"If the user asks for feature impact, call prepare_feature_context or analyze_function_impact before reading files.",
			"If the user asks whether all callers need a new guard/check, call analyze_callsite_contract.",
			"If the user asks how many files reference exact text, call count_literal_files rather than search_code.",
			"If the user asks which files read an env var at runtime, call find_env_usages rather than broad literal search.",
			"If the user asks what files change for a rename, call analyze_rename_impact.",
			"Use get_index_freshness when comparing indexed answers to a live checkout.",
			"Use list_node_types when you need to understand what graph data exists.",
			"Use search_code for broad terms only when no high-level command matches the task.",
			"Use find_file to narrow by path when the result mentions a directory.",
			"Use find_symbol to locate functions, classes, hooks, constants, and exported names.",
			"Use get_relations with direction outbound for dependencies and inbound for dependents.",
			"Use open_file_excerpt or open_symbol_body only after locating a specific file or symbol.",
		},
		"commandGuide": []map[string]any{
			{
				"tool":     "prepare_feature_context",
				"useWhen":  "Starting feature work and needing entry points, likely edit files, tests, dependencies, index freshness, and bounded blast radius.",
				"returns":  []string{"entryPoints", "directChangeFiles", "relatedTests", "blastRadius", "dependencySummary", "suggestedFollowUpReads"},
				"nextStep": "If contextCompleteForPlanning is true, plan from the response. Read only suggestedFollowUpReads when implementing or exact lines are needed.",
			},
			{
				"tool":     "analyze_function_impact",
				"useWhen":  "A named function, hook, component, method, or exported symbol may change behavior and callers need review.",
				"returns":  []string{"definitions", "imports", "callSites", "references", "transitive owner expansion", "graphSummary"},
				"nextStep": "Use callSites for direct edits and transitive owners for broader blast radius. Increase transitiveDepth only when needed.",
			},
			{
				"tool":     "analyze_callsite_contract",
				"useWhen":  "Every existing call to one callee must be preceded by another check or setup call in the same enclosing function.",
				"returns":  []string{"missing call sites", "satisfied call sites", "owners", "line numbers", "implementationGuidance"},
				"nextStep": "Edit missing call sites. Review unowned call sites manually because the scanner could not identify an enclosing function.",
			},
			{
				"tool":     "analyze_rename_impact",
				"useWhen":  "A literal, env var, path, package, or symbol is being renamed and stale references must be found.",
				"returns":  []string{"filesToChange grouped by runtime/config/tests/docs/scripts", "match counts", "graphSummary", "external notes"},
				"nextStep": "Update grouped files by priority: runtime first, then config/tests/docs/scripts. For env vars, update external secret stores too.",
			},
			{
				"tool":     "find_env_usages",
				"useWhen":  "You only care about runtime reads of process.env.NAME, not docs, tests, or config assignments.",
				"returns":  []string{"runtime read files", "read counts", "optional lines/snippets"},
				"nextStep": "Use lines or snippets only when exact edit locations are required.",
			},
			{
				"tool":     "count_literal_files",
				"useWhen":  "The user asks how many files contain exact text, or wants a compact literal reference count.",
				"returns":  []string{"unique file count", "total match count", "category counts", "paths"},
				"nextStep": "Use analyze_rename_impact if the count implies a rename or migration task.",
			},
			{
				"tool":     "search_code",
				"useWhen":  "Exploring an unknown concept, package, route, file path, or term where no higher-level command applies.",
				"returns":  []string{"matching indexed graph nodes"},
				"nextStep": "Use find_symbol, find_file, or get_relations on returned node IDs.",
			},
			{
				"tool":     "get_relations",
				"useWhen":  "You have a node ID and need dependency or dependent graph traversal.",
				"returns":  []string{"relationship paths from the indexed graph"},
				"nextStep": "Use direction outbound for dependencies and inbound/reverse for dependents.",
			},
		},
		"workflowRecipes": []map[string]any{
			{
				"task":  "Start feature work around a symbol",
				"calls": []map[string]any{{"tool": "prepare_feature_context", "arguments": map[string]any{"query": "resolveTenantAccount", "symbol": "resolveTenantAccount"}}},
			},
			{
				"task":  "Find blast radius of a behavior change",
				"calls": []map[string]any{{"tool": "analyze_function_impact", "arguments": map[string]any{"symbol": "resolveTenantAccount", "transitiveDepth": 1}}},
			},
			{
				"task":  "Require a guard before every call",
				"calls": []map[string]any{{"tool": "analyze_callsite_contract", "arguments": map[string]any{"callee": "performAction", "requiredBeforeCall": "assertActionAllowed", "includeTests": false}}},
			},
			{
				"task":  "Rename an env var",
				"calls": []map[string]any{{"tool": "analyze_rename_impact", "arguments": map[string]any{"oldName": "SERVICE_LOGIN_EMAIL", "kind": "env"}}},
			},
			{
				"task":  "Count exact references",
				"calls": []map[string]any{{"tool": "count_literal_files", "arguments": map[string]any{"query": "SERVICE_LOGIN_EMAIL"}}},
			},
			{
				"task": "Explore graph manually",
				"calls": []map[string]any{
					{"tool": "search_code", "arguments": map[string]any{"query": "billing", "limit": 20}},
					{"tool": "get_relations", "arguments": map[string]any{"sourceId": "file:src/main.ts", "direction": "both", "depth": 2}},
				},
			},
		},
		"outputGuidance": []string{
			"Prefer high-level summary fields over opening files when answering planning questions.",
			"Use snippets=false unless exact source text is required; snippets increase token usage.",
			"Report truncation when present and rerun with higher limits only if it changes the answer.",
			"Treat unowned call sites as requiring manual follow-up before edits.",
			"Use suggestedFollowUpReads from prepare_feature_context as the first files to inspect for implementation.",
		},
		"examples": []map[string]any{
			{"tool": "prepare_feature_context", "arguments": map[string]any{"query": "resolveTenantAccount"}},
			{"tool": "count_literal_files", "arguments": map[string]any{"query": "SERVICE_LOGIN_EMAIL"}},
			{"tool": "find_env_usages", "arguments": map[string]any{"envName": "SERVICE_LOGIN_EMAIL"}},
			{"tool": "analyze_rename_impact", "arguments": map[string]any{"oldName": "SERVICE_LOGIN_EMAIL", "kind": "env"}},
			{"tool": "analyze_function_impact", "arguments": map[string]any{"symbol": "resolveTenantAccount"}},
			{"tool": "analyze_callsite_contract", "arguments": map[string]any{"callee": "performAction", "requiredBeforeCall": "assertActionAllowed"}},
			{"tool": "search_code", "arguments": map[string]any{"query": "billing", "limit": 20}},
			{"tool": "find_file", "arguments": map[string]any{"query": "src/main.ts", "limit": 20}},
			{"tool": "find_symbol", "arguments": map[string]any{"query": "BillingService", "limit": 20}},
			{"tool": "get_relations", "arguments": map[string]any{"sourceId": "package:@app/main", "direction": "outbound", "depth": 2, "limit": 25}},
			{"tool": "get_relations", "arguments": map[string]any{"sourceId": "file:src/main.ts", "direction": "inbound", "depth": 2, "limit": 25}},
			{"tool": "open_file_excerpt", "arguments": map[string]any{"path": "src/main.ts", "startLine": 1, "endLine": 120}},
		},
		"argumentAliases": map[string]any{
			"search":    []string{"query", "q", "text", "term", "name", "path"},
			"nodeId":    []string{"targetId", "sourceId", "id", "nodeId", "query", "q"},
			"direction": map[string]string{"outbound": "forward", "out": "forward", "incoming": "reverse", "inbound": "reverse", "in": "reverse"},
			"filePath":  []string{"path", "file", "filePath"},
		},
		"tools": []string{
			"codegraph_help",
			"get_ripple_info",
			"get_index_freshness",
			"list_node_types",
			"count_literal_files",
			"find_env_usages",
			"analyze_rename_impact",
			"analyze_function_impact",
			"analyze_callsite_contract",
			"prepare_feature_context",
			"search_code",
			"find_symbol",
			"find_file",
			"get_dependencies",
			"get_dependents",
			"get_relations",
			"get_impact",
			"get_route_impact",
			"get_related_tests",
			"find_paths",
			"open_symbol_body",
			"open_file_excerpt",
		},
	}
}

func (s Server) openFile(path string, start int, end int) (map[string]any, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	full := filepath.Join(s.Repo, clean)
	if !strings.HasPrefix(full, filepath.Clean(s.Repo)) {
		return nil, fmt.Errorf("path escapes repo")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	if start > len(lines) {
		return map[string]any{"path": path, "text": ""}, nil
	}
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{"path": path, "startLine": start, "endLine": end, "text": strings.Join(lines[start-1:end], "\n")}, nil
}

func (s Server) searchLiteral(query string, args map[string]any) (map[string]any, error) {
	result, err := searchLiteralFiles(s.Repo, query, literalSearchOptions{
		IncludeTests:    boolArg(args, "includeTests", true),
		IncludeDocs:     boolArg(args, "includeDocs", true),
		IncludeConfig:   boolArg(args, "includeConfig", true),
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    boolArg(args, "lines", false),
		IncludeSnippets: boolArg(args, "snippets", false),
		MatchesPerFile:  intArg(args, "matchesPerFile", 3),
		Limit:           intArg(args, "limit", 100),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"query":        result.Query,
		"uniqueFiles":  result.UniqueFiles,
		"totalMatches": result.TotalMatches,
		"counts":       result.Counts,
		"files":        result.Files,
		"truncated":    result.Truncated,
	}, nil
}

func (s Server) countLiteralFiles(query string, args map[string]any) (map[string]any, error) {
	result, err := searchLiteralFiles(s.Repo, query, literalSearchOptions{
		IncludeTests:    boolArg(args, "includeTests", true),
		IncludeDocs:     boolArg(args, "includeDocs", true),
		IncludeConfig:   boolArg(args, "includeConfig", true),
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    false,
		IncludeSnippets: false,
		Limit:           intArg(args, "limit", 100),
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		paths = append(paths, file.Path)
	}
	return map[string]any{
		"query":        result.Query,
		"uniqueFiles":  result.UniqueFiles,
		"totalMatches": result.TotalMatches,
		"counts":       result.Counts,
		"files":        paths,
		"truncated":    result.Truncated,
	}, nil
}

func (s Server) findEnvUsages(envName string, args map[string]any) (map[string]any, error) {
	search, err := searchLiteralFiles(s.Repo, envName, literalSearchOptions{
		IncludeTests:    boolArg(args, "includeTests", false),
		IncludeDocs:     false,
		IncludeConfig:   false,
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    boolArg(args, "lines", false),
		IncludeSnippets: boolArg(args, "snippets", false),
		MatchesPerFile:  intArg(args, "matchesPerFile", 20),
		Limit:           intArg(args, "limit", 100),
	})
	if err != nil {
		return nil, err
	}
	files := []map[string]any{}
	totalReads := 0
	for _, file := range search.Files {
		readCount := file.KindCounts["runtime_env_read"]
		if readCount == 0 {
			continue
		}
		entry := map[string]any{"path": file.Path, "category": file.Category, "readCount": readCount}
		matches := filterMatchesByKind(file.Matches, "runtime_env_read")
		if len(matches) > 0 {
			entry["matches"] = matches
		}
		files = append(files, entry)
		totalReads += readCount
	}
	return map[string]any{
		"envName":          envName,
		"uniqueFiles":      len(files),
		"runtimeReadCount": totalReads,
		"files":            files,
	}, nil
}

func (s Server) indexFreshness(ctx context.Context) (map[string]any, error) {
	metadata, err := s.Query.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	metadata["repo"] = s.Repo
	metadata["localHead"] = gitOutput(s.Repo, "rev-parse", "HEAD")
	metadata["localBranch"] = gitOutput(s.Repo, "rev-parse", "--abbrev-ref", "HEAD")
	return metadata, nil
}

func filterMatchesByKind(matches []literalLineMatch, kind string) []literalLineMatch {
	filtered := []literalLineMatch{}
	for _, match := range matches {
		if match.Kind == kind {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func (s Server) analyzeRenameImpact(ctx context.Context, oldName string, kind string, args map[string]any) (map[string]any, error) {
	search, err := searchLiteralFiles(s.Repo, oldName, literalSearchOptions{
		IncludeTests:    true,
		IncludeDocs:     true,
		IncludeConfig:   true,
		IncludeScripts:  true,
		IncludeHidden:   boolArg(args, "includeHidden", true),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    boolArg(args, "lines", false),
		IncludeSnippets: boolArg(args, "snippets", false),
		MatchesPerFile:  intArg(args, "matchesPerFile", 6),
		Limit:           intArg(args, "limit", 200),
	})
	if err != nil {
		return nil, err
	}
	filesByBucket := map[string][]map[string]any{
		"runtimeReads": {},
		"scripts":      {},
		"config":       {},
		"tests":        {},
		"docs":         {},
		"other":        {},
	}
	relationPaths := []string{}
	for _, file := range search.Files {
		entry := map[string]any{"path": file.Path, "matchCount": file.MatchCount}
		if len(file.Matches) > 0 {
			entry["matches"] = file.Matches
		}
		bucket := renameBucket(file)
		filesByBucket[bucket] = append(filesByBucket[bucket], entry)
		if bucket == "runtimeReads" || bucket == "scripts" {
			relationPaths = append(relationPaths, file.Path)
		}
	}
	relationSummary := map[string]any{"files": []map[string]any{}}
	if boolArg(args, "includeGraph", true) && len(relationPaths) > 0 {
		relationSummary, err = s.Query.FileRelationSummary(ctx, relationPaths, intArg(args, "relationExamples", 5))
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"oldName":       oldName,
		"kind":          kind,
		"uniqueFiles":   search.UniqueFiles,
		"totalMatches":  search.TotalMatches,
		"counts":        search.Counts,
		"filesToChange": filesByBucket,
		"graphSummary":  relationSummary,
		"external":      externalRenameNotes(kind),
		"truncated":     search.Truncated,
	}, nil
}

func (s Server) analyzeFunctionImpact(ctx context.Context, symbol string, args map[string]any) (map[string]any, error) {
	includeTests := boolArg(args, "includeTests", true)
	includeScripts := boolArg(args, "includeScripts", true)
	includeTmp := boolArg(args, "includeTmp", false)
	impact, err := analyzeFunctionImpact(s.Repo, symbol, functionImpactOptions{
		IncludeTests:    includeTests,
		IncludeDocs:     boolArg(args, "includeDocs", false),
		IncludeConfig:   boolArg(args, "includeConfig", false),
		IncludeScripts:  includeScripts,
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      includeTmp,
		IncludeLines:    boolArg(args, "lines", false),
		IncludeSnippets: boolArg(args, "snippets", false),
		MatchesPerFile:  intArg(args, "matchesPerFile", 5),
		Limit:           intArg(args, "limit", 100),
	})
	if err != nil {
		return nil, err
	}
	graphPaths := pathsForFunctionGraph(impact)
	graphSummary := map[string]any{"files": []map[string]any{}}
	if boolArg(args, "includeGraph", true) && len(graphPaths) > 0 {
		graphSummary, err = s.Query.FileRelationSummary(ctx, graphPaths, intArg(args, "relationExamples", 5))
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"symbol":       symbol,
		"uniqueFiles":  impact.UniqueFiles,
		"totalHits":    impact.TotalHits,
		"counts":       impact.Counts,
		"definitions":  compactFunctionMatches(impact.Definitions),
		"imports":      compactFunctionMatches(impact.Imports),
		"callSites":    compactFunctionMatches(impact.CallSites),
		"references":   compactFunctionMatches(impact.References),
		"transitive":   s.transitiveFunctionImpact(symbol, impact, intArg(args, "transitiveDepth", 1), intArg(args, "maxTransitiveSymbols", 8), includeTests, includeScripts, includeTmp),
		"graphSummary": graphSummary,
		"truncated":    impact.Truncated,
	}, nil
}

func (s Server) analyzeCallsiteContract(callee string, requiredBeforeCall string, args map[string]any) (map[string]any, error) {
	resultLimit := intArg(args, "resultLimit", 30)
	result, err := analyzeCallsiteContract(s.Repo, callee, requiredBeforeCall, callsiteContractOptions{
		IncludeTests:    boolArg(args, "includeTests", true),
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeSnippets: boolArg(args, "snippets", false),
		Limit:           intArg(args, "limit", 200),
	})
	if err != nil {
		return nil, err
	}
	response := map[string]any{
		"callee":                 result.Callee,
		"requiredBeforeCall":     result.RequiredBeforeCall,
		"uniqueFiles":            result.UniqueFiles,
		"totalCallSites":         result.TotalCallSites,
		"missingCallSites":       result.MissingCallSites,
		"satisfiedCallSites":     result.SatisfiedCallSites,
		"unownedCallSites":       result.UnownedCallSites,
		"files":                  result.Files,
		"missing":                compactCallsiteContractMatches(result.Missing, resultLimit),
		"satisfied":              compactCallsiteContractMatches(result.Satisfied, resultLimit),
		"implementationGuidance": callsiteContractGuidance(result),
		"truncated":              result.Truncated,
	}
	if boolArg(args, "includeAllCallSites", false) {
		response["callSites"] = compactCallsiteContractMatches(result.CallSites, resultLimit)
	}
	return response, nil
}

func (s Server) prepareFeatureContext(ctx context.Context, query string, args map[string]any) (map[string]any, error) {
	limit := intArg(args, "limit", 12)
	graphMatches := map[string]any{"nodes": []map[string]any{}}
	var err error
	if query != "" {
		graphMatches, err = s.Query.Search(ctx, query, graph.Options{Limit: limit})
		if err != nil {
			return nil, err
		}
	}
	sourceMatches, err := searchLiteralFiles(s.Repo, query, literalSearchOptions{
		IncludeTests:    true,
		IncludeDocs:     true,
		IncludeConfig:   true,
		IncludeScripts:  true,
		IncludeHidden:   false,
		IncludeTmp:      false,
		IncludeLines:    false,
		IncludeSnippets: false,
		Limit:           limit,
	})
	if err != nil {
		return nil, err
	}
	paths := pathsFromFeatureContext(graphMatches, sourceMatches)
	graphSummary := map[string]any{"files": []map[string]any{}}
	if len(paths) > 0 {
		graphSummary, err = s.Query.FileRelationSummary(ctx, paths, intArg(args, "relationExamples", 4))
		if err != nil {
			return nil, err
		}
	}
	index, err := s.indexFreshness(ctx)
	if err != nil {
		return nil, err
	}
	symbol := firstStringArg(args, "symbol", "name")
	if symbol == "" && isIdentifier(query) {
		symbol = query
	}
	impactSummary := map[string]any{}
	entryPoints := entryPointsFromGraph(graphMatches)
	directChangeFiles := []map[string]any{}
	tests := relatedTests(sourceMatches.Files)
	blastRadius := map[string]any{}
	if symbol != "" {
		impact, err := analyzeFunctionImpact(s.Repo, symbol, functionImpactOptions{
			IncludeTests:   true,
			IncludeScripts: true,
			Limit:          intArg(args, "impactLimit", 80),
		})
		if err != nil {
			return nil, err
		}
		transitiveDepth := min(intArg(args, "transitiveDepth", 1), 1)
		maxTransitiveSymbols := min(intArg(args, "maxTransitiveSymbols", 4), 4)
		transitive := s.transitiveFunctionImpact(symbol, impact, transitiveDepth, maxTransitiveSymbols, true, true, false)
		impactSummary = compactFeatureImpact(impact, transitive)
		entryPoints = appendEntryPoints(entryPoints, impact.Definitions)
		directChangeFiles = directChangeFilesFromImpact(impact)
		tests = mergeTestFiles(tests, testFilesFromImpact(impact))
		blastRadius = map[string]any{
			"directCallSites": summarizeFunctionMatches(impact.CallSites, 12),
			"references":      summarizeFunctionMatches(impact.References, 8),
			"indirectOwners":  compactTransitiveOwners(transitive),
		}
	}
	return map[string]any{
		"query":                      query,
		"contextCompleteForPlanning": true,
		"index":                      index,
		"entryPoints":                entryPoints,
		"directChangeFiles":          directChangeFiles,
		"relatedTests":               tests,
		"blastRadius":                blastRadius,
		"dependencySummary":          graphSummary,
		"sourceMatches":              compactLiteralSearch(sourceMatches),
		"graphMatches":               compactGraphNodes(graphMatches),
		"impactSummary":              impactSummary,
		"suggestedFollowUpReads":     suggestedFollowUpReads(entryPoints, directChangeFiles, tests),
	}, nil
}

func entryPointsFromGraph(result map[string]any) []map[string]any {
	nodes := compactGraphNodes(result)
	entryPoints := []map[string]any{}
	seen := map[string]bool{}
	for _, node := range nodes {
		path := firstAnyString(node, "path")
		if path == "" || seen[path] {
			continue
		}
		entry := map[string]any{
			"path":   path,
			"reason": "graph_match",
		}
		for _, key := range []string{"kind", "name"} {
			if value, ok := node[key]; ok && value != nil {
				entry[key] = value
			}
		}
		entryPoints = append(entryPoints, entry)
		seen[path] = true
		if len(entryPoints) >= 6 {
			break
		}
	}
	return entryPoints
}

func appendEntryPoints(entryPoints []map[string]any, definitions []functionFileMatch) []map[string]any {
	seen := map[string]bool{}
	for _, entry := range entryPoints {
		if path := firstAnyString(entry, "path"); path != "" {
			seen[path] = true
		}
	}
	for _, match := range definitions {
		if seen[match.Path] {
			continue
		}
		entryPoints = append(entryPoints, map[string]any{
			"path":   match.Path,
			"reason": "symbol_definition",
		})
		seen[match.Path] = true
	}
	return entryPoints
}

func directChangeFilesFromImpact(impact functionImpactResult) []map[string]any {
	files := []map[string]any{}
	seen := map[string]bool{}
	groups := []struct {
		role    string
		matches []functionFileMatch
	}{
		{"definition", impact.Definitions},
		{"import", impact.Imports},
		{"call_site", impact.CallSites},
	}
	for _, group := range groups {
		for _, match := range group.matches {
			if match.Category == "test" || seen[match.Path] {
				continue
			}
			entry := map[string]any{
				"path":     match.Path,
				"category": match.Category,
				"role":     group.role,
				"hitCount": match.HitCount,
			}
			if len(match.Owners) > 0 && group.role == "call_site" {
				entry["owners"] = limitedStrings(match.Owners, 4)
			}
			files = append(files, entry)
			seen[match.Path] = true
			if len(files) >= 12 {
				return files
			}
		}
	}
	return files
}

func testFilesFromImpact(impact functionImpactResult) []map[string]any {
	tests := []map[string]any{}
	seen := map[string]bool{}
	for _, group := range [][]functionFileMatch{impact.Definitions, impact.Imports, impact.CallSites, impact.References} {
		for _, match := range group {
			if match.Category != "test" || seen[match.Path] {
				continue
			}
			tests = append(tests, map[string]any{"path": match.Path, "hitCount": match.HitCount})
			seen[match.Path] = true
		}
	}
	return tests
}

func mergeTestFiles(left []map[string]any, right []map[string]any) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, group := range [][]map[string]any{left, right} {
		for _, entry := range group {
			path := firstAnyString(entry, "path")
			if path == "" || seen[path] {
				continue
			}
			out = append(out, entry)
			seen[path] = true
		}
	}
	return out
}

func compactFeatureImpact(impact functionImpactResult, transitive []map[string]any) map[string]any {
	return map[string]any{
		"symbol":                 impact.Symbol,
		"uniqueFiles":            impact.UniqueFiles,
		"totalHits":              impact.TotalHits,
		"definitions":            summarizeFunctionMatches(impact.Definitions, 6),
		"imports":                summarizeFunctionMatches(impact.Imports, 8),
		"directCallSiteFiles":    len(impact.CallSites),
		"referenceOnlyFiles":     len(impact.References),
		"expandedIndirectOwners": len(transitive),
		"truncated":              impact.Truncated,
	}
}

func compactTransitiveOwners(transitive []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(transitive))
	for _, item := range transitive {
		callSites := item["callSites"]
		if values, ok := callSites.([]map[string]any); ok && len(values) > 8 {
			callSites = values[:8]
		}
		out = append(out, map[string]any{
			"level":       item["level"],
			"symbol":      item["symbol"],
			"uniqueFiles": item["uniqueFiles"],
			"totalHits":   item["totalHits"],
			"callSites":   callSites,
		})
	}
	return out
}

func suggestedFollowUpReads(entryPoints []map[string]any, directChangeFiles []map[string]any, tests []map[string]any) []map[string]any {
	reads := []map[string]any{}
	seen := map[string]bool{}
	for _, group := range [][]map[string]any{entryPoints, directChangeFiles, tests} {
		for _, entry := range group {
			path := firstAnyString(entry, "path")
			if path == "" || seen[path] {
				continue
			}
			reads = append(reads, map[string]any{"path": path, "why": followUpReadReason(entry)})
			seen[path] = true
			if len(reads) >= 4 {
				return reads
			}
		}
	}
	return reads
}

func followUpReadReason(entry map[string]any) string {
	if reason := firstAnyString(entry, "reason"); reason != "" {
		return reason
	}
	if category := firstAnyString(entry, "category"); category == "test" {
		return "related_test"
	}
	return "likely_edit_or_validation_file"
}

func limitedStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func compactGraphNodes(result map[string]any) []map[string]any {
	rawNodes, _ := result["nodes"].([]map[string]any)
	nodes := make([]map[string]any, 0, len(rawNodes))
	for _, node := range rawNodes {
		entry := map[string]any{
			"id":    node["sourceId"],
			"label": node["primaryLabel"],
			"path":  firstAnyString(node, "path", "filePath"),
		}
		for _, key := range []string{"name", "kind", "packageId"} {
			if value, ok := node[key]; ok && value != nil {
				entry[key] = value
			}
		}
		nodes = append(nodes, entry)
	}
	return nodes
}

func compactLiteralSearch(result literalSearchResult) map[string]any {
	files := make([]map[string]any, 0, len(result.Files))
	for _, file := range result.Files {
		files = append(files, map[string]any{"path": file.Path, "category": file.Category, "matchCount": file.MatchCount})
	}
	return map[string]any{"uniqueFiles": result.UniqueFiles, "totalMatches": result.TotalMatches, "counts": result.Counts, "files": files, "truncated": result.Truncated}
}

func pathsFromFeatureContext(graphMatches map[string]any, sourceMatches literalSearchResult) []string {
	seen := map[string]bool{}
	paths := []string{}
	for _, file := range sourceMatches.Files {
		if file.Category == "docs" || file.Category == "config" || seen[file.Path] {
			continue
		}
		paths = append(paths, file.Path)
		seen[file.Path] = true
	}
	if rawNodes, ok := graphMatches["nodes"].([]map[string]any); ok {
		for _, node := range rawNodes {
			path := firstAnyString(node, "path", "filePath")
			if path == "" || seen[path] {
				continue
			}
			paths = append(paths, path)
			seen[path] = true
			if len(paths) >= 25 {
				break
			}
		}
	}
	return paths
}

func relatedTests(files []literalFileMatch) []map[string]any {
	tests := []map[string]any{}
	for _, file := range files {
		if file.Category == "test" {
			tests = append(tests, map[string]any{"path": file.Path, "matchCount": file.MatchCount})
		}
	}
	return tests
}

func firstAnyString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, char := range value {
		if i == 0 {
			if !(char == '_' || char == '$' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z') {
				return false
			}
			continue
		}
		if !(char == '_' || char == '$' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func (s Server) transitiveFunctionImpact(root string, impact functionImpactResult, depth int, maxSymbols int, includeTests bool, includeScripts bool, includeTmp bool) []map[string]any {
	if depth <= 0 || maxSymbols <= 0 {
		return []map[string]any{}
	}
	seen := map[string]bool{root: true}
	queue := ownerNamesFromCalls(impact.CallSites, seen)
	results := []map[string]any{}
	for level := 1; level <= depth && len(queue) > 0; level++ {
		nextQueue := []string{}
		for _, owner := range queue {
			if len(results) >= maxSymbols {
				return results
			}
			if seen[owner] {
				continue
			}
			seen[owner] = true
			ownerImpact, err := analyzeFunctionImpact(s.Repo, owner, functionImpactOptions{
				IncludeTests:   includeTests,
				IncludeScripts: includeScripts,
				IncludeTmp:     includeTmp,
				Limit:          100,
			})
			if err != nil {
				continue
			}
			results = append(results, map[string]any{
				"level":       level,
				"symbol":      owner,
				"uniqueFiles": ownerImpact.UniqueFiles,
				"totalHits":   ownerImpact.TotalHits,
				"definitions": compactFunctionMatches(ownerImpact.Definitions),
				"callSites":   summarizeFunctionMatches(ownerImpact.CallSites, 20),
				"references":  summarizeFunctionMatches(ownerImpact.References, 10),
			})
			nextQueue = append(nextQueue, ownerNamesFromCalls(ownerImpact.CallSites, seen)...)
		}
		queue = nextQueue
	}
	return results
}

func summarizeFunctionMatches(matches []functionFileMatch, limit int) []map[string]any {
	if limit <= 0 {
		limit = 20
	}
	out := make([]map[string]any, 0, min(len(matches), limit))
	for index, match := range matches {
		if index >= limit {
			break
		}
		entry := map[string]any{"path": match.Path, "category": match.Category, "hitCount": match.HitCount}
		if len(match.Owners) > 0 {
			entry["ownerCount"] = len(match.Owners)
		}
		out = append(out, entry)
	}
	return out
}

func ownerNamesFromCalls(matches []functionFileMatch, seen map[string]bool) []string {
	owners := []string{}
	for _, match := range matches {
		for _, owner := range match.Owners {
			if owner == "" || seen[owner] {
				continue
			}
			owners = appendUnique(owners, owner)
		}
	}
	return owners
}

func pathsForFunctionGraph(impact functionImpactResult) []string {
	seen := map[string]bool{}
	paths := []string{}
	for _, group := range [][]functionFileMatch{impact.Definitions, impact.CallSites} {
		for _, match := range group {
			if match.Category == "docs" || match.Category == "config" || seen[match.Path] {
				continue
			}
			paths = append(paths, match.Path)
			seen[match.Path] = true
		}
	}
	return paths
}

func compactFunctionMatches(matches []functionFileMatch) []map[string]any {
	out := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		entry := map[string]any{"path": match.Path, "category": match.Category, "hitCount": match.HitCount}
		if len(match.KindCounts) > 0 {
			entry["kinds"] = match.KindCounts
		}
		if len(match.Owners) > 0 {
			entry["owners"] = match.Owners
		}
		if len(match.Matches) > 0 {
			entry["matches"] = match.Matches
		}
		out = append(out, entry)
	}
	return out
}

func compactCallsiteContractMatches(matches []callsiteContractMatch, limit int) []map[string]any {
	if limit <= 0 {
		limit = 80
	}
	out := make([]map[string]any, 0, min(len(matches), limit))
	for index, match := range matches {
		if index >= limit {
			break
		}
		entry := map[string]any{
			"path":                  match.Path,
			"category":              match.Category,
			"line":                  match.Line,
			"hasRequiredBeforeCall": match.HasRequiredBeforeCall,
		}
		if match.Owner != "" {
			entry["owner"] = match.Owner
		}
		if match.RequiredLine > 0 {
			entry["requiredLine"] = match.RequiredLine
		}
		if match.Snippet != "" {
			entry["snippet"] = match.Snippet
		}
		out = append(out, entry)
	}
	return out
}

func callsiteContractGuidance(result callsiteContractResult) []string {
	guidance := []string{}
	if result.MissingCallSites > 0 {
		guidance = append(guidance, "Add or verify "+result.RequiredBeforeCall+" before each missing "+result.Callee+" call site in the same owner.")
	}
	if result.UnownedCallSites > 0 {
		guidance = append(guidance, "Review unowned call sites manually because the scanner could not identify an enclosing function.")
	}
	if result.Truncated {
		guidance = append(guidance, "Result was truncated; rerun with a higher limit before editing broadly.")
	}
	return guidance
}

func renameBucket(file literalFileMatch) string {
	switch file.Category {
	case "test":
		return "tests"
	case "docs":
		return "docs"
	case "config":
		return "config"
	}
	if file.Flags != nil {
		if value, ok := file.Flags["runtimeEnvRead"].(bool); ok && value {
			return "runtimeReads"
		}
	}
	switch file.Category {
	case "script":
		return "scripts"
	default:
		return "other"
	}
}

func externalRenameNotes(kind string) []string {
	if kind == "env" {
		return []string{"Update deployed environment variables and CI/CD secret stores for every environment using this repo."}
	}
	return []string{}
}

func gitOutput(repo string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func errorResponse(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func tools() []map[string]any {
	return []map[string]any{
		tool("codegraph_help", "Explain how to use this CodeGraph MCP server, including the current ripple, available tools, argument aliases, and example investigation workflows. Call this first when unsure.", map[string]any{}, []string{}),
		tool("get_ripple_info", "Return metadata and graph counts for the current ripple.", map[string]any{}, []string{}),
		tool("get_index_freshness", "Return current ripple metadata, including repo path and index timestamps. Use before comparing graph answers with a live checkout.", map[string]any{}, []string{}),
		tool("list_node_types", "Return node label counts and relationship type counts for the current ripple.", map[string]any{}, []string{}),
		tool("analyze_rename_impact", "Single-call rename impact analysis. Use this for prompts like 'if X is renamed' or 'what files need changes'. For env vars it classifies runtime reads, config, tests, docs, and compact graph relationships.", map[string]any{
			"oldName":       stringSchema("Old name or exact literal. Aliases: query, q, text, term."),
			"kind":          stringSchema("env, literal, symbol, path, or package. Default literal."),
			"includeGraph":  boolSchema("Include compact graph relation summaries for runtime/script files. Default true."),
			"includeHidden": boolSchema("Include hidden files such as .env. Default true for rename impact."),
			"includeTmp":    boolSchema("Include tmp/ files. Default false."),
			"limit":         intSchema("Maximum files."),
		}, []string{"oldName"}),
		tool("analyze_function_impact", "Single-call feature blast-radius analysis for a function, hook, component, method, or exported symbol name. Use this for prompts like 'where is X used', 'what breaks if X behavior changes', or 'where must we add a precondition before calling X'. Returns definitions, imports, call sites, tests, and compact graph relationship summaries.", map[string]any{
			"symbol":               stringSchema("Function, hook, component, method, or symbol name. Aliases: name, query, q, text, term."),
			"includeTests":         boolSchema("Include test files. Default true."),
			"includeScripts":       boolSchema("Include files under scripts/. Default true."),
			"includeGraph":         boolSchema("Include compact graph relation summaries. Default true."),
			"includeTmp":           boolSchema("Include tmp/ files. Default false."),
			"transitiveDepth":      intSchema("Owner-call expansion depth. Default 1."),
			"maxTransitiveSymbols": intSchema("Maximum owner symbols to expand. Default 8."),
			"limit":                intSchema("Maximum files."),
		}, []string{"symbol"}),
		tool("analyze_callsite_contract", "Single-call call-site contract analysis. Use when a feature requires every existing call to callee X to be preceded by required check Y, for example 'before calling performAction call assertActionAllowed'. Returns missing and satisfied call sites by file, owner, and line. Leave snippets=false for planning; request snippets only when exact source text is required.", map[string]any{
			"callee":             stringSchema("Function, hook, component, or method that is being called. Aliases: function, symbol, query, q."),
			"requiredBeforeCall": stringSchema("Function or method that must be called earlier in the same enclosing function. Aliases: required, precheck, check."),
			"includeTests":       boolSchema("Include test files. Default true."),
			"includeScripts":     boolSchema("Include files under scripts/. Default true."),
			"includeTmp":         boolSchema("Include tmp/ files. Default false."),
			"snippets":           boolSchema("Include matching source snippets. Default false. Keep false for planning to reduce tokens."),
			"limit":              intSchema("Maximum call sites scanned. Default 200."),
			"resultLimit":        intSchema("Maximum call sites returned per list. Default 80."),
		}, []string{"callee", "requiredBeforeCall"}),
		tool("prepare_feature_context", "One-call starter context pack for feature work. Use exactly once before editing when the prompt asks for entry points, related files, tests, dependencies, index freshness, or blast radius. Treat contextCompleteForPlanning=true as enough for planning; only read files afterward when implementing or when exact source lines are required.", map[string]any{
			"query":                stringSchema("Feature term, symbol name, file path, or exact source text. Aliases: q, feature, symbol, name, path."),
			"symbol":               stringSchema("Optional function/hook/component/method name when known."),
			"limit":                intSchema("Maximum source and graph matches. Default 12."),
			"impactLimit":          intSchema("Maximum files scanned for symbol impact. Default 80."),
			"relationExamples":     intSchema("Maximum dependency examples per file."),
			"transitiveDepth":      intSchema("Owner-call expansion depth. Default 1."),
			"maxTransitiveSymbols": intSchema("Maximum owner symbols to expand. Default 4."),
		}, []string{"query"}),
		tool("count_literal_files", "Single-call exact string count. Use this for 'how many files contain X' prompts. Returns only paths plus category counts. Defaults match normal source search: excludes hidden files and tmp/ unless explicitly included.", map[string]any{
			"query":          stringSchema("Exact text to find. Aliases: q, text, term."),
			"includeTests":   boolSchema("Include test files. Default true."),
			"includeDocs":    boolSchema("Include markdown docs. Default true."),
			"includeConfig":  boolSchema("Include config files. Default true."),
			"includeScripts": boolSchema("Include files under scripts/. Default true."),
			"includeHidden":  boolSchema("Include hidden files such as .env. Default false."),
			"includeTmp":     boolSchema("Include tmp/ files. Default false."),
			"limit":          intSchema("Maximum files."),
		}, []string{"query"}),
		tool("find_env_usages", "Single-call runtime env var usage search. Use this for 'which files read process.env.NAME'. Returns files and read counts. Defaults exclude tests, docs, config, hidden files, and tmp files.", map[string]any{
			"envName":        stringSchema("Environment variable name. Aliases: name, query, q, text, term."),
			"includeTests":   boolSchema("Include test files. Default false."),
			"includeScripts": boolSchema("Include files under scripts/. Default true."),
			"includeHidden":  boolSchema("Include hidden files such as .env. Default false."),
			"includeTmp":     boolSchema("Include tmp/ files. Default false."),
			"limit":          intSchema("Maximum files."),
		}, []string{"envName"}),
		tool("search_code", "Search all indexed code graph nodes in the current ripple. Use this first for broad terms like billing, checkout, route, package names, or paths.", map[string]any{
			"query": stringSchema("Search text. Aliases: q, text, term, name, path."),
			"limit": intSchema("Maximum results."),
		}, []string{"query"}),
		tool("find_symbol", "Find symbols by name. Accepts name or query.", map[string]any{
			"name":  stringSchema("Symbol name or partial name. Alias: query."),
			"limit": intSchema("Maximum results."),
		}, []string{}),
		tool("find_file", "Find files by path. Accepts path or query.", map[string]any{
			"path":  stringSchema("File path or partial path. Alias: query."),
			"limit": intSchema("Maximum results."),
		}, []string{}),
		tool("get_dependencies", "Get outgoing dependencies for a node id. Accepts targetId, sourceId, id, or query.", nodeIDSchema(), []string{}),
		tool("get_dependents", "Get incoming dependents for a node id. Accepts targetId, sourceId, id, or query.", nodeIDSchema(), []string{}),
		tool("get_relations", "Get graph relations for a node id in forward, reverse, or both directions. Direction aliases: outbound, incoming, inbound.", mapMerge(nodeIDSchema(), map[string]any{
			"direction": stringSchema("forward, reverse, both, outbound, incoming, inbound."),
			"depth":     intSchema("Traversal depth."),
			"limit":     intSchema("Maximum relationship paths."),
		}), []string{}),
		tool("get_impact", "Get reverse dependency impact for a node id.", nodeIDSchema(), []string{}),
		tool("get_route_impact", "Get reverse route impact for a route or file node id.", nodeIDSchema(), []string{}),
		tool("get_related_tests", "Get tests related to a node id using reverse graph relations.", nodeIDSchema(), []string{}),
		tool("find_paths", "Find a shortest graph path between two node ids.", map[string]any{
			"fromId": stringSchema("Start node id. Aliases: from, sourceId, startId, source."),
			"toId":   stringSchema("End node id. Aliases: to, targetId, endId, target."),
			"depth":  intSchema("Maximum path depth."),
		}, []string{}),
		tool("open_symbol_body", "Open source text for a symbol id.", map[string]any{
			"symbolId": stringSchema("Symbol id. Aliases: id, targetId, sourceId."),
		}, []string{}),
		tool("open_file_excerpt", "Open a source file excerpt by path.", map[string]any{
			"path":      stringSchema("File path. Aliases: file, filePath."),
			"startLine": intSchema("Start line."),
			"endLine":   intSchema("End line."),
		}, []string{}),
	}
}

func tool(name string, description string, properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": true,
		},
	}
}

func nodeIDSchema() map[string]any {
	return map[string]any{
		"targetId": stringSchema("Node id. Aliases: sourceId, id, nodeId, query."),
		"depth":    intSchema("Traversal depth."),
		"limit":    intSchema("Maximum relationship paths."),
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func intSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func boolSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func mapMerge(left map[string]any, right map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range left {
		out[key] = value
	}
	for key, value := range right {
		out[key] = value
	}
	return out
}

func stringArg(args map[string]any, key string) string {
	if value, ok := args[key].(string); ok {
		return value
	}
	return ""
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringArg(args, key); value != "" {
			return value
		}
	}
	return ""
}

func nodeIDArg(args map[string]any) string {
	return firstStringArg(args, "targetId", "sourceId", "id", "nodeId", "query", "q")
}

func directionArg(args map[string]any, fallback string) string {
	switch strings.ToLower(firstStringArg(args, "direction", "dir")) {
	case "outbound", "out", "dependencies":
		return "forward"
	case "inbound", "incoming", "in", "dependents":
		return "reverse"
	case "forward", "reverse", "both":
		return strings.ToLower(firstStringArg(args, "direction", "dir"))
	default:
		return fallback
	}
}

func stringArgDefault(args map[string]any, key string, fallback string) string {
	if value := stringArg(args, key); value != "" {
		return value
	}
	return fallback
}

func intArg(args map[string]any, key string, fallback int) int {
	if value, ok := args[key].(float64); ok {
		return int(value)
	}
	return fallback
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	if value, ok := args[key].(bool); ok {
		return value
	}
	return fallback
}

func floatArg(args map[string]any, key string, fallback float64) float64 {
	if value, ok := args[key].(float64); ok {
		return value
	}
	return fallback
}

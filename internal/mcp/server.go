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
	"time"

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
	// Agent-safe defaults: small result budgets. Raise limit/depth/detail only when needed.
	opts := graph.Options{Depth: intArg(args, "depth", 1), Limit: intArg(args, "limit", 20), MinConfidence: floatArg(args, "minConfidence", 0.6)}
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
		// Hidden alias: prefer count_literal_files, or pass detail=lines.
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
		// Hidden aliases of reverse get_relations (kept for older clients).
		opts.Direction = "reverse"
		result, err = s.Query.Relations(ctx, nodeIDArg(args), opts)
	case "find_paths":
		result, err = s.Query.Paths(ctx, firstStringArg(args, "fromId", "from", "sourceId", "startId", "source"), firstStringArg(args, "toId", "to", "targetId", "endId", "target"), opts)
	case "open_file_excerpt":
		result, err = s.openFile(firstStringArg(args, "path", "file", "filePath"), intArg(args, "startLine", 1), intArg(args, "endLine", 40))
	case "open_symbol_body":
		symbolID := firstStringArg(args, "symbolId", "id", "targetId", "sourceId")
		if s.Query.Ripple != "" {
			symbolID = strings.TrimPrefix(symbolID, s.Query.Ripple+":")
		}
		result, err = s.openSymbolBody(ctx, symbolID, intArg(args, "contextLines", 2))
	default:
		err = fmt.Errorf("unknown tool %s", params.Name)
	}
	if err != nil {
		return nil, err
	}
	text, err := formatToolResult(params.Name, result, args)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
}

func (s Server) help() map[string]any {
	return map[string]any{
		"ripple":  s.Query.Ripple,
		"repo":    s.Repo,
		"purpose": "Token-efficient code investigation for one indexed ripple. Prefer one high-level tool, then open source only for exact lines.",
		"router": []string{
			"feature start / planning -> prepare_feature_context",
			"callers / blast radius -> analyze_function_impact",
			"rename / migration -> analyze_rename_impact",
			"env runtime reads -> find_env_usages",
			"exact text count -> count_literal_files",
			"guard before every call -> analyze_callsite_contract",
			"broad explore -> search_code / find_symbol / find_file",
			"deps for a known node id -> get_relations (depth=1, limit=20)",
			"source text -> open_file_excerpt / open_symbol_body after paths are known",
			"index age / mode -> get_index_freshness",
		},
		"tokenRules": []string{
			"Defaults return compact summary text. Raise detail only when needed: detail=summary|files|lines, or raw=true for JSON.",
			"Do not open files for planning. Use suggestedFollowUpReads or callSites paths only when implementing.",
			"If truncated=true, raise limit or detail only if that changes the answer.",
			"Fast-mode indexes may omit symbol-level relations; filesystem impact tools remain authoritative for text/call sites.",
		},
		"examples": []map[string]any{
			{"tool": "prepare_feature_context", "arguments": map[string]any{"query": "resolveTenantAccount"}},
			{"tool": "analyze_function_impact", "arguments": map[string]any{"symbol": "resolveTenantAccount"}},
			{"tool": "analyze_rename_impact", "arguments": map[string]any{"oldName": "SERVICE_LOGIN_EMAIL", "kind": "env"}},
			{"tool": "count_literal_files", "arguments": map[string]any{"query": "SERVICE_LOGIN_EMAIL"}},
			{"tool": "get_relations", "arguments": map[string]any{"sourceId": "file:src/main.ts", "direction": "inbound"}},
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
	// Hard cap excerpt size so agents cannot dump huge files by accident.
	const maxExcerptLines = 120
	if end-start+1 > maxExcerptLines {
		end = start + maxExcerptLines - 1
	}
	if start > len(lines) {
		return map[string]any{"path": path, "startLine": start, "endLine": start, "text": "", "truncated": true}, nil
	}
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{
		"path":      path,
		"startLine": start,
		"endLine":   end,
		"text":      strings.Join(lines[start-1:end], "\n"),
		"truncated": end < len(lines) && end-start+1 >= maxExcerptLines,
	}, nil
}

func (s Server) openSymbolBody(ctx context.Context, symbolID string, contextLines int) (map[string]any, error) {
	if symbolID == "" {
		return nil, fmt.Errorf("symbolId is required")
	}
	node, err := s.Query.Node(ctx, symbolID)
	if err != nil {
		return nil, err
	}
	path := firstAnyString(node, "filePath", "path")
	if path == "" {
		file, _, ok := strings.Cut(strings.TrimPrefix(symbolID, "symbol:"), "#")
		if !ok {
			return nil, fmt.Errorf("symbolId must be symbol:<path>#<name>")
		}
		path = file
	}
	start := intValue(node["startLine"])
	end := intValue(node["endLine"])
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = start
	}
	if contextLines < 0 {
		contextLines = 0
	}
	body, err := s.openFile(path, start-contextLines, end+contextLines)
	if err != nil {
		return nil, err
	}
	body["symbolId"] = firstAnyString(node, "sourceId", "id")
	body["symbolName"] = firstAnyString(node, "name")
	body["symbolKind"] = firstAnyString(node, "kind")
	return body, nil
}

func (s Server) searchLiteral(query string, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	includeLines := detail == "lines" || boolArg(args, "lines", false)
	includeSnippets := detail == "lines" || boolArg(args, "snippets", false)
	result, err := searchLiteralFiles(s.Repo, query, literalSearchOptions{
		IncludeTests:    boolArg(args, "includeTests", true),
		IncludeDocs:     boolArg(args, "includeDocs", true),
		IncludeConfig:   boolArg(args, "includeConfig", true),
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    includeLines,
		IncludeSnippets: includeSnippets,
		MatchesPerFile:  intArg(args, "matchesPerFile", 3),
		Limit:           intArg(args, "limit", 40),
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
		"detail":       detail,
	}, nil
}

func (s Server) countLiteralFiles(query string, args map[string]any) (map[string]any, error) {
	limit := intArg(args, "limit", 40)
	result, err := searchLiteralFiles(s.Repo, query, literalSearchOptions{
		IncludeTests:    boolArg(args, "includeTests", true),
		IncludeDocs:     boolArg(args, "includeDocs", true),
		IncludeConfig:   boolArg(args, "includeConfig", true),
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    false,
		IncludeSnippets: false,
		Limit:           limit,
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		paths = append(paths, file.Path)
	}
	maxItems := maxItemsArg(args, defaultMaxItems)
	paths, pathTruncated := limitSlice(paths, maxItems)
	return map[string]any{
		"query":        result.Query,
		"uniqueFiles":  result.UniqueFiles,
		"totalMatches": result.TotalMatches,
		"counts":       result.Counts,
		"files":        paths,
		"truncated":    result.Truncated || pathTruncated,
		"detail":       detailArg(args),
	}, nil
}

func (s Server) findEnvUsages(envName string, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	includeLines := detail == "lines" || boolArg(args, "lines", false)
	includeSnippets := detail == "lines" || boolArg(args, "snippets", false)
	search, err := searchLiteralFiles(s.Repo, envName, literalSearchOptions{
		IncludeTests:    boolArg(args, "includeTests", false),
		IncludeDocs:     false,
		IncludeConfig:   false,
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    includeLines,
		IncludeSnippets: includeSnippets,
		MatchesPerFile:  intArg(args, "matchesPerFile", 5),
		Limit:           intArg(args, "limit", 40),
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
		if includeLines {
			matches := filterMatchesByKind(file.Matches, "runtime_env_read")
			if len(matches) > 0 {
				entry["matches"] = matches
			}
		}
		files = append(files, entry)
		totalReads += readCount
	}
	uniqueFiles := len(files)
	maxItems := maxItemsArg(args, defaultMaxItems)
	files, truncated := limitSlice(files, maxItems)
	return map[string]any{
		"envName":          envName,
		"uniqueFiles":      uniqueFiles,
		"runtimeReadCount": totalReads,
		"files":            files,
		"truncated":        search.Truncated || truncated,
		"detail":           detail,
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
	detail := detailArg(args)
	includeLines := detail == "lines" || boolArg(args, "lines", false)
	includeSnippets := detail == "lines" || boolArg(args, "snippets", false)
	perBucket := maxItemsArg(args, defaultMaxItems)
	if detail == "summary" {
		perBucket = min(perBucket, 10)
	}
	search, err := searchLiteralFiles(s.Repo, oldName, literalSearchOptions{
		IncludeTests:    true,
		IncludeDocs:     true,
		IncludeConfig:   true,
		IncludeScripts:  true,
		IncludeHidden:   boolArg(args, "includeHidden", true),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeLines:    includeLines,
		IncludeSnippets: includeSnippets,
		MatchesPerFile:  intArg(args, "matchesPerFile", 4),
		Limit:           intArg(args, "limit", 40),
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
	bucketTotals := map[string]int{}
	relationPaths := []string{}
	truncated := search.Truncated
	for _, file := range search.Files {
		entry := map[string]any{"path": file.Path, "matchCount": file.MatchCount}
		if includeLines && len(file.Matches) > 0 {
			entry["matches"] = file.Matches
		}
		bucket := renameBucket(file)
		bucketTotals[bucket]++
		if len(filesByBucket[bucket]) < perBucket {
			filesByBucket[bucket] = append(filesByBucket[bucket], entry)
		} else {
			truncated = true
		}
		if bucket == "runtimeReads" || bucket == "scripts" {
			relationPaths = append(relationPaths, file.Path)
		}
	}
	// Convert to map[string]any so the compact formatter can nest list sections
	// (typed map[string][]map[string]any falls through to Go %v dumps).
	filesToChange := map[string]any{}
	for bucket, files := range filesByBucket {
		filesToChange[bucket] = files
	}
	response := map[string]any{
		"oldName":       oldName,
		"kind":          kind,
		"uniqueFiles":   search.UniqueFiles,
		"totalMatches":  search.TotalMatches,
		"counts":        search.Counts,
		"bucketTotals":  bucketTotals,
		"filesToChange": filesToChange,
		"external":      externalRenameNotes(kind),
		"truncated":     truncated,
		"detail":        detail,
	}
	if boolArg(args, "includeGraph", false) && len(relationPaths) > 0 {
		relationSummary, err := s.Query.FileRelationSummary(ctx, relationPaths, intArg(args, "relationExamples", 3))
		if err != nil {
			return nil, err
		}
		response["graphSummary"] = relationSummary
	}
	return response, nil
}

func (s Server) analyzeFunctionImpact(ctx context.Context, symbol string, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	includeTests := boolArg(args, "includeTests", true)
	includeScripts := boolArg(args, "includeScripts", true)
	includeTmp := boolArg(args, "includeTmp", false)
	includeLines := detail == "lines" || boolArg(args, "lines", false)
	includeSnippets := detail == "lines" || boolArg(args, "snippets", false)
	listLimit := maxItemsArg(args, defaultMaxItems)
	if detail == "summary" {
		listLimit = min(listLimit, 15)
	}
	impact, err := analyzeFunctionImpact(s.Repo, symbol, functionImpactOptions{
		IncludeTests:    includeTests,
		IncludeDocs:     boolArg(args, "includeDocs", false),
		IncludeConfig:   boolArg(args, "includeConfig", false),
		IncludeScripts:  includeScripts,
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      includeTmp,
		IncludeLines:    includeLines,
		IncludeSnippets: includeSnippets,
		MatchesPerFile:  intArg(args, "matchesPerFile", 3),
		Limit:           intArg(args, "limit", 40),
	})
	if err != nil {
		return nil, err
	}
	defs, defTrunc := limitSlice(compactFunctionMatches(impact.Definitions), listLimit)
	imports, importTrunc := limitSlice(compactFunctionMatches(impact.Imports), listLimit)
	calls, callTrunc := limitSlice(compactFunctionMatches(impact.CallSites), listLimit)
	refs, refTrunc := limitSlice(compactFunctionMatches(impact.References), min(listLimit, 10))
	truncated := impact.Truncated || defTrunc || importTrunc || callTrunc || refTrunc

	response := map[string]any{
		"symbol":      symbol,
		"uniqueFiles": impact.UniqueFiles,
		"totalHits":   impact.TotalHits,
		"counts":      impact.Counts,
		"definitions": defs,
		"callSites":   calls,
		"truncated":   truncated,
		"detail":      detail,
	}
	if detail == "summary" {
		response["importFiles"] = len(impact.Imports)
		response["referenceFiles"] = len(impact.References)
		if len(imports) > 0 {
			response["imports"] = imports
		}
	} else {
		response["imports"] = imports
		response["references"] = refs
	}

	// Transitive expansion is expensive; off by default.
	transitiveDepth := intArg(args, "transitiveDepth", 0)
	if transitiveDepth > 0 {
		response["transitive"] = s.transitiveFunctionImpact(symbol, impact, transitiveDepth, intArg(args, "maxTransitiveSymbols", 4), includeTests, includeScripts, includeTmp)
	}
	if boolArg(args, "includeGraph", false) {
		graphPaths := pathsForFunctionGraph(impact)
		if len(graphPaths) > 0 {
			graphSummary, err := s.Query.FileRelationSummary(ctx, graphPaths, intArg(args, "relationExamples", 3))
			if err != nil {
				return nil, err
			}
			response["graphSummary"] = graphSummary
		}
	}
	return response, nil
}

func (s Server) analyzeCallsiteContract(callee string, requiredBeforeCall string, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	resultLimit := intArg(args, "resultLimit", maxItemsArg(args, defaultMaxItems))
	if detail == "summary" {
		resultLimit = min(resultLimit, 15)
	}
	includeSnippets := detail == "lines" || boolArg(args, "snippets", false)
	result, err := analyzeCallsiteContract(s.Repo, callee, requiredBeforeCall, callsiteContractOptions{
		IncludeTests:    boolArg(args, "includeTests", true),
		IncludeScripts:  boolArg(args, "includeScripts", true),
		IncludeHidden:   boolArg(args, "includeHidden", false),
		IncludeTmp:      boolArg(args, "includeTmp", false),
		IncludeSnippets: includeSnippets,
		Limit:           intArg(args, "limit", 80),
	})
	if err != nil {
		return nil, err
	}
	files, filesTrunc := limitSlice(result.Files, maxItemsArg(args, defaultMaxItems))
	response := map[string]any{
		"callee":                 result.Callee,
		"requiredBeforeCall":     result.RequiredBeforeCall,
		"uniqueFiles":            result.UniqueFiles,
		"totalCallSites":         result.TotalCallSites,
		"missingCallSites":       result.MissingCallSites,
		"satisfiedCallSites":     result.SatisfiedCallSites,
		"unownedCallSites":       result.UnownedCallSites,
		"files":                  files,
		"missing":                compactCallsiteContractMatches(result.Missing, resultLimit),
		"implementationGuidance": callsiteContractGuidance(result),
		"truncated":              result.Truncated || filesTrunc || len(result.Missing) > resultLimit,
		"detail":                 detail,
	}
	// Satisfied sites are usually less actionable; omit in summary unless requested.
	if detail != "summary" || boolArg(args, "includeSatisfied", false) {
		response["satisfied"] = compactCallsiteContractMatches(result.Satisfied, resultLimit)
	}
	if boolArg(args, "includeAllCallSites", false) || detail == "lines" {
		response["callSites"] = compactCallsiteContractMatches(result.CallSites, resultLimit)
	}
	return response, nil
}

func (s Server) prepareFeatureContext(ctx context.Context, query string, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	limit := intArg(args, "limit", 12)
	if detail == "summary" {
		limit = min(limit, 12)
	}
	graphMatches := map[string]any{"nodes": []map[string]any{}}
	var err error
	if query != "" {
		// Graph search is optional enrichment; do not fail the whole pack if Neo4j is unavailable.
		if s.Query.Driver != nil {
			graphMatches, err = s.Query.Search(ctx, query, graph.Options{Limit: limit})
			if err != nil {
				graphMatches = map[string]any{"nodes": []map[string]any{}, "graphError": err.Error()}
			}
		}
	}
	sourceMatches, err := searchLiteralFiles(s.Repo, query, literalSearchOptions{
		IncludeTests:     true,
		IncludeDocs:      true,
		IncludeConfig:    true,
		IncludeScripts:   true,
		IncludeHidden:    false,
		IncludeTmp:       false,
		IncludeLines:     false,
		IncludeSnippets:  false,
		Limit:            limit,
		CandidateTimeout: 2 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	index := map[string]any{"repo": s.Repo, "ripple": s.Query.Ripple}
	if s.Query.Driver != nil {
		if freshness, freshErr := s.indexFreshness(ctx); freshErr == nil {
			index = slimIndex(freshness)
		} else {
			index["graphError"] = freshErr.Error()
		}
	}
	symbol := firstStringArg(args, "symbol", "name")
	if symbol == "" && isIdentifier(query) {
		symbol = query
	}
	entryPoints := entryPointsFromGraph(graphMatches)
	directChangeFiles := []map[string]any{}
	tests := relatedTests(sourceMatches.Files)
	var impact functionImpactResult
	var transitive []map[string]any
	if symbol != "" {
		impact, err = analyzeFunctionImpact(s.Repo, symbol, functionImpactOptions{
			IncludeTests:   true,
			IncludeScripts: true,
			Limit:          intArg(args, "impactLimit", 40),
		})
		if err != nil {
			return nil, err
		}
		// Transitive off by default for token/cost control.
		transitiveDepth := intArg(args, "transitiveDepth", 0)
		if transitiveDepth > 0 {
			transitive = s.transitiveFunctionImpact(symbol, impact, min(transitiveDepth, 1), min(intArg(args, "maxTransitiveSymbols", 4), 4), true, true, false)
		}
		entryPoints = appendEntryPoints(entryPoints, impact.Definitions)
		directChangeFiles = directChangeFilesFromImpact(impact)
		tests = mergeTestFiles(tests, testFilesFromImpact(impact))
	}

	entryPoints, _ = limitSlice(entryPoints, 6)
	directChangeFiles, _ = limitSlice(directChangeFiles, 10)
	tests, _ = limitSlice(tests, 8)
	reads := suggestedFollowUpReads(entryPoints, directChangeFiles, tests)

	response := map[string]any{
		"query":                      query,
		"contextCompleteForPlanning": true,
		"detail":                     detail,
		"index":                      index,
		"entryPoints":                entryPoints,
		"directChangeFiles":          directChangeFiles,
		"relatedTests":               tests,
		"suggestedFollowUpReads":     reads,
		"next":                       "Plan from this pack. Open only suggestedFollowUpReads when implementing. Use analyze_function_impact detail=files for fuller blast radius.",
	}
	if symbol != "" {
		response["impactSummary"] = map[string]any{
			"symbol":              impact.Symbol,
			"uniqueFiles":         impact.UniqueFiles,
			"totalHits":           impact.TotalHits,
			"directCallSiteFiles": len(impact.CallSites),
			"importFiles":         len(impact.Imports),
			"referenceOnlyFiles":  len(impact.References),
			"truncated":           impact.Truncated,
		}
		if detail != "summary" {
			response["blastRadius"] = map[string]any{
				"directCallSites": summarizeFunctionMatches(impact.CallSites, 12),
				"references":      summarizeFunctionMatches(impact.References, 8),
				"indirectOwners":  compactTransitiveOwners(transitive),
			}
		} else {
			response["blastRadius"] = map[string]any{
				"directCallSiteFiles": len(impact.CallSites),
				"referenceFiles":      len(impact.References),
				"topCallSites":        summarizeFunctionMatches(impact.CallSites, 8),
			}
		}
	}
	if detail != "summary" {
		paths := pathsFromFeatureContext(graphMatches, sourceMatches)
		if s.Query.Driver != nil && len(paths) > 0 {
			if graphSummary, gerr := s.Query.FileRelationSummary(ctx, paths, intArg(args, "relationExamples", 3)); gerr == nil {
				response["dependencySummary"] = graphSummary
			}
		}
		response["sourceMatches"] = compactLiteralSearch(sourceMatches)
		response["graphMatches"] = compactGraphNodes(graphMatches)
	} else {
		response["sourceMatchFiles"] = sourceMatches.UniqueFiles
		response["graphMatchNodes"] = len(compactGraphNodes(graphMatches))
	}
	return response, nil
}

func slimIndex(index map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"ripple", "repo", "language", "analysisMode", "nodes", "relationships", "localHead", "localBranch", "updatedAt"} {
		if value, ok := index[key]; ok && value != nil {
			out[key] = value
		}
	}
	return out
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
	detailSchema := stringSchema("Response detail: summary (default, cheapest), files, lines, or raw. Prefer summary for planning.")
	return []map[string]any{
		tool("codegraph_help", "Short router for CodeGraph tools. Use only when tool choice is unclear; prefer calling a task tool directly.", map[string]any{}, []string{}),
		tool("get_ripple_info", "Return metadata and graph counts for the current ripple.", map[string]any{}, []string{}),
		tool("get_index_freshness", "Return ripple metadata, analysisMode, and local git HEAD/branch when available.", map[string]any{}, []string{}),
		tool("list_node_types", "Return node label counts and relationship type counts for the current ripple.", map[string]any{}, []string{}),
		tool("prepare_feature_context", "One-call planning pack: entry points, likely edit files, tests, and compact blast radius. Default detail=summary is enough for planning; open files only when implementing.", map[string]any{
			"query":   stringSchema("Feature term, symbol, path, or exact text. Aliases: q, feature, symbol, name, path."),
			"symbol":  stringSchema("Optional function/hook/component name when known."),
			"detail":  detailSchema,
			"limit":   intSchema("Max source/graph matches. Default 12."),
			"maxItems": intSchema("Max list items returned. Default 20."),
		}, []string{"query"}),
		tool("analyze_function_impact", "Blast radius for a function, hook, component, method, or exported symbol: definitions, call sites, imports. Default detail=summary. Set transitiveDepth>0 only for owner expansion.", map[string]any{
			"symbol":          stringSchema("Symbol name. Aliases: name, query, q."),
			"detail":          detailSchema,
			"includeTests":    boolSchema("Include tests. Default true."),
			"includeScripts":  boolSchema("Include scripts/. Default true."),
			"includeGraph":    boolSchema("Include Neo4j file relation summary. Default false."),
			"transitiveDepth": intSchema("Owner expansion depth. Default 0 (off)."),
			"limit":           intSchema("Max files scanned. Default 40."),
			"maxItems":        intSchema("Max list items returned. Default 20."),
		}, []string{"symbol"}),
		tool("analyze_rename_impact", "Rename/migration impact for an exact name (env, literal, path, package). Groups runtime/config/tests/docs/scripts.", map[string]any{
			"oldName":      stringSchema("Old name or exact literal. Aliases: query, q."),
			"kind":         stringSchema("env, literal, symbol, path, or package. Default literal."),
			"detail":       detailSchema,
			"includeGraph": boolSchema("Include Neo4j relation summary. Default false."),
			"limit":        intSchema("Max files scanned. Default 40."),
			"maxItems":     intSchema("Max items per bucket. Default 20."),
		}, []string{"oldName"}),
		tool("analyze_callsite_contract", "Find call sites of callee missing a required pre-call check in the same owner function.", map[string]any{
			"callee":             stringSchema("Callee name. Aliases: function, symbol, query."),
			"requiredBeforeCall": stringSchema("Required precheck name. Aliases: required, precheck, check."),
			"detail":             detailSchema,
			"includeTests":       boolSchema("Include tests. Default true."),
			"limit":              intSchema("Max call sites scanned. Default 80."),
			"resultLimit":        intSchema("Max call sites returned. Default 20."),
		}, []string{"callee", "requiredBeforeCall"}),
		tool("count_literal_files", "Count unique files containing exact text. Returns category counts and paths.", map[string]any{
			"query":  stringSchema("Exact text. Aliases: q, text, term."),
			"detail": detailSchema,
			"limit":  intSchema("Max files. Default 40."),
		}, []string{"query"}),
		tool("find_env_usages", "Find runtime process.env.NAME reads. Defaults exclude tests/docs/config.", map[string]any{
			"envName": stringSchema("Env var name. Aliases: name, query, q."),
			"detail":  detailSchema,
			"limit":   intSchema("Max files. Default 40."),
		}, []string{"envName"}),
		tool("search_code", "Search indexed graph nodes (files, symbols, packages, routes). Prefer high-level tools when the task matches them.", map[string]any{
			"query": stringSchema("Search text. Aliases: q, text, term, name, path."),
			"limit": intSchema("Max results. Default 20."),
		}, []string{"query"}),
		tool("find_symbol", "Find symbols by name.", map[string]any{
			"name":  stringSchema("Symbol name. Alias: query."),
			"limit": intSchema("Max results. Default 20."),
		}, []string{}),
		tool("find_file", "Find files by path.", map[string]any{
			"path":  stringSchema("File path. Alias: query."),
			"limit": intSchema("Max results. Default 20."),
		}, []string{}),
		tool("get_dependencies", "Outgoing dependencies for a node id (depth default 1, limit default 20).", nodeIDSchema(), []string{}),
		tool("get_dependents", "Incoming dependents for a node id (depth default 1, limit default 20).", nodeIDSchema(), []string{}),
		tool("get_relations", "Graph relations for a node id. Prefer depth=1 and limit<=20.", mapMerge(nodeIDSchema(), map[string]any{
			"direction": stringSchema("forward, reverse, both, outbound, inbound."),
			"depth":     intSchema("Traversal depth. Default 1."),
			"limit":     intSchema("Max paths. Default 20."),
		}), []string{}),
		tool("find_paths", "Shortest graph path between two node ids.", map[string]any{
			"fromId": stringSchema("Start node id. Aliases: from, sourceId."),
			"toId":   stringSchema("End node id. Aliases: to, targetId."),
			"depth":  intSchema("Max path depth. Default 1."),
		}, []string{}),
		tool("open_symbol_body", "Open source for a symbol id. Use only after locating the symbol.", map[string]any{
			"symbolId":     stringSchema("Symbol id. Aliases: id, targetId, sourceId."),
			"contextLines": intSchema("Extra lines around the symbol. Default 2."),
		}, []string{}),
		tool("open_file_excerpt", "Open a source excerpt by path. Default endLine is 40; keep ranges tight.", map[string]any{
			"path":      stringSchema("File path. Aliases: file, filePath."),
			"startLine": intSchema("Start line. Default 1."),
			"endLine":   intSchema("End line. Default 40."),
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
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch value.(type) {
	case int, int32, int64, float32, float64, json.Number:
		return intValue(value)
	default:
		return fallback
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, err := typed.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	default:
		return 0
	}
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

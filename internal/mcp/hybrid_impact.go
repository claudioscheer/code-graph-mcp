package mcp

import (
	"context"
	"slices"
	"strings"

	"github.com/claudioscheer/code-graph-mcp/internal/graph"
)

// mergeHybridImpact combines filesystem text impact with optional graph CALLS/IMPORTS.
// Graph paths are preferred for definitions when present; call sites and imports are
// unioned by path with source tags (graph|text|both).
func mergeHybridImpact(text functionImpactResult, graphImpact map[string]any) (functionImpactResult, map[string]any) {
	meta := map[string]any{
		"resolutionMethod": "text",
		"graphCallSites":   0,
		"textOnlyFiles":    0,
		"graphOnlyFiles":   0,
		"hasCallGraph":     false,
	}
	if graphImpact == nil {
		meta["textOnlyFiles"] = text.UniqueFiles
		return text, meta
	}

	if v, ok := graphImpact["hasCallGraph"].(bool); ok {
		meta["hasCallGraph"] = v
	}

	graphDefs := mapSlice(graphImpact["definitions"])
	graphCalls := mapSlice(graphImpact["callSites"])
	graphImports := mapSlice(graphImpact["imports"])
	meta["graphCallSites"] = len(graphCalls)
	meta["graphImportFiles"] = len(graphImports)

	if len(graphDefs) == 0 && len(graphCalls) == 0 && len(graphImports) == 0 {
		meta["resolutionMethod"] = "text"
		meta["textOnlyFiles"] = text.UniqueFiles
		return text, meta
	}

	// Definitions: prefer graph symbols when available.
	if len(graphDefs) > 0 {
		defs := []functionFileMatch{}
		seen := map[string]bool{}
		for _, def := range graphDefs {
			path := firstAnyString(def, "path")
			if path == "" || seen[path] {
				continue
			}
			match := functionFileMatch{
				Path: path, Category: classifyPath(path), HitCount: 1,
				KindCounts: map[string]int{"definition": 1},
			}
			if name := firstAnyString(def, "name"); name != "" {
				match.Owners = []string{name}
			}
			defs = append(defs, match)
			seen[path] = true
		}
		// Keep text definitions for paths graph missed.
		for _, def := range text.Definitions {
			if !seen[def.Path] {
				defs = append(defs, def)
			}
		}
		text.Definitions = defs
	}

	// Call sites: union by path.
	callByPath := map[string]functionFileMatch{}
	for _, call := range text.CallSites {
		callByPath[call.Path] = call
	}
	graphOnly := 0
	for _, call := range graphCalls {
		path := firstAnyString(call, "path")
		if path == "" {
			continue
		}
		existing, ok := callByPath[path]
		if !ok {
			existing = functionFileMatch{
				Path: path, Category: classifyPath(path), KindCounts: map[string]int{},
			}
			graphOnly++
		}
		existing.HitCount++
		if existing.KindCounts == nil {
			existing.KindCounts = map[string]int{}
		}
		kind := firstAnyString(call, "kind")
		if kind == "" {
			kind = "call"
		}
		existing.KindCounts[strings.ToLower(kind)]++
		if owner := firstAnyString(call, "owner"); owner != "" {
			existing.Owners = appendUnique(existing.Owners, owner)
		}
		if line, ok := call["line"]; ok && (len(existing.Matches) < 5) {
			lm := literalLineMatch{Kind: "graph_" + strings.ToLower(kind)}
			if n := intValue(line); n > 0 {
				lm.Line = n
			}
			if owner := firstAnyString(call, "owner"); owner != "" {
				lm.Owner = owner
			}
			existing.Matches = append(existing.Matches, lm)
		}
		callByPath[path] = existing
	}
	text.CallSites = make([]functionFileMatch, 0, len(callByPath))
	for _, match := range callByPath {
		text.CallSites = append(text.CallSites, match)
	}
	sortFunctionMatches(text.CallSites)

	// Imports: union by path.
	importByPath := map[string]functionFileMatch{}
	for _, imp := range text.Imports {
		importByPath[imp.Path] = imp
	}
	for _, imp := range graphImports {
		path := firstAnyString(imp, "path")
		if path == "" {
			continue
		}
		existing, ok := importByPath[path]
		if !ok {
			existing = functionFileMatch{
				Path: path, Category: classifyPath(path), KindCounts: map[string]int{"import": 1}, HitCount: 1,
			}
			graphOnly++
		} else {
			existing.HitCount++
			if existing.KindCounts == nil {
				existing.KindCounts = map[string]int{}
			}
			existing.KindCounts["import"]++
		}
		importByPath[path] = existing
	}
	text.Imports = make([]functionFileMatch, 0, len(importByPath))
	for _, match := range importByPath {
		text.Imports = append(text.Imports, match)
	}
	sortFunctionMatches(text.Imports)

	// Rebuild files/counts.
	seenFiles := map[string]bool{}
	text.Files = nil
	text.Counts = map[string]int{}
	text.TotalHits = 0
	for _, group := range [][]functionFileMatch{text.Definitions, text.Imports, text.CallSites, text.References} {
		for _, match := range group {
			text.TotalHits += match.HitCount
			if !seenFiles[match.Path] {
				text.Files = append(text.Files, match.Path)
				seenFiles[match.Path] = true
				text.Counts[match.Category]++
			}
		}
	}
	slices.Sort(text.Files)
	text.UniqueFiles = len(text.Files)

	if graphOnly > 0 || len(graphCalls) > 0 || len(graphImports) > 0 {
		if len(text.CallSites) > 0 || len(text.Imports) > 0 {
			meta["resolutionMethod"] = "hybrid"
		} else {
			meta["resolutionMethod"] = "graph"
		}
	}
	meta["graphOnlyFiles"] = graphOnly
	textOnly := 0
	graphPaths := map[string]bool{}
	for _, call := range graphCalls {
		if p := firstAnyString(call, "path"); p != "" {
			graphPaths[p] = true
		}
	}
	for _, imp := range graphImports {
		if p := firstAnyString(imp, "path"); p != "" {
			graphPaths[p] = true
		}
	}
	for _, def := range graphDefs {
		if p := firstAnyString(def, "path"); p != "" {
			graphPaths[p] = true
		}
	}
	for _, path := range text.Files {
		if !graphPaths[path] {
			textOnly++
		}
	}
	meta["textOnlyFiles"] = textOnly
	return text, meta
}

func mapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}

func impactConfidence(method string, hasCallGraph bool, ambiguous bool, stale bool, uniqueFiles int) string {
	if ambiguous {
		return "low"
	}
	if stale {
		return "low"
	}
	switch method {
	case "hybrid":
		if hasCallGraph {
			return "high"
		}
		return "medium"
	case "graph":
		if hasCallGraph {
			return "high"
		}
		return "medium"
	case "text":
		if uniqueFiles == 0 {
			return "low"
		}
		return "medium"
	default:
		return "low"
	}
}

func (s Server) resolveImpactSymbol(ctx context.Context, symbol string, args map[string]any) (map[string]any, error) {
	opts := graph.ResolveOptions{
		Limit:      intArg(args, "limit", 20),
		PackageID:  firstStringArg(args, "packageId", "pkgId"),
		Package:    firstStringArg(args, "package", "pkg"),
		PathPrefix: firstStringArg(args, "pathPrefix", "prefix"),
		SymbolID:   firstStringArg(args, "symbolId", "id"),
	}
	if s.Query.Driver == nil {
		return map[string]any{
			"name": symbol, "candidates": []map[string]any{}, "returned": 0,
			"exactCount": 0, "ambiguous": false, "graphAvailable": false,
		}, nil
	}
	return s.Query.ResolveSymbol(ctx, symbol, opts)
}

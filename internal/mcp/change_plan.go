package mcp

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
)

// prepareChangePlan builds an ordered multi-target plan from symbols and/or paths.
func (s Server) prepareChangePlan(ctx context.Context, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	maxItems := maxItemsArg(args, defaultMaxItems)
	if detail == "summary" {
		maxItems = min(maxItems, 15)
	}

	symbols := uniqueSortedStrings(stringSliceArg(args, "symbols", "symbolNames"))
	if single := firstStringArg(args, "symbol", "name"); single != "" && !slices.Contains(symbols, single) {
		symbols = append(symbols, single)
	}
	paths := normalizePathList(stringSliceArg(args, "paths", "files", "path"))
	query := firstStringArg(args, "query", "q", "feature")
	packageFilter := firstStringArg(args, "package", "pkg")
	if packageFilter == "" {
		packageFilter = firstStringArg(args, "packageId", "pkgId")
	}
	pathPrefix := firstStringArg(args, "pathPrefix", "prefix")

	if boolArg(args, "useDirty", false) {
		for _, dirty := range gitDirtyFiles(s.Repo) {
			paths = append(paths, dirty)
		}
	}
	// If only a free-text query is provided, treat identifier queries as symbols.
	if len(symbols) == 0 && len(paths) == 0 && query != "" && isIdentifier(query) {
		symbols = []string{query}
	}
	if len(symbols) == 0 && len(paths) == 0 && query != "" {
		// Fall back to feature context style pack for non-identifier queries.
		feature, err := s.prepareFeatureContext(ctx, query, args)
		if err != nil {
			return nil, err
		}
		feature["planKind"] = "feature_fallback"
		feature["next"] = "For multi-file refactors pass symbols[] and/or paths[] (or useDirty=true)."
		return feature, nil
	}
	if len(symbols) == 0 && len(paths) == 0 {
		return map[string]any{
			"planKind":  "empty",
			"confidence": "low",
			"next":      "Provide symbols[], paths[], query, or useDirty=true.",
		}, nil
	}

	// Cap fan-out so one tool call cannot explode cost.
	if len(symbols) > 8 {
		symbols = symbols[:8]
	}
	paths = uniqueSortedPaths(paths)
	if len(paths) > 60 {
		paths = paths[:60]
	}

	symbolResults := []map[string]any{}
	mustEdit := []string{}
	mustVerify := []string{}
	mustNotTouch := []string{}
	packages := []string{}
	seenPkg := map[string]bool{}
	seenEdit := map[string]bool{}
	seenVerify := map[string]bool{}
	needsDisambiguation := false
	allCandidates := []map[string]any{}
	methods := []string{}
	confidences := []string{}

	for _, symbol := range symbols {
		impactArgs := map[string]any{
			"symbol": symbol,
			"detail": "files",
			"limit":  intArg(args, "impactLimit", 40),
		}
		if packageFilter != "" {
			impactArgs["package"] = packageFilter
		}
		if pathPrefix != "" {
			impactArgs["pathPrefix"] = pathPrefix
		}
		impact, err := s.analyzeFunctionImpact(ctx, symbol, impactArgs)
		if err != nil {
			return nil, err
		}
		summary := map[string]any{
			"symbol":       symbol,
			"uniqueFiles":  impact["uniqueFiles"],
			"totalHits":    impact["totalHits"],
			"confidence":   impact["confidence"],
			"resolutionMethod": impact["resolutionMethod"],
			"needsDisambiguation": impact["needsDisambiguation"],
		}
		if needs, _ := impact["needsDisambiguation"].(bool); needs {
			needsDisambiguation = true
			if cands := mapSlice(impact["candidates"]); len(cands) > 0 {
				allCandidates = append(allCandidates, cands...)
				summary["candidates"] = cands
			}
		}
		if method, ok := impact["resolutionMethod"].(string); ok {
			methods = append(methods, method)
		}
		if conf, ok := impact["confidence"].(string); ok {
			confidences = append(confidences, conf)
		}
		for _, def := range mapSlice(impact["definitions"]) {
			addPath(&mustEdit, seenEdit, firstAnyString(def, "path"))
		}
		for _, call := range mapSlice(impact["callSites"]) {
			addPath(&mustVerify, seenVerify, firstAnyString(call, "path"))
		}
		for _, imp := range mapSlice(impact["imports"]) {
			addPath(&mustVerify, seenVerify, firstAnyString(imp, "path"))
		}
		symbolResults = append(symbolResults, summary)
	}

	var pathImpact map[string]any
	if len(paths) > 0 {
		pathArgs := map[string]any{
			"paths":  paths,
			"detail": "files",
			"limit":  intArg(args, "pathLimit", 40),
		}
		var err error
		pathImpact, err = s.analyzePathSetImpact(ctx, pathArgs)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			addPath(&mustEdit, seenEdit, p)
		}
		for _, item := range mapSlice(pathImpact["dependents"]) {
			addPath(&mustVerify, seenVerify, firstAnyString(item, "path"))
		}
		for _, item := range mapSlice(pathImpact["relatedTests"]) {
			addPath(&mustVerify, seenVerify, firstAnyString(item, "path"))
		}
		for _, pkg := range stringSlice(pathImpact["packages"]) {
			if !seenPkg[pkg] {
				packages = append(packages, pkg)
				seenPkg[pkg] = true
			}
		}
		if method, ok := pathImpact["resolutionMethod"].(string); ok {
			methods = append(methods, method)
		}
		if conf, ok := pathImpact["confidence"].(string); ok {
			confidences = append(confidences, conf)
		}
	}

	// Heuristic must-not-touch: config/docs seeds that are not in mustEdit.
	for _, path := range mustVerify {
		cat := classifyPath(path)
		if (cat == "docs" || cat == "config") && !seenEdit[path] {
			// docs/config verification is optional; keep out of mustEdit noise.
			continue
		}
	}
	// Exclude mustEdit from mustVerify lists.
	mustVerify = filterOut(mustVerify, seenEdit)

	// Suggested order: definitions/seeds first, then dependents, then tests.
	suggestedOrder := []string{}
	seenOrder := map[string]bool{}
	for _, p := range mustEdit {
		addPath(&suggestedOrder, seenOrder, p)
	}
	for _, p := range mustVerify {
		if classifyPath(p) == "test" {
			continue
		}
		addPath(&suggestedOrder, seenOrder, p)
	}
	for _, p := range mustVerify {
		if classifyPath(p) == "test" {
			addPath(&suggestedOrder, seenOrder, p)
		}
	}

	// Package hints from mustEdit paths.
	for _, path := range mustEdit {
		if pkg := packageHintFromPath(path); pkg != "" && !seenPkg[pkg] {
			packages = append(packages, pkg)
			seenPkg[pkg] = true
		}
	}

	stale := false
	graphReliable := false
	if freshness, err := s.indexFreshness(ctx); err == nil {
		if v, ok := freshness["stale"].(bool); ok {
			stale = v
		}
		if v, ok := freshness["graphReliable"].(bool); ok {
			graphReliable = v
		}
	}

	confidence := rollupConfidence(confidences, needsDisambiguation, stale)
	method := rollupMethod(methods)

	mustEditCount := len(mustEdit)
	mustVerifyCount := len(mustVerify)
	mustEditSample, mustEditSampled := limitSlice(mustEdit, maxItems)
	mustVerifySample, mustVerifySampled := limitSlice(mustVerify, maxItems)
	suggestedOrder, _ = limitSlice(suggestedOrder, maxItems+5)
	packages, _ = limitSlice(packages, 20)

	openNext := []map[string]any{}
	for i, path := range mustEditSample {
		if i >= 3 {
			break
		}
		openNext = append(openNext, map[string]any{"path": path, "why": "must_edit"})
	}
	for _, path := range mustVerifySample {
		if len(openNext) >= 5 {
			break
		}
		if classifyPath(path) == "test" {
			openNext = append(openNext, map[string]any{"path": path, "why": "must_verify_test"})
			break
		}
	}

	response := map[string]any{
		"planKind":            "change",
		"symbols":             symbols,
		"seedPaths":           paths,
		"query":               query,
		"symbolCount":         len(symbols),
		"seedPathCount":       len(paths),
		"packages":            packages,
		"packageCount":        len(packages),
		"mustEditCount":       mustEditCount,
		"mustVerifyCount":     mustVerifyCount,
		"mustEditSample":      mustEditSample,
		"mustVerifySample":    mustVerifySample,
		"mustEditIsSample":    mustEditSampled,
		"mustVerifyIsSample":  mustVerifySampled,
		"mustNotTouch":        mustNotTouch,
		"suggestedOrder":      suggestedOrder,
		"openNext":            openNext,
		"symbolImpacts":       symbolResults,
		"resolutionMethod":    method,
		"confidence":          confidence,
		"needsDisambiguation": needsDisambiguation,
		"graphReliable":       graphReliable,
		"stale":               stale,
		"contextCompleteForPlanning": !needsDisambiguation,
		"detail":              detail,
		"totals": map[string]any{
			"mustEditFiles":   mustEditCount,
			"mustVerifyFiles": mustVerifyCount,
		},
		"reportUsing": []string{
			"mustEditCount / totals.mustEditFiles — NEVER len(mustEditSample)",
			"mustVerifyCount for verification surface",
		},
		"stopConditions": []string{
			"If needsDisambiguation=true, call resolve_symbol and rerun with symbolId/package before editing.",
			"If stale=true or graphReliable=false, prefer text residual and consider reindex.",
			"If truncated on any child impact, split symbols/paths into smaller batches.",
			"Do not report mustEditSample length as total impact.",
		},
		"next": "Edit mustEditCount paths (see mustEditSample for examples) in suggestedOrder. Verify mustVerify. Do not expand into mustNotTouch without evidence.",
	}
	// Never put a capped list on mustEdit/mustVerify when sampled — agents count the array.
	if !mustEditSampled {
		response["mustEdit"] = mustEditSample
	} else {
		response["mustEditNote"] = "SAMPLE only in mustEditSample. Total is mustEditCount / totals.mustEditFiles."
	}
	if !mustVerifySampled {
		response["mustVerify"] = mustVerifySample
	} else {
		response["mustVerifyNote"] = "SAMPLE only in mustVerifySample. Total is mustVerifyCount."
	}
	if packageFilter != "" {
		response["package"] = packageFilter
	}
	if pathPrefix != "" {
		response["pathPrefix"] = pathPrefix
	}
	if needsDisambiguation && len(allCandidates) > 0 {
		response["candidates"], _ = limitSlice(dedupeCandidates(allCandidates), 12)
		response["next"] = "Disambiguate symbols first via resolve_symbol (package/pathPrefix/symbolId), then rerun prepare_change_plan."
	}
	if pathImpact != nil {
		if detail == "summary" {
			response["pathImpactSummary"] = map[string]any{
				"dependentCount":   pathImpact["dependentCount"],
				"relatedTestCount": pathImpact["relatedTestCount"],
				"confidence":       pathImpact["confidence"],
			}
		} else {
			response["pathImpact"] = pathImpact
		}
	}
	if detail == "summary" && len(symbolResults) > 3 {
		response["symbolImpacts"], _ = limitSlice(symbolResults, 3)
		response["symbolImpactTotal"] = len(symbolResults)
	}

	// Explicit empty mustNotTouch guidance for agents.
	if len(mustNotTouch) == 0 {
		response["mustNotTouchNote"] = "No hard exclusions inferred. Stay inside mustEdit/mustVerify unless a new dependent appears."
	}

	return response, nil
}

func addPath(dst *[]string, seen map[string]bool, path string) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || seen[path] {
		return
	}
	seen[path] = true
	*dst = append(*dst, path)
}

func filterOut(paths []string, exclude map[string]bool) []string {
	out := []string{}
	for _, path := range paths {
		if exclude[path] {
			continue
		}
		out = append(out, path)
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func rollupConfidence(values []string, ambiguous bool, stale bool) string {
	if ambiguous || stale {
		return "low"
	}
	if len(values) == 0 {
		return "medium"
	}
	hasHigh := false
	hasLow := false
	for _, value := range values {
		switch value {
		case "high":
			hasHigh = true
		case "low":
			hasLow = true
		}
	}
	if hasLow {
		return "low"
	}
	if hasHigh {
		return "high"
	}
	return "medium"
}

func rollupMethod(methods []string) string {
	if len(methods) == 0 {
		return "text"
	}
	hasGraph := false
	hasText := false
	hasHybrid := false
	for _, method := range methods {
		switch method {
		case "graph":
			hasGraph = true
		case "text":
			hasText = true
		case "hybrid":
			hasHybrid = true
		}
	}
	if hasHybrid || (hasGraph && hasText) {
		return "hybrid"
	}
	if hasGraph {
		return "graph"
	}
	return "text"
}

func dedupeCandidates(items []map[string]any) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, item := range items {
		id := firstAnyString(item, "id", "path", "name")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, item)
	}
	return out
}

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// analyzePathSetImpact bounds blast radius for a set of file paths using graph
// file relations when available, plus filesystem import/test heuristics.
func (s Server) analyzePathSetImpact(ctx context.Context, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	limit := intArg(args, "limit", 40)
	maxItems := maxItemsArg(args, defaultMaxItems)
	if detail == "summary" {
		maxItems = min(maxItems, 15)
	}

	paths := normalizePathList(stringSliceArg(args, "paths", "files", "path"))
	if boolArg(args, "useDirty", false) {
		for _, dirty := range gitDirtyFiles(s.Repo) {
			if isSearchableFile(dirty) || strings.HasSuffix(dirty, ".ts") || strings.HasSuffix(dirty, ".tsx") ||
				strings.HasSuffix(dirty, ".js") || strings.HasSuffix(dirty, ".jsx") {
				paths = append(paths, dirty)
			}
		}
	}
	paths = uniqueSortedPaths(paths)
	if len(paths) == 0 {
		return map[string]any{
			"seedPaths":     []string{},
			"uniqueFiles":   0,
			"dependents":    []map[string]any{},
			"relatedTests":  []map[string]any{},
			"packages":      []string{},
			"resolutionMethod": "empty",
			"confidence":    "low",
			"next":          "Pass paths[] or useDirty=true with a dirty git tree.",
		}, nil
	}
	if len(paths) > 80 {
		paths = paths[:80]
	}

	textImpact := textPathSetImpact(s.Repo, paths, limit)
	var graphImpact map[string]any
	if s.Query.Driver != nil {
		if g, err := s.Query.PathSetImpact(ctx, paths, limit); err == nil {
			graphImpact = g
		} else {
			graphImpact = map[string]any{"graphError": err.Error()}
		}
	}

	merged := mergePathSetImpact(paths, textImpact, graphImpact)
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

	method := "text"
	if graphImpact != nil && len(mapSlice(graphImpact["dependents"])) > 0 {
		if textImpact.importerCount > 0 {
			method = "hybrid"
		} else {
			method = "graph"
		}
	} else if graphImpact != nil && len(mapSlice(graphImpact["seedFiles"])) > 0 {
		method = "hybrid"
	}

	confidence := "medium"
	if method == "hybrid" && graphReliable && !stale {
		confidence = "high"
	}
	if len(paths) > 40 || stale {
		confidence = "low"
	}

	dependents, depTrunc := limitSlice(merged.dependents, maxItems)
	tests, testTrunc := limitSlice(merged.tests, maxItems)
	deps, depsTrunc := limitSlice(merged.dependencies, maxItems)
	packages, _ := limitSlice(merged.packages, 20)

	mustVerify := []string{}
	for _, d := range dependents {
		if p := firstAnyString(d, "path"); p != "" {
			mustVerify = append(mustVerify, p)
		}
		if len(mustVerify) >= 12 {
			break
		}
	}
	for _, t := range tests {
		if p := firstAnyString(t, "path"); p != "" && !slices.Contains(mustVerify, p) {
			mustVerify = append(mustVerify, p)
		}
		if len(mustVerify) >= 16 {
			break
		}
	}

	response := map[string]any{
		"seedPaths":          paths,
		"seedCount":          len(paths),
		"uniqueFiles":        merged.uniqueFiles,
		"dependentCount":     len(merged.dependents),
		"relatedTestCount":   len(merged.tests),
		"dependencyCount":    len(merged.dependencies),
		"packages":           packages,
		"packageCount":       len(merged.packages),
		"dependents":         dependents,
		"relatedTests":       tests,
		"resolutionMethod":   method,
		"confidence":         confidence,
		"graphReliable":      graphReliable,
		"stale":              stale,
		"truncated":          depTrunc || testTrunc || depsTrunc || len(paths) >= 80,
		"detail":             detail,
		"mustEdit":           paths,
		"mustVerify":         mustVerify,
		"suggestedOrder":     suggestedPathOrder(paths, dependents, tests),
		"openNext":           openNextFromPathImpact(paths, dependents, tests),
		"stopConditions": []string{
			"If truncated=true, raise limit or split the path set.",
			"If confidence=low or stale=true, reindex before trusting graph dependents.",
		},
	}
	if detail != "summary" {
		response["dependencies"] = deps
		response["textImporterCount"] = textImpact.importerCount
		if graphImpact != nil {
			response["graphDependentCount"] = len(mapSlice(graphImpact["dependents"]))
		}
	}
	return response, nil
}

type textPathImpact struct {
	importers     []map[string]any
	tests         []map[string]any
	importerCount int
}

func textPathSetImpact(repo string, paths []string, limit int) textPathImpact {
	result := textPathImpact{}
	if repo == "" || len(paths) == 0 {
		return result
	}
	seenImp := map[string]bool{}
	seenTest := map[string]bool{}
	seedSet := map[string]bool{}
	for _, p := range paths {
		seedSet[filepath.ToSlash(p)] = true
	}

	// Co-located / naming-convention tests without scanning the whole tree.
	for _, path := range paths {
		path = filepath.ToSlash(path)
		for _, candidate := range relatedTestCandidates(path) {
			full := filepath.Join(repo, filepath.FromSlash(candidate))
			if _, err := os.Stat(full); err != nil {
				continue
			}
			if seenTest[candidate] {
				continue
			}
			result.tests = append(result.tests, map[string]any{
				"path": candidate, "reason": "colocated_or_named_test", "source": "text",
			})
			seenTest[candidate] = true
		}
	}

	// Import reverse lookup via rg on unique path stems.
	queries := importSearchQueries(paths)
	for _, query := range queries {
		if len(result.importers) >= limit {
			break
		}
		files, ok, err := candidateFiles(repo, query, false, false, 0)
		if err != nil || !ok {
			continue
		}
		for _, rel := range files {
			rel = filepath.ToSlash(rel)
			if seedSet[rel] || seenImp[rel] {
				continue
			}
			if !isSearchableFile(rel) {
				continue
			}
			category := classifyPath(rel)
			entry := map[string]any{
				"path": rel, "category": category, "reason": "import_or_path_reference", "source": "text", "query": query,
			}
			if category == "test" {
				if !seenTest[rel] {
					result.tests = append(result.tests, map[string]any{
						"path": rel, "reason": "test_references_seed", "source": "text",
					})
					seenTest[rel] = true
				}
			} else {
				result.importers = append(result.importers, entry)
				seenImp[rel] = true
			}
			if len(result.importers) >= limit {
				break
			}
		}
	}
	result.importerCount = len(result.importers)
	return result
}

func relatedTestCandidates(path string) []string {
	path = filepath.ToSlash(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	dir := filepath.ToSlash(filepath.Dir(path))
	name := strings.TrimSuffix(filepath.Base(path), ext)
	candidates := []string{
		base + ".test" + ext,
		base + ".spec" + ext,
		dir + "/__tests__/" + name + testExt(ext),
		dir + "/__tests__/" + name + ".test" + ext,
	}
	// JS/TS dual extensions
	for _, alt := range []string{".ts", ".tsx", ".js", ".jsx"} {
		if alt == ext {
			continue
		}
		candidates = append(candidates, base+".test"+alt, base+".spec"+alt)
	}
	return uniqueSortedPaths(candidates)
}

func testExt(ext string) string {
	switch ext {
	case ".tsx":
		return ".tsx"
	case ".jsx":
		return ".jsx"
	case ".js":
		return ".js"
	default:
		return ".ts"
	}
}

func importSearchQueries(paths []string) []string {
	seen := map[string]bool{}
	queries := []string{}
	for _, path := range paths {
		path = filepath.ToSlash(path)
		// Prefer distinctive path fragments over bare basenames when possible.
		noExt := strings.TrimSuffix(path, filepath.Ext(path))
		parts := strings.Split(noExt, "/")
		if len(parts) >= 2 {
			frag := parts[len(parts)-2] + "/" + parts[len(parts)-1]
			if !seen[frag] && len(frag) >= 4 {
				queries = append(queries, frag)
				seen[frag] = true
			}
		}
		base := parts[len(parts)-1]
		if base != "" && base != "index" && base != "mod" && !seen[base] && len(base) >= 3 {
			queries = append(queries, base)
			seen[base] = true
		}
	}
	return queries
}

type mergedPathImpact struct {
	dependents   []map[string]any
	dependencies []map[string]any
	tests        []map[string]any
	packages     []string
	uniqueFiles  int
}

func mergePathSetImpact(seeds []string, text textPathImpact, graph map[string]any) mergedPathImpact {
	out := mergedPathImpact{}
	seenDep := map[string]bool{}
	seenTest := map[string]bool{}
	seenPkg := map[string]bool{}
	seedSet := map[string]bool{}
	for _, s := range seeds {
		seedSet[filepath.ToSlash(s)] = true
	}

	addPkg := func(pkg string) {
		if pkg == "" || seenPkg[pkg] {
			return
		}
		out.packages = append(out.packages, pkg)
		seenPkg[pkg] = true
	}

	for _, item := range mapSlice(graph["dependents"]) {
		path := firstAnyString(item, "path")
		if path == "" || seedSet[path] || seenDep[path] {
			continue
		}
		// TESTS_FILE edges are tests, not runtime dependents.
		if firstAnyString(item, "rel") == "TESTS_FILE" || classifyPath(path) == "test" {
			if !seenTest[path] {
				out.tests = append(out.tests, map[string]any{
					"path": path, "reason": "graph_tests_file", "source": "graph",
				})
				seenTest[path] = true
			}
			continue
		}
		item["role"] = "dependent"
		if item["source"] == nil {
			item["source"] = "graph"
		}
		out.dependents = append(out.dependents, item)
		seenDep[path] = true
		addPkg(firstAnyString(item, "packageId"))
	}
	for _, item := range text.importers {
		path := firstAnyString(item, "path")
		if path == "" || seedSet[path] || seenDep[path] {
			continue
		}
		out.dependents = append(out.dependents, item)
		seenDep[path] = true
	}
	for _, item := range mapSlice(graph["dependencies"]) {
		path := firstAnyString(item, "path")
		if path == "" {
			continue
		}
		out.dependencies = append(out.dependencies, item)
		addPkg(firstAnyString(item, "packageId"))
	}
	for _, item := range text.tests {
		path := firstAnyString(item, "path")
		if path == "" || seenTest[path] {
			continue
		}
		out.tests = append(out.tests, item)
		seenTest[path] = true
	}
	for _, item := range mapSlice(graph["seedFiles"]) {
		addPkg(firstAnyString(item, "packageId"))
	}
	for _, pkg := range stringSlice(graph["packages"]) {
		addPkg(pkg)
	}

	// Heuristic packages from path prefixes when graph has none.
	if len(out.packages) == 0 {
		for _, path := range seeds {
			if pkg := packageHintFromPath(path); pkg != "" && !seenPkg[pkg] {
				out.packages = append(out.packages, pkg)
				seenPkg[pkg] = true
			}
		}
	}

	files := map[string]bool{}
	for _, p := range seeds {
		files[p] = true
	}
	for _, group := range [][]map[string]any{out.dependents, out.dependencies, out.tests} {
		for _, item := range group {
			if p := firstAnyString(item, "path"); p != "" {
				files[p] = true
			}
		}
	}
	out.uniqueFiles = len(files)
	return out
}

func packageHintFromPath(path string) string {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && (parts[0] == "packages" || parts[0] == "apps") {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

func suggestedPathOrder(seeds []string, dependents []map[string]any, tests []map[string]any) []string {
	order := append([]string{}, seeds...)
	for _, d := range dependents {
		if p := firstAnyString(d, "path"); p != "" && !slices.Contains(order, p) {
			order = append(order, p)
		}
		if len(order) >= 20 {
			break
		}
	}
	for _, t := range tests {
		if p := firstAnyString(t, "path"); p != "" && !slices.Contains(order, p) {
			order = append(order, p)
		}
		if len(order) >= 24 {
			break
		}
	}
	return order
}

func openNextFromPathImpact(seeds []string, dependents []map[string]any, tests []map[string]any) []map[string]any {
	reads := []map[string]any{}
	for i, seed := range seeds {
		if i >= 2 {
			break
		}
		reads = append(reads, map[string]any{"path": seed, "why": "seed_edit"})
	}
	if len(dependents) > 0 {
		if p := firstAnyString(dependents[0], "path"); p != "" {
			reads = append(reads, map[string]any{"path": p, "why": "top_dependent"})
		}
	}
	if len(tests) > 0 {
		if p := firstAnyString(tests[0], "path"); p != "" {
			reads = append(reads, map[string]any{"path": p, "why": "related_test"})
		}
	}
	if len(reads) > 5 {
		reads = reads[:5]
	}
	return reads
}

func normalizePathList(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		path = strings.TrimPrefix(path, "./")
		path = filepath.ToSlash(path)
		if path == "" || path == "." {
			continue
		}
		out = append(out, path)
	}
	return out
}

func uniqueSortedPaths(paths []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}



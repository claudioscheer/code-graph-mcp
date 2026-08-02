package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Service struct {
	Driver neo4j.DriverWithContext
	Ripple string
}

type Options struct {
	Depth         int
	Limit         int
	MinConfidence float64
	Direction     string
}

func (s Service) Search(ctx context.Context, query string, opts Options) (map[string]any, error) {
	opts = normalize(opts)
	return queryNodes(ctx, s.Driver, `
		MATCH (n:GraphNode)
		WHERE n.ripple = $ripple
			AND (
				toLower(coalesce(n.id, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.sourceId, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.name, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.path, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.filePath, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.kind, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.packageId, "")) CONTAINS toLower($query)
				OR toLower(coalesce(n.primaryLabel, "")) CONTAINS toLower($query)
			)
		RETURN n AS node
		ORDER BY n.primaryLabel, coalesce(n.path, n.filePath, n.name, n.id), n.id
		LIMIT $limit
	`, map[string]any{"query": query, "ripple": s.Ripple, "limit": opts.Limit + 1}, opts.Limit)
}

func (s Service) Metadata(ctx context.Context) (map[string]any, error) {
	result, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		CALL {
			MATCH (n:GraphNode {ripple: $ripple})
			RETURN count(n) AS nodes
		}
		CALL {
			MATCH ()-[r]->()
			WHERE r.ripple = $ripple
			RETURN count(r) AS relationships
		}
		OPTIONAL MATCH (ripple:Ripple {name: $ripple})
		RETURN nodes, relationships, ripple.repo AS repo, ripple.language AS language, coalesce(ripple.analysisMode, "full") AS analysisMode, ripple.createdAt AS createdAt, ripple.updatedAt AS updatedAt
	`, map[string]any{"ripple": s.Ripple}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	if len(result.Records) == 0 {
		return map[string]any{"ripple": s.Ripple, "nodes": 0, "relationships": 0}, nil
	}
	out := result.Records[0].AsMap()
	out["ripple"] = s.Ripple
	return out, nil
}

func (s Service) Types(ctx context.Context) (map[string]any, error) {
	labelResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (n:GraphNode {ripple: $ripple})
		RETURN coalesce(n.primaryLabel, "Unknown") AS label, count(n) AS count
		ORDER BY count DESC, label
	`, map[string]any{"ripple": s.Ripple}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	relResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH ()-[r]->()
		WHERE r.ripple = $ripple
		RETURN type(r) AS type, count(r) AS count
		ORDER BY count DESC, type
	`, map[string]any{"ripple": s.Ripple}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	labels := []map[string]any{}
	for _, record := range labelResult.Records {
		labels = append(labels, record.AsMap())
	}
	relationships := []map[string]any{}
	for _, record := range relResult.Records {
		relationships = append(relationships, record.AsMap())
	}
	return map[string]any{"ripple": s.Ripple, "nodeLabels": labels, "relationshipTypes": relationships}, nil
}

func (s Service) FindSymbol(ctx context.Context, name string, opts Options) (map[string]any, error) {
	opts = normalize(opts)
	return queryNodes(ctx, s.Driver, `
		MATCH (n:GraphNode:Symbol)
		WHERE n.ripple = $ripple AND toLower(coalesce(n.name, "")) CONTAINS toLower($name)
		RETURN n AS node
		ORDER BY n.name, n.id
		LIMIT $limit
	`, map[string]any{"name": name, "ripple": s.Ripple, "limit": opts.Limit + 1}, opts.Limit)
}

// ResolveOptions controls symbol disambiguation for hybrid impact tools.
type ResolveOptions struct {
	Limit      int
	PackageID  string
	Package    string
	PathPrefix string
	SymbolID   string
}

// ResolveSymbol returns ranked Symbol candidates for a name (or a single id lookup).
// Exact name matches are preferred over substring matches. Nested locals (name contains
// "::") are deprioritized so agents pick top-level exports first.
func (s Service) ResolveSymbol(ctx context.Context, name string, opts ResolveOptions) (map[string]any, error) {
	if s.Driver == nil {
		return map[string]any{
			"name": name, "candidates": []map[string]any{}, "returned": 0, "exactCount": 0,
			"ambiguous": false, "truncated": false, "graphAvailable": false,
		}, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}

	if opts.SymbolID != "" {
		node, err := s.Node(ctx, opts.SymbolID)
		if err != nil {
			return map[string]any{
				"name": name, "symbolId": opts.SymbolID, "candidates": []map[string]any{},
				"returned": 0, "exactCount": 0, "ambiguous": false, "truncated": false,
				"graphAvailable": true, "error": err.Error(),
			}, nil
		}
		candidate := symbolCandidate(node)
		exact := 0
		if strings.EqualFold(firstString(candidate, "name"), name) || name == "" {
			exact = 1
		}
		return map[string]any{
			"name":           name,
			"symbolId":       opts.SymbolID,
			"candidates":     []map[string]any{candidate},
			"returned":       1,
			"exactCount":     exact,
			"ambiguous":      false,
			"truncated":      false,
			"graphAvailable": true,
			"selected":       candidate,
		}, nil
	}

	if name == "" {
		return map[string]any{
			"name": name, "candidates": []map[string]any{}, "returned": 0, "exactCount": 0,
			"ambiguous": false, "truncated": false, "graphAvailable": true,
		}, nil
	}

	result, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (n:GraphNode:Symbol {ripple: $ripple})
		WHERE toLower(coalesce(n.name, "")) = toLower($name)
			OR toLower(coalesce(n.name, "")) CONTAINS toLower($name)
			OR toLower(coalesce(n.sourceId, "")) CONTAINS toLower($name)
		RETURN n AS node
		ORDER BY
			CASE WHEN toLower(coalesce(n.name, "")) = toLower($name) THEN 0 ELSE 1 END,
			CASE WHEN coalesce(n.name, "") CONTAINS '::' THEN 1 ELSE 0 END,
			CASE WHEN coalesce(n.exported, false) = true THEN 0 ELSE 1 END,
			coalesce(n.filePath, n.path, n.sourceId),
			n.name
		LIMIT $limit
	`, map[string]any{"name": name, "ripple": s.Ripple, "limit": opts.Limit + 1}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}

	candidates := []map[string]any{}
	exactCount := 0
	for i, record := range result.Records {
		if i >= opts.Limit {
			break
		}
		// Full props include exported / line ranges for ranking fields already applied in Cypher.
		full := nodeMap(record.AsMap()["node"].(neo4j.Node))
		candidate := symbolCandidate(full)
		if !matchesResolveFilters(candidate, opts) {
			continue
		}
		if strings.EqualFold(firstString(candidate, "name"), name) {
			exactCount++
			candidate["match"] = "exact"
		} else {
			candidate["match"] = "contains"
		}
		candidates = append(candidates, candidate)
	}

	// Recompute after filters (Cypher LIMIT was before filter; re-query if filters emptied result)
	if (opts.PackageID != "" || opts.Package != "" || opts.PathPrefix != "") && len(candidates) == 0 && len(result.Records) > 0 {
		// Filters removed all limited rows; run a wider filtered query.
		return s.resolveSymbolFiltered(ctx, name, opts)
	}

	var selected map[string]any
	ambiguous := false
	if exactCount == 1 {
		for _, c := range candidates {
			if c["match"] == "exact" {
				selected = c
				break
			}
		}
	} else if exactCount > 1 {
		ambiguous = true
	} else if len(candidates) == 1 {
		selected = candidates[0]
	} else if len(candidates) > 1 {
		ambiguous = true
	}

	out := map[string]any{
		"name":           name,
		"candidates":     candidates,
		"returned":       len(candidates),
		"exactCount":     exactCount,
		"ambiguous":      ambiguous,
		"truncated":      len(result.Records) > opts.Limit,
		"graphAvailable": true,
	}
	if selected != nil {
		out["selected"] = selected
	}
	if opts.PackageID != "" {
		out["packageId"] = opts.PackageID
	}
	if opts.Package != "" {
		out["package"] = opts.Package
	}
	if opts.PathPrefix != "" {
		out["pathPrefix"] = opts.PathPrefix
	}
	return out, nil
}

func (s Service) resolveSymbolFiltered(ctx context.Context, name string, opts ResolveOptions) (map[string]any, error) {
	// Pull a larger candidate set then filter in Go (package/path filters are flexible).
	wide := opts
	wide.Limit = min(opts.Limit*10, 200)
	if wide.Limit < 50 {
		wide.Limit = 50
	}
	result, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (n:GraphNode:Symbol {ripple: $ripple})
		WHERE toLower(coalesce(n.name, "")) = toLower($name)
			OR toLower(coalesce(n.name, "")) CONTAINS toLower($name)
		RETURN n AS node
		ORDER BY
			CASE WHEN toLower(coalesce(n.name, "")) = toLower($name) THEN 0 ELSE 1 END,
			CASE WHEN coalesce(n.name, "") CONTAINS '::' THEN 1 ELSE 0 END,
			coalesce(n.filePath, n.path, n.sourceId)
		LIMIT $limit
	`, map[string]any{"name": name, "ripple": s.Ripple, "limit": wide.Limit}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	candidates := []map[string]any{}
	exactCount := 0
	for _, record := range result.Records {
		full := nodeMap(record.AsMap()["node"].(neo4j.Node))
		candidate := symbolCandidate(full)
		if !matchesResolveFilters(candidate, opts) {
			continue
		}
		if strings.EqualFold(firstString(candidate, "name"), name) {
			exactCount++
			candidate["match"] = "exact"
		} else {
			candidate["match"] = "contains"
		}
		candidates = append(candidates, candidate)
		if len(candidates) >= opts.Limit {
			break
		}
	}
	var selected map[string]any
	ambiguous := false
	if exactCount == 1 {
		for _, c := range candidates {
			if c["match"] == "exact" {
				selected = c
				break
			}
		}
	} else if exactCount > 1 {
		ambiguous = true
	} else if len(candidates) == 1 {
		selected = candidates[0]
	} else if len(candidates) > 1 {
		ambiguous = true
	}
	out := map[string]any{
		"name": name, "candidates": candidates, "returned": len(candidates),
		"exactCount": exactCount, "ambiguous": ambiguous, "truncated": false, "graphAvailable": true,
	}
	if selected != nil {
		out["selected"] = selected
	}
	return out, nil
}

// SymbolImpact walks CALLS/RENDERS/INSTANTIATES (callers) and IMPORTS_SYMBOL for selected symbols.
func (s Service) SymbolImpact(ctx context.Context, symbolSourceIDs []string, opts Options) (map[string]any, error) {
	opts = normalize(opts)
	if s.Driver == nil || len(symbolSourceIDs) == 0 {
		return map[string]any{
			"definitions": []map[string]any{}, "callSites": []map[string]any{}, "imports": []map[string]any{},
			"graphAvailable": s.Driver != nil, "hasCallGraph": false,
		}, nil
	}

	scoped := make([]string, 0, len(symbolSourceIDs))
	for _, id := range symbolSourceIDs {
		scoped = append(scoped, s.scopedID(id))
	}

	defResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (sym:GraphNode:Symbol {ripple: $ripple})
		WHERE sym.sourceId IN $ids OR sym.id IN $scoped
		RETURN sym AS node
	`, map[string]any{"ripple": s.Ripple, "ids": symbolSourceIDs, "scoped": scoped}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	definitions := []map[string]any{}
	for _, record := range defResult.Records {
		full := nodeMap(record.AsMap()["node"].(neo4j.Node))
		definitions = append(definitions, symbolCandidate(full))
	}

	callResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (caller:GraphNode {ripple: $ripple})-[r]->(sym:GraphNode:Symbol {ripple: $ripple})
		WHERE (sym.sourceId IN $ids OR sym.id IN $scoped)
			AND type(r) IN ['CALLS', 'RENDERS', 'INSTANTIATES']
			AND coalesce(r.confidence, 1.0) >= $minConfidence
		RETURN caller AS node, type(r) AS rel, r.sourceFile AS sourceFile, r.startLine AS startLine,
			coalesce(r.confidence, 1.0) AS confidence, caller.name AS owner
		ORDER BY coalesce(r.sourceFile, caller.filePath, caller.path), coalesce(r.startLine, 0)
		LIMIT $limit
	`, map[string]any{
		"ripple": s.Ripple, "ids": symbolSourceIDs, "scoped": scoped,
		"minConfidence": opts.MinConfidence, "limit": opts.Limit + 1,
	}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	callSites := []map[string]any{}
	for i, record := range callResult.Records {
		if i >= opts.Limit {
			break
		}
		values := record.AsMap()
		caller := nodeMapLean(values["node"].(neo4j.Node))
		path := stringValue(values["sourceFile"])
		if path == "" {
			path = firstString(caller, "path", "filePath")
		}
		entry := map[string]any{
			"path":     path,
			"kind":     stringValue(values["rel"]),
			"owner":    stringValue(values["owner"]),
			"source":   "graph",
			"hitCount": 1,
		}
		if line := values["startLine"]; line != nil {
			entry["line"] = line
		}
		if conf := values["confidence"]; conf != nil {
			entry["confidence"] = conf
		}
		if name := firstString(caller, "name"); name != "" && entry["owner"] == "" {
			entry["owner"] = name
		}
		callSites = append(callSites, entry)
	}

	importResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (file:GraphNode {ripple: $ripple})-[r:IMPORTS_SYMBOL]->(sym:GraphNode:Symbol {ripple: $ripple})
		WHERE (sym.sourceId IN $ids OR sym.id IN $scoped)
			AND coalesce(r.confidence, 1.0) >= $minConfidence
		RETURN file AS node, r.sourceFile AS sourceFile, r.startLine AS startLine
		ORDER BY coalesce(file.path, file.filePath, r.sourceFile)
		LIMIT $limit
	`, map[string]any{
		"ripple": s.Ripple, "ids": symbolSourceIDs, "scoped": scoped,
		"minConfidence": opts.MinConfidence, "limit": opts.Limit + 1,
	}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	imports := []map[string]any{}
	for i, record := range importResult.Records {
		if i >= opts.Limit {
			break
		}
		values := record.AsMap()
		file := nodeMapLean(values["node"].(neo4j.Node))
		path := stringValue(values["sourceFile"])
		if path == "" {
			path = firstString(file, "path", "filePath")
		}
		entry := map[string]any{"path": path, "kind": "import", "source": "graph", "hitCount": 1}
		if line := values["startLine"]; line != nil {
			entry["line"] = line
		}
		imports = append(imports, entry)
	}

	hasCallGraph := len(callSites) > 0
	if !hasCallGraph {
		// Distinguish "no callers" from "no CALLS edges in index".
		if count, cerr := s.CountRelationships(ctx, []string{"CALLS", "RENDERS", "INSTANTIATES"}); cerr == nil && count > 0 {
			hasCallGraph = true
		}
	}

	return map[string]any{
		"definitions":    definitions,
		"callSites":      callSites,
		"imports":        imports,
		"graphAvailable": true,
		"hasCallGraph":   hasCallGraph,
		"truncated":      len(callResult.Records) > opts.Limit || len(importResult.Records) > opts.Limit,
		"returnedCalls":  len(callSites),
		"returnedImports": len(imports),
	}, nil
}

// CountRelationships returns how many edges of the given types exist for this ripple.
func (s Service) CountRelationships(ctx context.Context, types []string) (int64, error) {
	if s.Driver == nil {
		return 0, nil
	}
	if len(types) == 0 {
		types = []string{"CALLS"}
	}
	result, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH ()-[r]->()
		WHERE r.ripple = $ripple AND type(r) IN $types
		RETURN count(r) AS count
	`, map[string]any{"ripple": s.Ripple, "types": types}, neo4j.EagerResultTransformer)
	if err != nil {
		return 0, err
	}
	if len(result.Records) == 0 {
		return 0, nil
	}
	switch v := result.Records[0].AsMap()["count"].(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	default:
		return 0, nil
	}
}

// PathSetImpact returns inbound/outbound file relations and package ids for seed paths.
func (s Service) PathSetImpact(ctx context.Context, paths []string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 40
	}
	if limit > 200 {
		limit = 200
	}
	if s.Driver == nil || len(paths) == 0 {
		return map[string]any{
			"seedFiles": []map[string]any{}, "dependents": []map[string]any{}, "dependencies": []map[string]any{},
			"packages": []string{}, "graphAvailable": s.Driver != nil,
		}, nil
	}
	sourceIDs := make([]string, 0, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
		if path == "" {
			continue
		}
		normalized = append(normalized, path)
		sourceIDs = append(sourceIDs, "file:"+path)
	}
	if len(normalized) == 0 {
		return map[string]any{
			"seedFiles": []map[string]any{}, "dependents": []map[string]any{}, "dependencies": []map[string]any{},
			"packages": []string{}, "graphAvailable": true,
		}, nil
	}

	seedResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (f:GraphNode:File {ripple: $ripple})
		WHERE f.sourceId IN $sourceIDs OR f.path IN $paths
		RETURN f.path AS path, coalesce(f.packageId, "") AS packageId, coalesce(f.kind, "") AS kind
		ORDER BY f.path
	`, map[string]any{"ripple": s.Ripple, "paths": normalized, "sourceIDs": sourceIDs}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	seedFiles := []map[string]any{}
	packages := []string{}
	seenPkg := map[string]bool{}
	for _, record := range seedResult.Records {
		values := record.AsMap()
		entry := map[string]any{"path": values["path"], "source": "graph"}
		if pkg := stringValue(values["packageId"]); pkg != "" {
			entry["packageId"] = pkg
			if !seenPkg[pkg] {
				packages = append(packages, pkg)
				seenPkg[pkg] = true
			}
		}
		if kind := stringValue(values["kind"]); kind != "" {
			entry["kind"] = kind
		}
		seedFiles = append(seedFiles, entry)
	}

	depResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (f:GraphNode:File {ripple: $ripple})
		WHERE f.sourceId IN $sourceIDs OR f.path IN $paths
		MATCH (other:GraphNode {ripple: $ripple})-[r]->(f)
		WHERE type(r) IN ['IMPORTS_FILE', 'RE_EXPORTS_FILE', 'DYNAMIC_IMPORTS_FILE', 'TESTS_FILE']
			AND coalesce(r.confidence, 1.0) >= 0.5
		RETURN DISTINCT coalesce(other.path, other.filePath, other.sourceId) AS path,
			type(r) AS rel,
			coalesce(other.packageId, "") AS packageId,
			coalesce(other.primaryLabel, "") AS label
		ORDER BY path
		LIMIT $limit
	`, map[string]any{"ripple": s.Ripple, "paths": normalized, "sourceIDs": sourceIDs, "limit": limit + 1}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	dependents := []map[string]any{}
	for i, record := range depResult.Records {
		if i >= limit {
			break
		}
		values := record.AsMap()
		entry := map[string]any{
			"path":   stringValue(values["path"]),
			"rel":    stringValue(values["rel"]),
			"source": "graph",
			"role":   "dependent",
		}
		if pkg := stringValue(values["packageId"]); pkg != "" {
			entry["packageId"] = pkg
			if !seenPkg[pkg] {
				packages = append(packages, pkg)
				seenPkg[pkg] = true
			}
		}
		if label := stringValue(values["label"]); label != "" {
			entry["label"] = label
		}
		dependents = append(dependents, entry)
	}

	outResult, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (f:GraphNode:File {ripple: $ripple})
		WHERE f.sourceId IN $sourceIDs OR f.path IN $paths
		MATCH (f)-[r]->(other:GraphNode {ripple: $ripple})
		WHERE type(r) IN ['IMPORTS_FILE', 'RE_EXPORTS_FILE', 'DYNAMIC_IMPORTS_FILE']
			AND coalesce(r.confidence, 1.0) >= 0.5
		RETURN DISTINCT coalesce(other.path, other.filePath, other.sourceId) AS path,
			type(r) AS rel,
			coalesce(other.packageId, "") AS packageId
		ORDER BY path
		LIMIT $limit
	`, map[string]any{"ripple": s.Ripple, "paths": normalized, "sourceIDs": sourceIDs, "limit": limit + 1}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	dependencies := []map[string]any{}
	for i, record := range outResult.Records {
		if i >= limit {
			break
		}
		values := record.AsMap()
		entry := map[string]any{
			"path":   stringValue(values["path"]),
			"rel":    stringValue(values["rel"]),
			"source": "graph",
			"role":   "dependency",
		}
		if pkg := stringValue(values["packageId"]); pkg != "" {
			entry["packageId"] = pkg
		}
		dependencies = append(dependencies, entry)
	}

	return map[string]any{
		"seedFiles":      seedFiles,
		"dependents":     dependents,
		"dependencies":   dependencies,
		"packages":       packages,
		"graphAvailable": true,
		"truncated":      len(depResult.Records) > limit || len(outResult.Records) > limit,
		"returnedDependents": len(dependents),
		"returnedDependencies": len(dependencies),
	}, nil
}

func symbolCandidate(node map[string]any) map[string]any {
	path := firstString(node, "filePath", "path")
	out := map[string]any{
		"id":     firstString(node, "sourceId", "id"),
		"name":   firstString(node, "name"),
		"kind":   firstString(node, "kind"),
		"path":   path,
		"label":  firstString(node, "primaryLabel"),
	}
	if pkg := firstString(node, "packageId"); pkg != "" {
		out["packageId"] = pkg
	}
	if line, ok := node["startLine"]; ok && line != nil {
		out["startLine"] = line
	}
	if end, ok := node["endLine"]; ok && end != nil {
		out["endLine"] = end
	}
	if exported, ok := node["exported"].(bool); ok {
		out["exported"] = exported
	}
	return out
}

func matchesResolveFilters(candidate map[string]any, opts ResolveOptions) bool {
	path := firstString(candidate, "path")
	packageID := firstString(candidate, "packageId")
	if opts.PackageID != "" {
		want := opts.PackageID
		if !strings.HasPrefix(want, "package:") {
			want = "package:" + want
		}
		if packageID != want && packageID != opts.PackageID && !strings.EqualFold(packageID, want) {
			// also allow substring on package name
			if !strings.Contains(strings.ToLower(packageID), strings.ToLower(opts.PackageID)) {
				return false
			}
		}
	}
	if opts.Package != "" && !packageScopeMatch(packageID, path, opts.Package) {
		return false
	}
	if opts.PathPrefix != "" {
		prefix := strings.TrimSuffix(opts.PathPrefix, "/")
		if path != prefix && !strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}

// PackageScopeMatch reports whether a packageId/path belongs to a package filter.
func PackageScopeMatch(packageID string, path string, filter string) bool {
	return packageScopeMatch(packageID, path, filter)
}

func packageScopeMatch(packageID string, path string, filter string) bool {
	if filter == "" {
		return true
	}
	raw := strings.TrimSpace(filter)
	normalized := strings.TrimPrefix(raw, "package:")
	if normalized == "" {
		return true
	}
	lowerPkg := strings.ToLower(packageID)
	lowerFilter := strings.ToLower(normalized)
	if lowerPkg != "" {
		if lowerPkg == "package:"+lowerFilter || lowerPkg == lowerFilter || strings.Contains(lowerPkg, lowerFilter) {
			return true
		}
	}
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, strings.ToLower(raw)) || strings.Contains(lowerPath, lowerFilter) {
		return true
	}
	leaf := normalized
	if i := strings.LastIndex(normalized, "/"); i >= 0 {
		leaf = normalized[i+1:]
	}
	if leaf != "" {
		if strings.Contains(lowerPath, "/"+strings.ToLower(leaf)+"/") ||
			strings.HasPrefix(lowerPath, strings.ToLower(leaf)+"/") ||
			strings.Contains(lowerPath, "packages/"+strings.ToLower(leaf)+"/") ||
			strings.Contains(lowerPath, "apps/"+strings.ToLower(leaf)+"/") {
			return true
		}
	}
	return false
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func (s Service) FindFile(ctx context.Context, path string, opts Options) (map[string]any, error) {
	opts = normalize(opts)
	return queryNodes(ctx, s.Driver, `
		MATCH (n:GraphNode:File)
		WHERE n.ripple = $ripple AND toLower(coalesce(n.path, n.id)) CONTAINS toLower($path)
		RETURN n AS node
		ORDER BY n.path, n.id
		LIMIT $limit
	`, map[string]any{"path": path, "ripple": s.Ripple, "limit": opts.Limit + 1}, opts.Limit)
}

func (s Service) Node(ctx context.Context, id string) (map[string]any, error) {
	result, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (n:GraphNode {ripple: $ripple})
		WHERE n.id = $id OR n.sourceId = $sourceId
		RETURN n AS node
		LIMIT 1
	`, map[string]any{"id": s.scopedID(id), "sourceId": unscopedID(s.Ripple, id), "ripple": s.Ripple}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	if len(result.Records) == 0 {
		return nil, fmt.Errorf("node %q not found", id)
	}
	return nodeMap(result.Records[0].AsMap()["node"].(neo4j.Node)), nil
}

func (s Service) Relations(ctx context.Context, targetID string, opts Options) (map[string]any, error) {
	opts = normalize(opts)
	pattern := `(start)-[r*1..%d]-(n)`
	if opts.Direction == "forward" {
		pattern = `(start)-[r*1..%d]->(n)`
	}
	if opts.Direction == "reverse" {
		pattern = `(start)<-[r*1..%d]-(n)`
	}
	query := fmt.Sprintf(`
		MATCH (start:GraphNode {id: $id, ripple: $ripple})
		MATCH path = %s
		WHERE all(node IN nodes(path) WHERE node.ripple = $ripple)
			AND all(rel IN relationships(path) WHERE rel.ripple = $ripple AND coalesce(rel.confidence, 1.0) >= $minConfidence)
		RETURN nodes(path) AS nodes, relationships(path) AS relationships
		LIMIT $limit
	`, fmt.Sprintf(pattern, opts.Depth))
	return queryPathsAsSlice(ctx, s.Driver, query, map[string]any{
		"id": s.scopedID(targetID), "ripple": s.Ripple, "limit": opts.Limit + 1, "minConfidence": opts.MinConfidence,
	}, opts.Limit)
}

func (s Service) Paths(ctx context.Context, fromID string, toID string, opts Options) (map[string]any, error) {
	opts = normalize(opts)
	query := fmt.Sprintf(`
		MATCH (from:GraphNode {id: $from, ripple: $ripple}), (to:GraphNode {id: $to, ripple: $ripple})
		MATCH path = shortestPath((from)-[*1..%d]-(to))
		WHERE all(node IN nodes(path) WHERE node.ripple = $ripple)
			AND all(rel IN relationships(path) WHERE rel.ripple = $ripple AND coalesce(rel.confidence, 1.0) >= $minConfidence)
		RETURN nodes(path) AS nodes, relationships(path) AS relationships
		LIMIT $limit
	`, opts.Depth)
	return queryPathResults(ctx, s.Driver, query, map[string]any{
		"from": s.scopedID(fromID), "to": s.scopedID(toID), "ripple": s.Ripple, "limit": opts.Limit + 1, "minConfidence": opts.MinConfidence,
	}, opts.Limit)
}

func (s Service) FileRelationSummary(ctx context.Context, paths []string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 5
	}
	if len(paths) == 0 {
		return map[string]any{"files": []map[string]any{}}, nil
	}
	sourceIDs := make([]string, 0, len(paths))
	for _, path := range paths {
		sourceIDs = append(sourceIDs, "file:"+path)
	}
	result, err := neo4j.ExecuteQuery(ctx, s.Driver, `
		MATCH (f:GraphNode:File {ripple: $ripple})
		WHERE f.sourceId IN $sourceIDs OR f.path IN $paths
		CALL {
			WITH f
			OPTIONAL MATCH (incoming:GraphNode)-[r]->(f)
			WHERE incoming.ripple = $ripple AND r.ripple = $ripple
			RETURN count(DISTINCT incoming) AS inboundCount,
				collect(DISTINCT coalesce(incoming.path, incoming.filePath, incoming.name, incoming.sourceId))[0..$limit] AS inboundExamples
		}
		CALL {
			WITH f
			OPTIONAL MATCH (f)-[r]->(outgoing:GraphNode)
			WHERE outgoing.ripple = $ripple AND r.ripple = $ripple
			RETURN count(DISTINCT outgoing) AS outboundCount,
				collect(DISTINCT coalesce(outgoing.path, outgoing.filePath, outgoing.name, outgoing.sourceId))[0..$limit] AS outboundExamples
		}
		RETURN f.path AS path, inboundCount, inboundExamples, outboundCount, outboundExamples
		ORDER BY f.path
	`, map[string]any{"ripple": s.Ripple, "paths": paths, "sourceIDs": sourceIDs, "limit": limit}, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	files := []map[string]any{}
	for _, record := range result.Records {
		values := record.AsMap()
		files = append(files, map[string]any{
			"path":             values["path"],
			"inboundCount":     values["inboundCount"],
			"inboundExamples":  values["inboundExamples"],
			"outboundCount":    values["outboundCount"],
			"outboundExamples": values["outboundExamples"],
		})
	}
	return map[string]any{"files": files, "returned": len(files)}, nil
}

func (s Service) scopedID(id string) string {
	if s.Ripple == "" || strings.HasPrefix(id, s.Ripple+":") {
		return id
	}
	return s.Ripple + ":" + id
}

func unscopedID(ripple string, id string) string {
	if ripple == "" {
		return id
	}
	return strings.TrimPrefix(id, ripple+":")
}

func normalize(opts Options) Options {
	if opts.Depth <= 0 {
		opts.Depth = 1
	}
	if opts.Depth > 8 {
		opts.Depth = 8
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 200 {
		opts.Limit = 200
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = 0.6
	}
	if opts.Direction == "" {
		opts.Direction = "both"
	}
	return opts
}

func queryNodes(ctx context.Context, driver neo4j.DriverWithContext, query string, params map[string]any, limit int) (map[string]any, error) {
	result, err := neo4j.ExecuteQuery(ctx, driver, query, params, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	nodes := []map[string]any{}
	for i, record := range result.Records {
		if i >= limit {
			break
		}
		nodes = append(nodes, nodeMapLean(record.AsMap()["node"].(neo4j.Node)))
	}
	return map[string]any{"nodes": nodes, "returned": len(nodes), "totalKnown": len(result.Records), "truncated": len(result.Records) > limit}, nil
}

func queryPathsAsSlice(ctx context.Context, driver neo4j.DriverWithContext, query string, params map[string]any, limit int) (map[string]any, error) {
	result, err := neo4j.ExecuteQuery(ctx, driver, query, params, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	nodeByID := map[string]map[string]any{}
	relByID := map[string]map[string]any{}
	for i, record := range result.Records {
		if i >= limit {
			break
		}
		values := record.AsMap()
		for _, raw := range values["nodes"].([]any) {
			node := raw.(neo4j.Node)
			nodeByID[node.ElementId] = nodeMapLean(node)
		}
		for _, raw := range values["relationships"].([]any) {
			rel := raw.(neo4j.Relationship)
			relByID[rel.ElementId] = relMapLean(rel)
		}
	}
	nodes := []map[string]any{}
	for _, node := range nodeByID {
		nodes = append(nodes, node)
	}
	rels := []map[string]any{}
	for _, rel := range relByID {
		rels = append(rels, rel)
	}
	return map[string]any{"nodes": nodes, "relationships": rels, "returned": len(rels), "totalKnown": len(result.Records), "truncated": len(result.Records) > limit}, nil
}

func queryPathResults(ctx context.Context, driver neo4j.DriverWithContext, query string, params map[string]any, limit int) (map[string]any, error) {
	result, err := neo4j.ExecuteQuery(ctx, driver, query, params, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	paths := []map[string]any{}
	for i, record := range result.Records {
		if i >= limit {
			break
		}
		values := record.AsMap()
		nodes := []map[string]any{}
		for _, raw := range values["nodes"].([]any) {
			nodes = append(nodes, nodeMapLean(raw.(neo4j.Node)))
		}
		rels := []map[string]any{}
		for _, raw := range values["relationships"].([]any) {
			rels = append(rels, relMapLean(raw.(neo4j.Relationship)))
		}
		paths = append(paths, map[string]any{"nodes": nodes, "relationships": rels})
	}
	return map[string]any{"paths": paths, "returned": len(paths), "totalKnown": len(result.Records), "truncated": len(result.Records) > limit}, nil
}

// nodeMap returns full node properties. Used when opening a symbol body needs start/end lines.
func nodeMap(node neo4j.Node) map[string]any {
	out := map[string]any{}
	for key, value := range node.Props {
		out[key] = value
	}
	out["labels"] = node.Labels
	return out
}

// nodeMapLean projects only agent-useful fields for search/relation responses.
func nodeMapLean(node neo4j.Node) map[string]any {
	props := node.Props
	out := map[string]any{}
	if sourceID, ok := props["sourceId"]; ok {
		out["sourceId"] = sourceID
	} else if id, ok := props["id"]; ok {
		out["sourceId"] = id
	}
	if label, ok := props["primaryLabel"]; ok {
		out["primaryLabel"] = label
	} else if len(node.Labels) > 0 {
		// Prefer the most specific non-GraphNode label.
		for _, label := range node.Labels {
			if label != "GraphNode" {
				out["primaryLabel"] = label
				break
			}
		}
	}
	for _, key := range []string{"name", "kind", "packageId"} {
		if value, ok := props[key]; ok && value != nil && value != "" {
			out[key] = value
		}
	}
	path, _ := props["path"].(string)
	if path == "" {
		path, _ = props["filePath"].(string)
	}
	if path != "" {
		out["path"] = path
		out["filePath"] = path
	}
	if startLine, ok := props["startLine"]; ok && startLine != nil {
		out["startLine"] = startLine
	}
	return out
}

func relMapLean(rel neo4j.Relationship) map[string]any {
	props := rel.Props
	out := map[string]any{"type": rel.Type}
	for _, key := range []string{"sourceFile", "startLine", "endLine", "confidence", "from", "to"} {
		if value, ok := props[key]; ok && value != nil {
			out[key] = value
		}
	}
	// Prefer stable source ids from props when extractors set them.
	if from, ok := props["sourceId"]; ok {
		out["from"] = from
	}
	if to, ok := props["targetId"]; ok {
		out["to"] = to
	}
	if out["from"] == nil {
		out["startId"] = rel.StartElementId
	}
	if out["to"] == nil {
		out["endId"] = rel.EndElementId
	}
	return out
}

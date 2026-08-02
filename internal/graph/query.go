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

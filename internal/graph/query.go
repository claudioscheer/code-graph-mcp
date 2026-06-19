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
		RETURN nodes, relationships, ripple.repo AS repo, ripple.language AS language, ripple.createdAt AS createdAt, ripple.updatedAt AS updatedAt
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

func (s Service) scopedID(id string) string {
	if s.Ripple == "" || strings.HasPrefix(id, s.Ripple+":") {
		return id
	}
	return s.Ripple + ":" + id
}

func normalize(opts Options) Options {
	if opts.Depth <= 0 {
		opts.Depth = 2
	}
	if opts.Depth > 8 {
		opts.Depth = 8
	}
	if opts.Limit <= 0 {
		opts.Limit = 100
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
		nodes = append(nodes, nodeMap(record.AsMap()["node"].(neo4j.Node)))
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
			nodeByID[node.ElementId] = nodeMap(node)
		}
		for _, raw := range values["relationships"].([]any) {
			rel := raw.(neo4j.Relationship)
			relByID[rel.ElementId] = relMap(rel)
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
			nodes = append(nodes, nodeMap(raw.(neo4j.Node)))
		}
		rels := []map[string]any{}
		for _, raw := range values["relationships"].([]any) {
			rels = append(rels, relMap(raw.(neo4j.Relationship)))
		}
		paths = append(paths, map[string]any{"nodes": nodes, "relationships": rels})
	}
	return map[string]any{"paths": paths, "returned": len(paths), "totalKnown": len(result.Records), "truncated": len(result.Records) > limit}, nil
}

func nodeMap(node neo4j.Node) map[string]any {
	out := map[string]any{}
	for key, value := range node.Props {
		out[key] = value
	}
	out["labels"] = node.Labels
	return out
}

func relMap(rel neo4j.Relationship) map[string]any {
	out := map[string]any{}
	for key, value := range rel.Props {
		out[key] = value
	}
	out["type"] = rel.Type
	out["startId"] = rel.StartElementId
	out["endId"] = rel.EndElementId
	return out
}

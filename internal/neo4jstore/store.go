package neo4jstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/claudioscheer/code-graph-mcp/internal/events"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Store struct {
	driver neo4j.DriverWithContext
}

func New(uri string, user string, password string) (*Store, error) {
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""))
	if err != nil {
		return nil, err
	}
	return &Store{driver: driver}, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.driver.Close(ctx)
}

func (s *Store) Driver() neo4j.DriverWithContext {
	return s.driver
}

func (s *Store) CreateConstraints(ctx context.Context) error {
	statements := []string{
		`CREATE CONSTRAINT graph_node_id IF NOT EXISTS FOR (n:GraphNode) REQUIRE n.id IS UNIQUE`,
		`CREATE INDEX graph_node_label IF NOT EXISTS FOR (n:GraphNode) ON (n.primaryLabel)`,
		`CREATE INDEX graph_node_path IF NOT EXISTS FOR (n:GraphNode) ON (n.path)`,
		`CREATE INDEX graph_node_name IF NOT EXISTS FOR (n:GraphNode) ON (n.name)`,
	}
	for _, statement := range statements {
		if _, err := neo4j.ExecuteQuery(ctx, s.driver, statement, nil, neo4j.EagerResultTransformer); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Reset(ctx context.Context) error {
	if _, err := neo4j.ExecuteQuery(ctx, s.driver, `MATCH (n) DETACH DELETE n`, nil, neo4j.EagerResultTransformer); err != nil {
		return err
	}
	return s.CreateConstraints(ctx)
}

func (s *Store) Emit(ctx context.Context, event events.GraphEvent) error {
	return s.ApplyEvent(ctx, event)
}

func (s *Store) Flush(ctx context.Context) error {
	return nil
}

func (s *Store) ApplyEvent(ctx context.Context, event events.GraphEvent) error {
	if err := events.Validate(event); err != nil {
		return err
	}
	switch event.Type {
	case events.EventNode:
		return s.applyNode(ctx, event)
	case events.EventEdge:
		return s.applyEdge(ctx, event)
	case events.EventWarning, events.EventSummary:
		return s.applyMeta(ctx, event)
	default:
		return nil
	}
}

func (s *Store) applyNode(ctx context.Context, event events.GraphEvent) error {
	label := event.Label
	if !safeName(label) {
		return fmt.Errorf("unsafe label %q", label)
	}
	props := copyProps(event.Props)
	props["id"] = event.ID
	props["primaryLabel"] = event.Label
	query := fmt.Sprintf(`MERGE (n:GraphNode:%s {id: $id}) SET n += $props`, label)
	_, err := neo4j.ExecuteQuery(ctx, s.driver, query, map[string]any{"id": event.ID, "props": props}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) applyEdge(ctx context.Context, event events.GraphEvent) error {
	if !safeName(event.Rel) {
		return fmt.Errorf("unsafe relationship %q", event.Rel)
	}
	props := copyProps(event.Props)
	props["type"] = event.Rel
	query := fmt.Sprintf(`
		MATCH (from:GraphNode {id: $from})
		MATCH (to:GraphNode {id: $to})
		MERGE (from)-[r:%s]->(to)
		SET r += $props
	`, event.Rel)
	_, err := neo4j.ExecuteQuery(ctx, s.driver, query, map[string]any{"from": event.From, "to": event.To, "props": props}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) applyMeta(ctx context.Context, event events.GraphEvent) error {
	props := copyProps(event.Props)
	props["protocol"] = event.Protocol
	props["type"] = string(event.Type)
	props["source"] = event.Source
	props["message"] = event.Message
	id := fmt.Sprintf("meta:%s:%s:%v", event.Source, event.Type, props["durationMs"])
	_, err := neo4j.ExecuteQuery(ctx, s.driver, `MERGE (n:GraphNode:Meta {id: $id}) SET n += $props`, map[string]any{"id": id, "props": props}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) Status(ctx context.Context) (map[string]any, error) {
	result, err := neo4j.ExecuteQuery(ctx, s.driver, `
		CALL {
			MATCH (n:GraphNode)
			RETURN count(n) AS nodes
		}
		CALL {
			MATCH ()-[r]->()
			RETURN count(r) AS relationships
		}
		RETURN nodes, relationships
	`, nil, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	if len(result.Records) == 0 {
		return map[string]any{"nodes": 0, "relationships": 0}, nil
	}
	return result.Records[0].AsMap(), nil
}

func copyProps(props map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range props {
		out[key] = value
	}
	return out
}

func safeName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'A' && char <= 'Z') && !(char >= 'a' && char <= 'z') && char != '_' {
			return false
		}
	}
	return !strings.Contains(value, "__")
}

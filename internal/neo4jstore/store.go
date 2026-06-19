package neo4jstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/claudioscheer/code-graph-mcp/internal/events"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Store struct {
	driver neo4j.DriverWithContext
	ripple string
}

type Ripple struct {
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	Language  string `json:"language"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
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

func (s *Store) ForRipple(name string) *Store {
	return &Store{driver: s.driver, ripple: name}
}

func (s *Store) CreateConstraints(ctx context.Context) error {
	legacyConstraints := []string{
		"config_key",
		"external_package_name",
		"file_path",
		"package_id",
		"route_id",
		"symbol_id",
		"test_id",
	}
	for _, name := range legacyConstraints {
		if _, err := neo4j.ExecuteQuery(ctx, s.driver, `DROP CONSTRAINT `+name+` IF EXISTS`, nil, neo4j.EagerResultTransformer); err != nil {
			return err
		}
	}
	statements := []string{
		`CREATE CONSTRAINT graph_node_id IF NOT EXISTS FOR (n:GraphNode) REQUIRE n.id IS UNIQUE`,
		`CREATE CONSTRAINT ripple_name IF NOT EXISTS FOR (r:Ripple) REQUIRE r.name IS UNIQUE`,
		`CREATE INDEX graph_node_ripple IF NOT EXISTS FOR (n:GraphNode) ON (n.ripple)`,
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

func (s *Store) ResetRipple(ctx context.Context, ripple string) error {
	if ripple == "" {
		return fmt.Errorf("ripple is required")
	}
	_, err := neo4j.ExecuteQuery(ctx, s.driver, `
		MATCH (n:GraphNode {ripple: $ripple})
		DETACH DELETE n
	`, map[string]any{"ripple": ripple}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) SaveRipple(ctx context.Context, ripple Ripple) error {
	if ripple.Name == "" {
		return fmt.Errorf("ripple is required")
	}
	now := time.Now().Format(time.RFC3339)
	_, err := neo4j.ExecuteQuery(ctx, s.driver, `
		MERGE (r:Ripple {name: $name})
		ON CREATE SET r.createdAt = $now
		SET r.repo = $repo,
			r.language = $language,
			r.updatedAt = $now
	`, map[string]any{"name": ripple.Name, "repo": ripple.Repo, "language": ripple.Language, "now": now}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) GetRipple(ctx context.Context, name string) (Ripple, error) {
	result, err := neo4j.ExecuteQuery(ctx, s.driver, `
		MATCH (r:Ripple {name: $name})
		RETURN r.name AS name, r.repo AS repo, r.language AS language, r.createdAt AS createdAt, r.updatedAt AS updatedAt
	`, map[string]any{"name": name}, neo4j.EagerResultTransformer)
	if err != nil {
		return Ripple{}, err
	}
	if len(result.Records) == 0 {
		return Ripple{}, fmt.Errorf("ripple %q not found", name)
	}
	values := result.Records[0].AsMap()
	return Ripple{
		Name:      stringValue(values["name"]),
		Repo:      stringValue(values["repo"]),
		Language:  stringValue(values["language"]),
		CreatedAt: stringValue(values["createdAt"]),
		UpdatedAt: stringValue(values["updatedAt"]),
	}, nil
}

func (s *Store) ListRipples(ctx context.Context) ([]Ripple, error) {
	result, err := neo4j.ExecuteQuery(ctx, s.driver, `
		MATCH (r:Ripple)
		RETURN r.name AS name, r.repo AS repo, r.language AS language, r.createdAt AS createdAt, r.updatedAt AS updatedAt
		ORDER BY r.name
	`, nil, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	ripples := []Ripple{}
	for _, record := range result.Records {
		values := record.AsMap()
		ripples = append(ripples, Ripple{
			Name:      stringValue(values["name"]),
			Repo:      stringValue(values["repo"]),
			Language:  stringValue(values["language"]),
			CreatedAt: stringValue(values["createdAt"]),
			UpdatedAt: stringValue(values["updatedAt"]),
		})
	}
	return ripples, nil
}

func (s *Store) Emit(ctx context.Context, event events.GraphEvent) error {
	return s.ApplyEvent(ctx, event)
}

func (s *Store) Flush(ctx context.Context) error {
	return nil
}

func (s *Store) ApplyEvent(ctx context.Context, event events.GraphEvent) error {
	if s.ripple == "" {
		return fmt.Errorf("ripple is required")
	}
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
	props["id"] = scopedID(s.ripple, event.ID)
	props["sourceId"] = event.ID
	props["ripple"] = s.ripple
	props["primaryLabel"] = event.Label
	query := fmt.Sprintf(`MERGE (n:GraphNode:%s {id: $id}) SET n += $props`, label)
	_, err := neo4j.ExecuteQuery(ctx, s.driver, query, map[string]any{"id": scopedID(s.ripple, event.ID), "props": props}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) applyEdge(ctx context.Context, event events.GraphEvent) error {
	if !safeName(event.Rel) {
		return fmt.Errorf("unsafe relationship %q", event.Rel)
	}
	props := copyProps(event.Props)
	props["type"] = event.Rel
	props["ripple"] = s.ripple
	props["sourceFrom"] = event.From
	props["sourceTo"] = event.To
	query := fmt.Sprintf(`
		MATCH (from:GraphNode {id: $from})
		MATCH (to:GraphNode {id: $to})
		MERGE (from)-[r:%s]->(to)
		SET r += $props
	`, event.Rel)
	_, err := neo4j.ExecuteQuery(ctx, s.driver, query, map[string]any{"from": scopedID(s.ripple, event.From), "to": scopedID(s.ripple, event.To), "props": props}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) applyMeta(ctx context.Context, event events.GraphEvent) error {
	props := copyProps(event.Props)
	props["protocol"] = event.Protocol
	props["type"] = string(event.Type)
	props["source"] = event.Source
	props["message"] = event.Message
	props["ripple"] = s.ripple
	sourceID := fmt.Sprintf("meta:%s:%s:%v", event.Source, event.Type, props["durationMs"])
	id := scopedID(s.ripple, sourceID)
	props["id"] = id
	props["sourceId"] = sourceID
	_, err := neo4j.ExecuteQuery(ctx, s.driver, `MERGE (n:GraphNode:Meta {id: $id}) SET n += $props`, map[string]any{"id": id, "props": props}, neo4j.EagerResultTransformer)
	return err
}

func (s *Store) Status(ctx context.Context, ripple string) (map[string]any, error) {
	where := ""
	params := map[string]any{}
	if ripple != "" {
		where = "{ripple: $ripple}"
		params["ripple"] = ripple
	}
	result, err := neo4j.ExecuteQuery(ctx, s.driver, `
		CALL {
			MATCH (n:GraphNode `+where+`)
			RETURN count(n) AS nodes
		}
		CALL {
			MATCH ()-[r]->()
			WHERE $ripple IS NULL OR r.ripple = $ripple
			RETURN count(r) AS relationships
		}
		RETURN nodes, relationships
	`, paramsWithNilRipple(params), neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	if len(result.Records) == 0 {
		return map[string]any{"nodes": 0, "relationships": 0}, nil
	}
	return result.Records[0].AsMap(), nil
}

func scopedID(ripple string, id string) string {
	if strings.HasPrefix(id, ripple+":") {
		return id
	}
	return ripple + ":" + id
}

func paramsWithNilRipple(params map[string]any) map[string]any {
	if _, ok := params["ripple"]; !ok {
		params["ripple"] = nil
	}
	return params
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
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

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	case "search_code":
		result, err = s.Query.Search(ctx, firstStringArg(args, "query", "q", "text", "term", "name", "path"), opts)
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

func errorResponse(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func tools() []map[string]any {
	return []map[string]any{
		tool("search_code", "Search all indexed code graph nodes in the current ripple. Use this first for broad terms like worker, auth, route, package names, or paths.", map[string]any{
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

func floatArg(args map[string]any, key string, fallback float64) float64 {
	if value, ok := args[key].(float64); ok {
		return value
	}
	return fallback
}

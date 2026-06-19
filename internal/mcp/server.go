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
	case "find_symbol":
		result, err = s.Query.FindSymbol(ctx, stringArg(args, "name"), opts)
	case "find_file":
		result, err = s.Query.FindFile(ctx, stringArg(args, "path"), opts)
	case "get_dependencies":
		opts.Direction = "forward"
		result, err = s.Query.Relations(ctx, stringArg(args, "targetId"), opts)
	case "get_dependents":
		opts.Direction = "reverse"
		result, err = s.Query.Relations(ctx, stringArg(args, "targetId"), opts)
	case "get_relations":
		opts.Direction = stringArgDefault(args, "direction", "both")
		result, err = s.Query.Relations(ctx, stringArg(args, "targetId"), opts)
	case "get_impact", "get_route_impact", "get_related_tests":
		opts.Direction = "reverse"
		result, err = s.Query.Relations(ctx, stringArg(args, "targetId"), opts)
	case "find_paths":
		result, err = s.Query.Paths(ctx, stringArg(args, "fromId"), stringArg(args, "toId"), opts)
	case "open_file_excerpt":
		result, err = s.openFile(stringArg(args, "path"), intArg(args, "startLine", 1), intArg(args, "endLine", 80))
	case "open_symbol_body":
		symbolID := stringArg(args, "symbolId")
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
	names := []string{"find_symbol", "find_file", "get_dependencies", "get_dependents", "get_relations", "get_impact", "get_route_impact", "get_related_tests", "find_paths", "open_symbol_body", "open_file_excerpt"}
	out := []map[string]any{}
	for _, name := range names {
		out = append(out, map[string]any{"name": name, "description": "Code graph tool: " + name, "inputSchema": map[string]any{"type": "object", "additionalProperties": true}})
	}
	return out
}

func stringArg(args map[string]any, key string) string {
	if value, ok := args[key].(string); ok {
		return value
	}
	return ""
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

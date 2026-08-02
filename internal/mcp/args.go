package mcp

import "strings"

// stringSliceArg reads string arrays from tool args (JSON arrays, single string, or comma-separated).
func stringSliceArg(args map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return typed
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			return out
		case string:
			text := strings.TrimSpace(typed)
			if text == "" {
				continue
			}
			if strings.Contains(text, ",") {
				parts := strings.Split(text, ",")
				out := make([]string, 0, len(parts))
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if part != "" {
						out = append(out, part)
					}
				}
				return out
			}
			return []string{text}
		}
	}
	return nil
}

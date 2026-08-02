package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func formatToolResult(tool string, result any, args map[string]any) (string, error) {
	if wantsJSON(args) {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return compactToolResult(tool, result), nil
}

func wantsJSON(args map[string]any) bool {
	if boolArg(args, "raw", false) || boolArg(args, "json", false) {
		return true
	}
	return strings.EqualFold(firstStringArg(args, "format", "output"), "json")
}

func compactToolResult(tool string, result any) string {
	values, ok := result.(map[string]any)
	if !ok {
		return compactValue(result)
	}
	if text := compactGraphResult(tool, values); text != "" {
		return text
	}
	var b strings.Builder
	header := compactHeader(tool, values)
	if header != "" {
		b.WriteString(header)
		b.WriteByte('\n')
	}
	writeMapSections(&b, values, 0)
	return strings.TrimSpace(b.String())
}

func compactHeader(tool string, values map[string]any) string {
	parts := []string{tool}
	for _, key := range []string{"query", "symbol", "oldName", "callee", "envName", "ripple"} {
		if value := firstAnyString(values, key); value != "" {
			parts = append(parts, quoteIfNeeded(value))
			break
		}
	}
	for _, key := range []string{"returned", "uniqueFiles", "totalMatches", "totalHits", "runtimeReadCount", "nodes", "relationships"} {
		if value, ok := values[key]; ok {
			if isScalar(value) {
				parts = append(parts, fmt.Sprintf("%s=%v", key, value))
			}
		}
	}
	if value, ok := values["truncated"]; ok {
		parts = append(parts, fmt.Sprintf("truncated=%v", value))
	}
	return strings.Join(parts, " ")
}

func compactGraphResult(tool string, values map[string]any) string {
	if rawNodes, ok := values["nodes"].([]map[string]any); ok {
		var b strings.Builder
		b.WriteString(compactHeader(tool, values))
		b.WriteByte('\n')
		writeNodeList(&b, "nodes", rawNodes, 0)
		if rawRels, ok := values["relationships"].([]map[string]any); ok {
			writeRelationList(&b, "relationships", rawRels, 0)
		}
		return strings.TrimSpace(b.String())
	}
	if rawPaths, ok := values["paths"].([]map[string]any); ok {
		var b strings.Builder
		b.WriteString(compactHeader(tool, values))
		b.WriteByte('\n')
		for index, path := range rawPaths {
			b.WriteString(fmt.Sprintf("%d. path\n", index+1))
			if nodes, ok := path["nodes"].([]map[string]any); ok {
				writeNodeList(&b, "nodes", nodes, 2)
			}
			if rels, ok := path["relationships"].([]map[string]any); ok {
				writeRelationList(&b, "relationships", rels, 2)
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func writeMapSections(b *strings.Builder, values map[string]any, indent int) {
	scalars := scalarPairs(values, "code", "text", "files", "nodes", "relationships", "paths")
	if len(scalars) > 0 {
		b.WriteString(indentString(indent))
		b.WriteString(strings.Join(scalars, " "))
		b.WriteByte('\n')
	}
	for _, key := range []string{"text", "code"} {
		if value := firstAnyString(values, key); value != "" {
			writeTextBlock(b, key, value, indent)
		}
	}
	keys := sortedKeys(values)
	for _, key := range keys {
		if isScalar(values[key]) || key == "code" || key == "text" {
			continue
		}
		switch value := values[key].(type) {
		case []string:
			writeStringList(b, key, value, indent)
		case []map[string]any:
			writeMapList(b, key, value, indent)
		case map[string]any:
			if len(value) == 0 {
				continue
			}
			b.WriteString(indentString(indent))
			b.WriteString(key)
			b.WriteString(":\n")
			writeMapSections(b, value, indent+2)
		default:
			if text := compactValue(value); text != "" {
				b.WriteString(indentString(indent))
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
	}
}

func writeMapList(b *strings.Builder, name string, items []map[string]any, indent int) {
	if len(items) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(items)))
	for index, item := range items {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("- %s\n", compactMapItem(index+1, item)))
	}
}

func writeNodeList(b *strings.Builder, name string, nodes []map[string]any, indent int) {
	if len(nodes) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(nodes)))
	for index, node := range nodes {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("- %d. %s\n", index+1, compactNode(node)))
	}
}

func writeRelationList(b *strings.Builder, name string, rels []map[string]any, indent int) {
	if len(rels) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(rels)))
	for index, rel := range rels {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("- %d. %s\n", index+1, compactRelation(rel)))
	}
}

func writeStringList(b *strings.Builder, name string, values []string, indent int) {
	if len(values) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(values)))
	for _, value := range values {
		b.WriteString(indentString(indent))
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
}

func compactMapItem(index int, item map[string]any) string {
	main := firstAnyString(item, "path", "filePath", "id", "sourceId", "name", "symbol", "tool", "task", "label", "type")
	if main == "" {
		main = fmt.Sprintf("%d", index)
	}
	parts := []string{main}
	if line := item["line"]; line != nil {
		parts = append(parts, fmt.Sprintf("line=%v", line))
	}
	for _, key := range []string{"category", "role", "kind", "count", "hitCount", "matchCount", "readCount", "owner", "reason", "truncated"} {
		if value, ok := item[key]; ok && isScalar(value) {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	if snippet := firstAnyString(item, "snippet", "text"); snippet != "" {
		parts = append(parts, "snippet="+compactLine(snippet, 140))
	}
	return strings.Join(parts, " ")
}

func writeTextBlock(b *strings.Builder, name string, text string, indent int) {
	b.WriteString(indentString(indent))
	b.WriteString(name)
	b.WriteString(":\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(indentString(indent + 2))
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func compactNode(node map[string]any) string {
	id := firstAnyString(node, "sourceId", "id")
	label := firstAnyString(node, "primaryLabel", "label")
	path := firstAnyString(node, "path", "filePath")
	name := firstAnyString(node, "name")
	kind := firstAnyString(node, "kind")
	line := node["startLine"]
	parts := []string{}
	if label != "" {
		parts = append(parts, label)
	}
	if name != "" {
		parts = append(parts, name)
	}
	if kind != "" {
		parts = append(parts, kind)
	}
	if path != "" {
		if line != nil {
			path = fmt.Sprintf("%s:%v", path, line)
		}
		parts = append(parts, path)
	}
	if id != "" {
		parts = append(parts, "id="+id)
	}
	return strings.Join(parts, " ")
}

func compactRelation(rel map[string]any) string {
	relType := firstAnyString(rel, "type", "rel")
	source := firstAnyString(rel, "sourceFile")
	if source != "" && rel["startLine"] != nil {
		source = fmt.Sprintf("%s:%v", source, rel["startLine"])
	}
	parts := []string{}
	if relType != "" {
		parts = append(parts, relType)
	}
	for _, key := range []string{"from", "to", "startId", "endId"} {
		if value := firstAnyString(rel, key); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if source != "" {
		parts = append(parts, "at="+source)
	}
	return strings.Join(parts, " ")
}

func scalarPairs(values map[string]any, exclude ...string) []string {
	excluded := map[string]bool{}
	for _, key := range exclude {
		excluded[key] = true
	}
	keys := sortedKeys(values)
	pairs := []string{}
	for _, key := range keys {
		if excluded[key] || !isScalar(values[key]) {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%v", key, values[key]))
	}
	return pairs
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, int, int64, float64:
		return true
	default:
		return false
	}
}

func compactValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return compactLine(typed, 240)
	case []string:
		return strings.Join(typed, ", ")
	case map[string]any:
		return strings.Join(scalarPairs(typed), " ")
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func compactLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func quoteIfNeeded(value string) string {
	if strings.ContainsAny(value, " \t\n") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func indentString(indent int) string {
	return strings.Repeat(" ", indent)
}

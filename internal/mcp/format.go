package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const defaultMaxItems = 20

// detail levels control how much structure high-level tools return and how
// aggressively lists are sampled.
//
//	summary (default) — counts + small samples
//	files             — fuller file lists, still hard-capped
//	lines             — includes line matches / snippets when present
//	raw               — maximum structure for the tool (still may sample huge lists)
//
// Note: raw=true / format=json only control serialization (JSON vs compact text).
// They must NOT force detail=raw, or agents lose summary sampling rules and
// mis-count sample arrays as totals.
func detailArg(args map[string]any) string {
	detail := strings.ToLower(firstStringArg(args, "detail", "view"))
	switch detail {
	case "files", "file", "lines", "line", "full", "raw", "json":
		if detail == "file" {
			return "files"
		}
		if detail == "line" || detail == "full" {
			return "lines"
		}
		if detail == "json" {
			return "raw"
		}
		return detail
	default:
		return "summary"
	}
}

func maxItemsArg(args map[string]any, fallback int) int {
	if fallback <= 0 {
		fallback = defaultMaxItems
	}
	value := intArg(args, "maxItems", fallback)
	if value <= 0 {
		return fallback
	}
	if value > 200 {
		return 200
	}
	return value
}

func formatToolResult(tool string, result any, args map[string]any) (string, error) {
	// JSON when explicitly requested. detail=raw alone stays compact unless format=json/raw=true,
	// so agents can request deep structure as compact text without flipping serializers.
	if wantsJSON(args) {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return compactToolResult(tool, result, maxItemsArg(args, defaultMaxItems)), nil
}

func wantsJSON(args map[string]any) bool {
	if boolArg(args, "raw", false) || boolArg(args, "json", false) {
		return true
	}
	return strings.EqualFold(firstStringArg(args, "format", "output"), "json")
}

func compactToolResult(tool string, result any, maxItems int) string {
	values, ok := result.(map[string]any)
	if !ok {
		return compactValue(result)
	}
	if text := compactGraphResult(tool, values, maxItems); text != "" {
		return text
	}
	var b strings.Builder
	header := compactHeader(tool, values)
	if header != "" {
		b.WriteString(header)
		b.WriteByte('\n')
	}
	writeMapSections(&b, values, 0, maxItems, headerKeys(values))
	return strings.TrimSpace(b.String())
}

func headerKeys(values map[string]any) map[string]bool {
	keys := map[string]bool{}
	for _, key := range []string{"query", "symbol", "oldName", "callee", "envName", "ripple", "requiredBeforeCall", "planKind", "status"} {
		if firstAnyString(values, key) != "" {
			keys[key] = true
		}
	}
	for _, key := range []string{
		"returned", "uniqueFiles", "totalMatches", "totalHits", "runtimeReadCount",
		"totalCallSites", "missingCallSites", "satisfiedCallSites", "unownedCallSites",
		"truncated", "contextCompleteForPlanning", "resolutionMethod", "confidence",
		"needsDisambiguation", "graphReliable", "stale", "hasCallGraph", "dirtyFileCount",
		"exactCount", "ambiguous", "seedCount", "dependentCount", "relatedTestCount",
		"packageCount", "symbolCount", "seedPathCount", "success", "incremental",
		"scanTruncated", "listTruncated", "complete", "mustEditCount", "mustNotTouchCount",
		"identityCount", "totalMatchesApprox", "mustEditSampleCap", "mustEditIsSample",
	} {
		if value, ok := values[key]; ok && isScalar(value) {
			keys[key] = true
		}
	}
	return keys
}

func compactHeader(tool string, values map[string]any) string {
	parts := []string{tool}
	for _, key := range []string{"query", "symbol", "oldName", "callee", "envName", "ripple", "planKind", "status"} {
		if value := firstAnyString(values, key); value != "" {
			parts = append(parts, quoteIfNeeded(value))
			break
		}
	}
	if required := firstAnyString(values, "requiredBeforeCall"); required != "" {
		parts = append(parts, "requires="+quoteIfNeeded(required))
	}
	// Prefer totals map headline fields when present (rename/change plans).
	if totals, ok := values["totals"].(map[string]any); ok {
		for _, key := range []string{"mustEditFiles", "directoryPathFiles", "packageIdentityFiles", "mustNotTouchFiles"} {
			if value, ok := totals[key]; ok && isScalar(value) {
				parts = append(parts, fmt.Sprintf("%s=%v", key, value))
			}
		}
	}
	for _, key := range []string{
		"returned", "uniqueFiles", "totalMatches", "totalHits", "runtimeReadCount",
		"totalCallSites", "missingCallSites", "satisfiedCallSites", "unownedCallSites",
		"resolutionMethod", "confidence", "dirtyFileCount", "exactCount",
		"seedCount", "dependentCount", "relatedTestCount", "packageCount",
		"symbolCount", "seedPathCount", "mustEditCount", "mustNotTouchCount",
		"mustVerifyCount", "totalMatchesApprox",
	} {
		if value, ok := values[key]; ok {
			if isScalar(value) {
				parts = append(parts, fmt.Sprintf("%s=%v", key, value))
			}
		}
	}
	for _, key := range []string{
		"needsDisambiguation", "graphReliable", "stale", "hasCallGraph", "ambiguous",
		"truncated", "scanTruncated", "listTruncated", "complete", "mustEditIsSample",
		"success", "incremental",
	} {
		if value, ok := values[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	if value, ok := values["contextCompleteForPlanning"]; ok {
		parts = append(parts, fmt.Sprintf("planningReady=%v", value))
	}
	return strings.Join(parts, " ")
}

func compactGraphResult(tool string, values map[string]any, maxItems int) string {
	if rawNodes, ok := values["nodes"].([]map[string]any); ok {
		var b strings.Builder
		b.WriteString(compactHeader(tool, values))
		b.WriteByte('\n')
		writeNodeList(&b, "nodes", rawNodes, 0, maxItems)
		if rawRels, ok := values["relationships"].([]map[string]any); ok {
			writeRelationList(&b, "relationships", rawRels, 0, maxItems)
		}
		return strings.TrimSpace(b.String())
	}
	if rawPaths, ok := values["paths"].([]map[string]any); ok {
		var b strings.Builder
		b.WriteString(compactHeader(tool, values))
		b.WriteByte('\n')
		shown := min(len(rawPaths), maxItems)
		for index := 0; index < shown; index++ {
			path := rawPaths[index]
			b.WriteString(fmt.Sprintf("%d. path\n", index+1))
			if nodes, ok := path["nodes"].([]map[string]any); ok {
				writeNodeList(&b, "nodes", nodes, 2, maxItems)
			}
			if rels, ok := path["relationships"].([]map[string]any); ok {
				writeRelationList(&b, "relationships", rels, 2, maxItems)
			}
		}
		if len(rawPaths) > maxItems {
			b.WriteString(fmt.Sprintf("... +%d more paths\n", len(rawPaths)-maxItems))
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func writeMapSections(b *strings.Builder, values map[string]any, indent int, maxItems int, skipScalars map[string]bool) {
	if skipScalars == nil {
		skipScalars = map[string]bool{}
	}
	scalars := scalarPairs(values, skipScalars, "code", "text", "files", "nodes", "relationships", "paths")
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
			writeStringList(b, key, value, indent, maxItems)
		case []map[string]any:
			writeMapList(b, key, value, indent, maxItems)
		case map[string]any:
			writeNestedMap(b, key, value, indent, maxItems)
		case map[string]int:
			if len(value) == 0 {
				continue
			}
			b.WriteString(indentString(indent))
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(compactValue(value))
			b.WriteByte('\n')
		default:
			// Normalize map[string][]map[string]any and similar nested containers.
			if nested, ok := asStringAnyMap(value); ok {
				writeNestedMap(b, key, nested, indent, maxItems)
				continue
			}
			if items, ok := asMapSlice(value); ok {
				writeMapList(b, key, items, indent, maxItems)
				continue
			}
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

func writeMapList(b *strings.Builder, name string, items []map[string]any, indent int, maxItems int) {
	if len(items) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(items)))
	shown := min(len(items), maxItems)
	for index := 0; index < shown; index++ {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("- %s\n", compactMapItem(index+1, items[index])))
	}
	if len(items) > maxItems {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("... +%d more\n", len(items)-maxItems))
	}
}

func writeNodeList(b *strings.Builder, name string, nodes []map[string]any, indent int, maxItems int) {
	if len(nodes) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(nodes)))
	shown := min(len(nodes), maxItems)
	for index := 0; index < shown; index++ {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("- %d. %s\n", index+1, compactNode(nodes[index])))
	}
	if len(nodes) > maxItems {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("... +%d more\n", len(nodes)-maxItems))
	}
}

func writeRelationList(b *strings.Builder, name string, rels []map[string]any, indent int, maxItems int) {
	if len(rels) == 0 {
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(fmt.Sprintf("%s (%d)\n", name, len(rels)))
	shown := min(len(rels), maxItems)
	for index := 0; index < shown; index++ {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("- %d. %s\n", index+1, compactRelation(rels[index])))
	}
	if len(rels) > maxItems {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("... +%d more\n", len(rels)-maxItems))
	}
}

func writeStringList(b *strings.Builder, name string, values []string, indent int, maxItems int) {
	if len(values) == 0 {
		return
	}
	// Sample lists: show real total in the label, print only a few examples so
	// agents do not treat list length as impact size.
	listMax := maxItems
	total := len(values)
	if strings.HasSuffix(name, "Sample") || strings.HasSuffix(name, "sample") {
		listMax = min(maxItems, 8)
	}
	b.WriteString(indentString(indent))
	if strings.HasSuffix(name, "Sample") || strings.HasSuffix(name, "sample") {
		b.WriteString(fmt.Sprintf("%s (sample %d of set; use *Count/totals for size)\n", name, total))
	} else {
		b.WriteString(fmt.Sprintf("%s (%d)\n", name, total))
	}
	shown := min(total, listMax)
	for index := 0; index < shown; index++ {
		b.WriteString(indentString(indent))
		b.WriteString("- ")
		b.WriteString(values[index])
		b.WriteByte('\n')
	}
	if total > listMax {
		b.WriteString(indentString(indent))
		b.WriteString(fmt.Sprintf("... +%d more\n", total-listMax))
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
	for _, key := range []string{"category", "role", "kind", "count", "hitCount", "matchCount", "readCount", "owner", "reason", "why", "level", "truncated", "hasRequiredBeforeCall"} {
		if value, ok := item[key]; ok && isScalar(value) {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	if owners := stringSlice(item["owners"]); len(owners) > 0 {
		shown := min(len(owners), 3)
		label := strings.Join(owners[:shown], ",")
		if len(owners) > shown {
			label += fmt.Sprintf("+%d", len(owners)-shown)
		}
		parts = append(parts, "owners="+label)
	}
	if ownerCount, ok := item["ownerCount"]; ok && isScalar(ownerCount) && len(stringSlice(item["owners"])) == 0 {
		parts = append(parts, fmt.Sprintf("ownerCount=%v", ownerCount))
	}
	if snippet := firstAnyString(item, "snippet", "text"); snippet != "" {
		parts = append(parts, "snippet="+compactLine(snippet, 100))
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
	// Prefer human sourceIds over opaque element ids when present.
	for _, key := range []string{"from", "to", "startSourceId", "endSourceId"} {
		if value := firstAnyString(rel, key); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if source != "" {
		parts = append(parts, "at="+source)
	}
	return strings.Join(parts, " ")
}

func scalarPairs(values map[string]any, skip map[string]bool, exclude ...string) []string {
	excluded := map[string]bool{}
	for _, key := range exclude {
		excluded[key] = true
	}
	for key, value := range skip {
		if value {
			excluded[key] = true
		}
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
	case nil, string, bool, int, int32, int64, float32, float64:
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
		return strings.Join(scalarPairs(typed, nil), " ")
	case map[string]int:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", key, typed[key]))
		}
		return strings.Join(parts, " ")
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

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func writeNestedMap(b *strings.Builder, key string, value map[string]any, indent int, maxItems int) {
	if len(value) == 0 {
		return
	}
	// Prefer inline scalar maps (counts) over nested sections.
	if pairs := scalarPairs(value, nil); len(pairs) == len(value) {
		b.WriteString(indentString(indent))
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(strings.Join(pairs, " "))
		b.WriteByte('\n')
		return
	}
	b.WriteString(indentString(indent))
	b.WriteString(key)
	b.WriteString(":\n")
	writeMapSections(b, value, indent+2, maxItems, nil)
}

func asStringAnyMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string][]map[string]any:
		out := make(map[string]any, len(typed))
		for key, items := range typed {
			out[key] = items
		}
		return out, true
	case map[string][]string:
		out := make(map[string]any, len(typed))
		for key, items := range typed {
			out[key] = items
		}
		return out, true
	case map[string]int:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func asMapSlice(value any) ([]map[string]any, bool) {
	switch typed := value.(type) {
	case []map[string]any:
		return typed, true
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				out = append(out, entry)
			} else {
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// limitSlice caps a slice for summary responses and reports whether truncation occurred.
func limitSlice[T any](items []T, limit int) ([]T, bool) {
	if limit <= 0 || len(items) <= limit {
		return items, false
	}
	return items[:limit], true
}

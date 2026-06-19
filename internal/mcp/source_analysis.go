package mcp

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type literalSearchOptions struct {
	IncludeTests    bool
	IncludeDocs     bool
	IncludeConfig   bool
	IncludeScripts  bool
	IncludeHidden   bool
	IncludeTmp      bool
	IncludeLines    bool
	IncludeSnippets bool
	MatchesPerFile  int
	Limit           int
}

type literalFileMatch struct {
	Path       string                 `json:"path"`
	Category   string                 `json:"category"`
	MatchCount int                    `json:"matchCount"`
	Matches    []literalLineMatch     `json:"matches,omitempty"`
	KindCounts map[string]int         `json:"-"`
	Flags      map[string]interface{} `json:"flags,omitempty"`
}

type literalLineMatch struct {
	Line    int    `json:"line"`
	Snippet string `json:"snippet,omitempty"`
	Kind    string `json:"kind"`
	Owner   string `json:"owner,omitempty"`
}

type functionImpactOptions struct {
	IncludeTests    bool
	IncludeDocs     bool
	IncludeConfig   bool
	IncludeScripts  bool
	IncludeHidden   bool
	IncludeTmp      bool
	IncludeLines    bool
	IncludeSnippets bool
	MatchesPerFile  int
	Limit           int
}

type functionImpactResult struct {
	Symbol      string              `json:"symbol"`
	Definitions []functionFileMatch `json:"definitions"`
	Imports     []functionFileMatch `json:"imports"`
	CallSites   []functionFileMatch `json:"callSites"`
	References  []functionFileMatch `json:"references"`
	Counts      map[string]int      `json:"counts"`
	Files       []string            `json:"files"`
	UniqueFiles int                 `json:"uniqueFiles"`
	TotalHits   int                 `json:"totalHits"`
	Truncated   bool                `json:"truncated"`
}

type functionFileMatch struct {
	Path       string             `json:"path"`
	Category   string             `json:"category"`
	HitCount   int                `json:"hitCount"`
	Owners     []string           `json:"owners,omitempty"`
	Matches    []literalLineMatch `json:"matches,omitempty"`
	KindCounts map[string]int     `json:"-"`
}

type callsiteContractOptions struct {
	IncludeTests    bool
	IncludeScripts  bool
	IncludeHidden   bool
	IncludeTmp      bool
	IncludeSnippets bool
	Limit           int
}

type callsiteContractResult struct {
	Callee             string                  `json:"callee"`
	RequiredBeforeCall string                  `json:"requiredBeforeCall"`
	CallSites          []callsiteContractMatch `json:"callSites"`
	Missing            []callsiteContractMatch `json:"missing"`
	Satisfied          []callsiteContractMatch `json:"satisfied"`
	Files              []string                `json:"files"`
	UniqueFiles        int                     `json:"uniqueFiles"`
	TotalCallSites     int                     `json:"totalCallSites"`
	MissingCallSites   int                     `json:"missingCallSites"`
	SatisfiedCallSites int                     `json:"satisfiedCallSites"`
	UnownedCallSites   int                     `json:"unownedCallSites"`
	Truncated          bool                    `json:"truncated"`
}

type callsiteContractMatch struct {
	Path                  string `json:"path"`
	Category              string `json:"category"`
	Owner                 string `json:"owner,omitempty"`
	Line                  int    `json:"line"`
	RequiredLine          int    `json:"requiredLine,omitempty"`
	HasRequiredBeforeCall bool   `json:"hasRequiredBeforeCall"`
	Snippet               string `json:"snippet,omitempty"`
}

type literalSearchResult struct {
	Query        string             `json:"query"`
	Files        []literalFileMatch `json:"files"`
	UniqueFiles  int                `json:"uniqueFiles"`
	TotalMatches int                `json:"totalMatches"`
	Counts       map[string]int     `json:"counts"`
	Truncated    bool               `json:"truncated"`
}

func searchLiteralFiles(repo string, query string, opts literalSearchOptions) (literalSearchResult, error) {
	opts = normalizeLiteralOptions(opts)
	result := literalSearchResult{Query: query, Counts: map[string]int{}}
	if query == "" {
		return result, nil
	}
	handleFile := func(path string, rel string) error {
		if shouldSkipFile(filepath.Base(path), rel, opts) {
			return nil
		}
		if !isSearchableFile(rel) {
			return nil
		}
		category := classifyPath(rel)
		if !includeCategory(category, opts) {
			return nil
		}
		fileMatch, ok, err := scanFileForLiteral(path, rel, category, query, opts)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		result.Files = append(result.Files, fileMatch)
		result.TotalMatches += fileMatch.MatchCount
		result.Counts[category]++
		if len(result.Files) >= opts.Limit {
			result.Truncated = true
		}
		return nil
	}
	if candidates, ok, err := candidateFiles(repo, query, opts.IncludeHidden, opts.IncludeTmp); err != nil {
		return result, err
	} else if ok {
		for _, rel := range candidates {
			if result.Truncated {
				break
			}
			path := filepath.Join(repo, filepath.FromSlash(rel))
			if err := handleFile(path, rel); err != nil {
				return result, err
			}
		}
		sortLiteralResult(&result)
		return result, nil
	}
	err := filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == repo {
			return nil
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if shouldSkipDir(entry.Name(), rel, opts) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(entry.Name(), rel, opts) {
			return nil
		}
		if err := handleFile(path, rel); err != nil {
			return err
		}
		if result.Truncated {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	sortLiteralResult(&result)
	return result, nil
}

func sortLiteralResult(result *literalSearchResult) {
	slices.SortFunc(result.Files, func(left literalFileMatch, right literalFileMatch) int {
		return strings.Compare(left.Path, right.Path)
	})
	result.UniqueFiles = len(result.Files)
}

func analyzeFunctionImpact(repo string, symbol string, opts functionImpactOptions) (functionImpactResult, error) {
	opts = normalizeFunctionImpactOptions(opts)
	result := functionImpactResult{Symbol: symbol, Counts: map[string]int{}}
	if symbol == "" {
		return result, nil
	}
	patterns := functionPatterns(symbol)
	fileKinds := map[string]map[string]functionFileMatch{}
	handleFile := func(path string, rel string) error {
		if shouldSkipFile(filepath.Base(path), rel, literalSearchOptions{IncludeHidden: opts.IncludeHidden, IncludeTmp: opts.IncludeTmp}) {
			return nil
		}
		if !isSearchableFile(rel) {
			return nil
		}
		category := classifyPath(rel)
		if !includeFunctionCategory(category, opts) {
			return nil
		}
		matches, err := scanFileForFunctionImpact(path, rel, symbol, category, patterns, opts)
		if err != nil {
			return err
		}
		for _, match := range matches {
			if fileKinds[match.Path] == nil {
				fileKinds[match.Path] = map[string]functionFileMatch{}
			}
			existing := fileKinds[match.Path][primaryFunctionKind(match)]
			if existing.Path == "" {
				existing = functionFileMatch{Path: match.Path, Category: match.Category, KindCounts: map[string]int{}}
			}
			existing.HitCount += match.HitCount
			existing.Matches = append(existing.Matches, match.Matches...)
			for _, owner := range match.Owners {
				existing.Owners = appendUnique(existing.Owners, owner)
			}
			for kind, count := range match.KindCounts {
				existing.KindCounts[kind] += count
			}
			fileKinds[match.Path][primaryFunctionKind(match)] = existing
		}
		if len(fileKinds) >= opts.Limit {
			result.Truncated = true
		}
		return nil
	}
	if candidates, ok, err := candidateFiles(repo, symbol, opts.IncludeHidden, opts.IncludeTmp); err != nil {
		return result, err
	} else if ok {
		for _, rel := range candidates {
			if result.Truncated {
				break
			}
			path := filepath.Join(repo, filepath.FromSlash(rel))
			if err := handleFile(path, rel); err != nil {
				return result, err
			}
		}
		completeFunctionImpact(&result, fileKinds)
		return result, nil
	}
	err := filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == repo {
			return nil
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if shouldSkipDir(entry.Name(), rel, literalSearchOptions{IncludeHidden: opts.IncludeHidden, IncludeTmp: opts.IncludeTmp}) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(entry.Name(), rel, literalSearchOptions{IncludeHidden: opts.IncludeHidden, IncludeTmp: opts.IncludeTmp}) {
			return nil
		}
		if err := handleFile(path, rel); err != nil {
			return err
		}
		if result.Truncated {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	completeFunctionImpact(&result, fileKinds)
	return result, nil
}

func completeFunctionImpact(result *functionImpactResult, fileKinds map[string]map[string]functionFileMatch) {
	seenFiles := map[string]bool{}
	for _, byKind := range fileKinds {
		for kind, match := range byKind {
			result.TotalHits += match.HitCount
			result.Counts[match.Category]++
			if !seenFiles[match.Path] {
				result.Files = append(result.Files, match.Path)
				seenFiles[match.Path] = true
			}
			switch kind {
			case "definition":
				result.Definitions = append(result.Definitions, match)
			case "import":
				result.Imports = append(result.Imports, match)
			case "reference":
				result.References = append(result.References, match)
			default:
				result.CallSites = append(result.CallSites, match)
			}
		}
	}
	sortFunctionMatches(result.Definitions)
	sortFunctionMatches(result.Imports)
	sortFunctionMatches(result.CallSites)
	sortFunctionMatches(result.References)
	slices.Sort(result.Files)
	result.UniqueFiles = len(result.Files)
}

func analyzeCallsiteContract(repo string, callee string, requiredBeforeCall string, opts callsiteContractOptions) (callsiteContractResult, error) {
	opts = normalizeCallsiteContractOptions(opts)
	result := callsiteContractResult{Callee: callee, RequiredBeforeCall: requiredBeforeCall}
	if callee == "" || requiredBeforeCall == "" {
		return result, nil
	}
	handleFile := func(path string, rel string) error {
		if shouldSkipFile(filepath.Base(path), rel, literalSearchOptions{IncludeHidden: opts.IncludeHidden, IncludeTmp: opts.IncludeTmp}) {
			return nil
		}
		if !isSearchableFile(rel) {
			return nil
		}
		category := classifyPath(rel)
		if !includeCallsiteContractCategory(category, opts) {
			return nil
		}
		matches, err := scanFileForCallsiteContract(path, rel, category, callee, requiredBeforeCall, opts)
		if err != nil {
			return err
		}
		for _, match := range matches {
			result.CallSites = append(result.CallSites, match)
			if match.HasRequiredBeforeCall {
				result.Satisfied = append(result.Satisfied, match)
			} else {
				result.Missing = append(result.Missing, match)
			}
			if match.Owner == "" {
				result.UnownedCallSites++
			}
			if len(result.CallSites) >= opts.Limit {
				result.Truncated = true
				break
			}
		}
		return nil
	}
	if candidates, ok, err := candidateFiles(repo, callee, opts.IncludeHidden, opts.IncludeTmp); err != nil {
		return result, err
	} else if ok {
		for _, rel := range candidates {
			if result.Truncated {
				break
			}
			path := filepath.Join(repo, filepath.FromSlash(rel))
			if err := handleFile(path, rel); err != nil {
				return result, err
			}
		}
		completeCallsiteContract(&result)
		return result, nil
	}
	err := filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == repo {
			return nil
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if shouldSkipDir(entry.Name(), rel, literalSearchOptions{IncludeHidden: opts.IncludeHidden, IncludeTmp: opts.IncludeTmp}) {
				return filepath.SkipDir
			}
			return nil
		}
		if err := handleFile(path, rel); err != nil {
			return err
		}
		if result.Truncated {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	completeCallsiteContract(&result)
	return result, nil
}

func completeCallsiteContract(result *callsiteContractResult) {
	seenFiles := map[string]bool{}
	for _, match := range result.CallSites {
		if !seenFiles[match.Path] {
			result.Files = append(result.Files, match.Path)
			seenFiles[match.Path] = true
		}
	}
	slices.Sort(result.Files)
	result.UniqueFiles = len(result.Files)
	result.TotalCallSites = len(result.CallSites)
	result.MissingCallSites = len(result.Missing)
	result.SatisfiedCallSites = len(result.Satisfied)
	sortCallsiteContractMatches(result.CallSites)
	sortCallsiteContractMatches(result.Missing)
	sortCallsiteContractMatches(result.Satisfied)
}

func candidateFiles(repo string, query string, includeHidden bool, includeTmp bool) ([]string, bool, error) {
	args := []string{"--files-with-matches", "--fixed-strings", "--no-messages"}
	if includeHidden {
		args = append(args, "--hidden", "--no-ignore")
	}
	for _, glob := range []string{
		"!.git/**",
		"!**/.git/**",
		"!node_modules/**",
		"!**/node_modules/**",
		"!.next/**",
		"!**/.next/**",
		"!dist/**",
		"!**/dist/**",
		"!coverage/**",
		"!**/coverage/**",
		"!.turbo/**",
		"!**/.turbo/**",
		"!.cache/**",
		"!**/.cache/**",
	} {
		args = append(args, "--glob", glob)
	}
	if !includeTmp {
		args = append(args, "--glob", "!tmp/**")
	}
	args = append(args, "--", query, repo)
	cmd := exec.Command("rg", args...)
	output, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return []string{}, true, nil
		}
		return nil, false, nil
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, true, nil
	}
	seen := map[string]bool{}
	files := []string{}
	for _, line := range lines {
		rel, err := filepath.Rel(repo, line)
		if err != nil {
			return nil, true, err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || seen[rel] {
			continue
		}
		files = append(files, rel)
		seen[rel] = true
	}
	slices.Sort(files)
	return files, true, nil
}

func normalizeLiteralOptions(opts literalSearchOptions) literalSearchOptions {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.MatchesPerFile <= 0 {
		opts.MatchesPerFile = 3
	}
	return opts
}

func normalizeFunctionImpactOptions(opts functionImpactOptions) functionImpactOptions {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.MatchesPerFile <= 0 {
		opts.MatchesPerFile = 5
	}
	return opts
}

func normalizeCallsiteContractOptions(opts callsiteContractOptions) callsiteContractOptions {
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	return opts
}

func functionPatterns(symbol string) map[string]*regexp.Regexp {
	quoted := regexp.QuoteMeta(symbol)
	return map[string]*regexp.Regexp{
		"definition": regexp.MustCompile(`\b(function|class|interface|type|const|let|var)\s+` + quoted + `\b|\b` + quoted + `\s*[:=]\s*(async\s*)?(\([^)]*\)\s*=>|function\b)|\b` + quoted + `\s*\([^)]*\)\s*\{`),
		"import":     regexp.MustCompile(`\bimport\b.*\b` + quoted + `\b`),
		"call":       regexp.MustCompile(`\b` + quoted + `\s*\(`),
		"methodCall": regexp.MustCompile(`\.` + quoted + `\s*\(`),
		"jsx":        regexp.MustCompile(`<` + quoted + `(\s|/|>)`),
		"reference":  regexp.MustCompile(`\b` + quoted + `\b`),
	}
}

func scanFileForFunctionImpact(path string, rel string, symbol string, category string, patterns map[string]*regexp.Regexp, opts functionImpactOptions) ([]functionFileMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	byKind := map[string]functionFileMatch{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024*10)
	lineNumber := 0
	currentOwner := ""
	ownerDepth := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if isCommentLine(line) {
			continue
		}
		if owner := extractOwnerName(line); owner != "" {
			currentOwner = owner
			ownerDepth = 0
		}
		if !strings.Contains(line, symbol) {
			if currentOwner != "" {
				ownerDepth += braceDelta(line)
				if ownerDepth <= 0 && strings.Contains(line, "}") {
					currentOwner = ""
				}
			}
			continue
		}
		kind := classifyFunctionLine(line, patterns)
		if kind == "" {
			if currentOwner != "" {
				ownerDepth += braceDelta(line)
				if ownerDepth <= 0 && strings.Contains(line, "}") {
					currentOwner = ""
				}
			}
			continue
		}
		matchKind := bucketFunctionKind(kind)
		match := byKind[matchKind]
		if match.Path == "" {
			match = functionFileMatch{Path: rel, Category: category, KindCounts: map[string]int{}}
		}
		match.HitCount++
		match.KindCounts[kind]++
		if currentOwner != "" {
			match.Owners = appendUnique(match.Owners, currentOwner)
		}
		if (opts.IncludeSnippets || opts.IncludeLines) && len(match.Matches) < opts.MatchesPerFile {
			lineMatch := literalLineMatch{Line: lineNumber, Kind: kind, Owner: currentOwner}
			if opts.IncludeSnippets {
				lineMatch.Snippet = redactSnippet(rel, strings.TrimSpace(line))
			}
			match.Matches = append(match.Matches, lineMatch)
		}
		byKind[matchKind] = match
		if currentOwner != "" {
			ownerDepth += braceDelta(line)
			if ownerDepth <= 0 && strings.Contains(line, "}") {
				currentOwner = ""
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	matches := make([]functionFileMatch, 0, len(byKind))
	for _, match := range byKind {
		matches = append(matches, match)
	}
	return matches, nil
}

func scanFileForCallsiteContract(path string, rel string, category string, callee string, requiredBeforeCall string, opts callsiteContractOptions) ([]callsiteContractMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	calleePattern := callPattern(callee)
	requiredPattern := callPattern(requiredBeforeCall)
	matches := []callsiteContractMatch{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024*10)
	lineNumber := 0
	currentOwner := ""
	ownerDepth := 0
	requiredLine := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if isCommentLine(line) {
			continue
		}
		if owner := extractOwnerName(line); owner != "" {
			currentOwner = owner
			ownerDepth = 0
			requiredLine = 0
		}

		requiredIndexes := requiredPattern.FindAllStringIndex(line, -1)
		calleeIndexes := calleePattern.FindAllStringIndex(line, -1)
		for _, callIndex := range calleeIndexes {
			if isDefinitionOrImportLine(line, callee) {
				continue
			}
			effectiveRequiredLine := requiredLine
			for _, requiredIndex := range requiredIndexes {
				if requiredIndex[0] < callIndex[0] {
					effectiveRequiredLine = lineNumber
					break
				}
			}
			match := callsiteContractMatch{
				Path:                  rel,
				Category:              category,
				Owner:                 currentOwner,
				Line:                  lineNumber,
				RequiredLine:          effectiveRequiredLine,
				HasRequiredBeforeCall: effectiveRequiredLine > 0,
			}
			if opts.IncludeSnippets {
				match.Snippet = redactSnippet(rel, strings.TrimSpace(line))
			}
			matches = append(matches, match)
			if len(matches) >= opts.Limit {
				return matches, nil
			}
		}
		if len(requiredIndexes) > 0 && len(calleeIndexes) == 0 && !isDefinitionOrImportLine(line, requiredBeforeCall) {
			requiredLine = lineNumber
		}
		if currentOwner != "" {
			ownerDepth += braceDelta(line)
			if ownerDepth <= 0 && strings.Contains(line, "}") {
				currentOwner = ""
				requiredLine = 0
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func callPattern(symbol string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(symbol)
	return regexp.MustCompile(`(?:^|[^.\w$])` + quoted + `\s*\(|\.` + quoted + `\s*\(`)
}

func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func extractOwnerName(line string) string {
	trimmed := strings.TrimSpace(line)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\b`),
		regexp.MustCompile(`^export\s+(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`),
		regexp.MustCompile(`^(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`),
		regexp.MustCompile(`^(?:async\s+)?([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*\{`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(trimmed)
		if len(match) == 2 && !isControlKeyword(match[1]) {
			return match[1]
		}
	}
	return ""
}

func isControlKeyword(value string) bool {
	switch value {
	case "if", "for", "while", "switch", "catch", "function":
		return true
	default:
		return false
	}
}

func braceDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
}

func isDefinitionOrImportLine(line string, symbol string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export ") && strings.Contains(trimmed, " from ") {
		return true
	}
	return functionPatterns(symbol)["definition"].MatchString(trimmed)
}

func classifyFunctionLine(line string, patterns map[string]*regexp.Regexp) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
		return ""
	}
	if patterns["definition"].MatchString(trimmed) {
		return "definition"
	}
	if patterns["import"].MatchString(trimmed) {
		return "import"
	}
	if patterns["methodCall"].MatchString(trimmed) {
		return "method_call"
	}
	if patterns["call"].MatchString(trimmed) {
		return "call"
	}
	if patterns["jsx"].MatchString(trimmed) {
		return "jsx"
	}
	if patterns["reference"].MatchString(trimmed) {
		return "reference"
	}
	return ""
}

func bucketFunctionKind(kind string) string {
	switch kind {
	case "definition":
		return "definition"
	case "import":
		return "import"
	case "reference":
		return "reference"
	default:
		return "call"
	}
}

func primaryFunctionKind(match functionFileMatch) string {
	if match.KindCounts["definition"] > 0 {
		return "definition"
	}
	if match.KindCounts["import"] > 0 {
		return "import"
	}
	if match.KindCounts["reference"] > 0 && match.KindCounts["call"] == 0 && match.KindCounts["method_call"] == 0 && match.KindCounts["jsx"] == 0 {
		return "reference"
	}
	return "call"
}

func includeFunctionCategory(category string, opts functionImpactOptions) bool {
	switch category {
	case "test":
		return opts.IncludeTests
	case "docs":
		return opts.IncludeDocs
	case "config":
		return opts.IncludeConfig
	case "script":
		return opts.IncludeScripts
	default:
		return true
	}
}

func includeCallsiteContractCategory(category string, opts callsiteContractOptions) bool {
	switch category {
	case "test":
		return opts.IncludeTests
	case "docs", "config":
		return false
	case "script":
		return opts.IncludeScripts
	default:
		return true
	}
}

func sortFunctionMatches(matches []functionFileMatch) {
	slices.SortFunc(matches, func(left functionFileMatch, right functionFileMatch) int {
		return strings.Compare(left.Path, right.Path)
	})
}

func sortCallsiteContractMatches(matches []callsiteContractMatch) {
	slices.SortFunc(matches, func(left callsiteContractMatch, right callsiteContractMatch) int {
		if left.Path == right.Path {
			return left.Line - right.Line
		}
		return strings.Compare(left.Path, right.Path)
	})
}

func shouldSkipDir(name string, rel string, opts literalSearchOptions) bool {
	switch name {
	case ".git", "node_modules", ".next", "dist", "coverage", ".turbo", ".cache":
		return true
	case "tmp":
		return !opts.IncludeTmp
	default:
		if strings.HasPrefix(name, ".") && !opts.IncludeHidden {
			return true
		}
		return strings.HasSuffix(rel, "/node_modules")
	}
}

func shouldSkipFile(name string, rel string, opts literalSearchOptions) bool {
	if strings.HasPrefix(name, ".") && !opts.IncludeHidden {
		return true
	}
	return strings.HasPrefix(rel, "tmp/") && !opts.IncludeTmp
}

func isSearchableFile(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".env") {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json", ".md", ".yml", ".yaml", ".env", ".toml":
		return true
	default:
		return false
	}
}

func classifyPath(path string) string {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	if strings.Contains(path, "__tests__/") || strings.Contains(path, ".test.") || strings.Contains(path, ".spec.") {
		return "test"
	}
	if ext == ".md" {
		return "docs"
	}
	if strings.HasPrefix(path, "scripts/") {
		return "script"
	}
	if strings.HasPrefix(base, ".env") || ext == ".json" || ext == ".yml" || ext == ".yaml" || ext == ".toml" || strings.Contains(base, "config") {
		return "config"
	}
	return "runtime"
}

func includeCategory(category string, opts literalSearchOptions) bool {
	switch category {
	case "test":
		return opts.IncludeTests
	case "docs":
		return opts.IncludeDocs
	case "config":
		return opts.IncludeConfig
	case "script":
		return opts.IncludeScripts
	default:
		return true
	}
}

func scanFileForLiteral(path string, rel string, category string, query string, opts literalSearchOptions) (literalFileMatch, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return literalFileMatch{}, false, err
	}
	defer file.Close()

	match := literalFileMatch{Path: rel, Category: category, Flags: map[string]interface{}{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024*10)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if !strings.Contains(line, query) {
			continue
		}
		match.MatchCount++
		kind := classifyLine(query, line)
		if match.KindCounts == nil {
			match.KindCounts = map[string]int{}
		}
		match.KindCounts[kind]++
		if kind == "runtime_env_read" {
			match.Flags["runtimeEnvRead"] = true
		}
		if opts.IncludeSnippets && len(match.Matches) < opts.MatchesPerFile {
			match.Matches = append(match.Matches, literalLineMatch{Line: lineNumber, Snippet: redactSnippet(rel, strings.TrimSpace(line)), Kind: kind})
		} else if opts.IncludeLines && len(match.Matches) < opts.MatchesPerFile {
			match.Matches = append(match.Matches, literalLineMatch{Line: lineNumber, Kind: kind})
		}
	}
	if err := scanner.Err(); err != nil {
		return literalFileMatch{}, false, err
	}
	if match.MatchCount == 0 {
		return literalFileMatch{}, false, nil
	}
	if len(match.Flags) == 0 {
		match.Flags = nil
	}
	return match, true, nil
}

func classifyLine(query string, line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "process.env."+query) || strings.Contains(trimmed, "process.env["+query+"]") || strings.Contains(trimmed, `process.env["`+query+`"]`) || strings.Contains(trimmed, `process.env['`+query+`']`) {
		return "runtime_env_read"
	}
	if strings.Contains(trimmed, query+"=") {
		return "env_assignment"
	}
	return "literal_reference"
}

func redactSnippet(path string, line string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".env") {
		if before, _, ok := strings.Cut(line, "="); ok {
			return before + "=<redacted>"
		}
	}
	if len(line) > 180 {
		return line[:177] + "..."
	}
	return line
}

package mcp

// AgentInstructions is returned on MCP initialize so clients that honor
// server instructions inject this into the agent system/context without a tool call.
// Keep it short, imperative, and tool-name exact.
const AgentInstructions = `CodeGraph is an impact/planning layer for one indexed TypeScript/JavaScript ripple. It is not a replacement for Read, Grep/rg, git, tests, or the editor.

Policy:
1. Prefer one high-level CodeGraph tool first for planning and blast radius.
2. Use normal tools to read exact source, edit, run tests, and check git.
3. Treat graph answers as incomplete when graphReliable=false, stale=true, confidence=low, or needsDisambiguation=true.
4. Do not open whole trees from CodeGraph; use mustEditCount/totals + openNext samples. NEVER report len(mustEditSample) as total impact.

Huge / multi-file change workflow:
1. get_index_freshness — if stale or graphReliable=false, plan to reindex after a batch of edits (or reindex first if the index is cold).
2. prepare_change_plan with symbols[] and/or paths[] (or useDirty=true). For a single feature name, prepare_feature_context is enough.
3. If needsDisambiguation=true → resolve_symbol with package/pathPrefix/symbolId → rerun the plan/impact tool with that pick.
4. Edit using mustEditCount/totals (samples are examples only). Verify mustVerifyCount. Open only openNext when implementing.
5. After many edits: analyze_path_set_impact with useDirty=true (or the edited paths). If you need graph CALLS again, reindex (full rebuild; long-running) then re-run prepare_change_plan / analyze_function_impact.
6. App/package renames: prepare_rename_plan with path + packageName (not bare shortName). Respect mustNotTouch and decisions[] (directory-only vs +CI/Docker). Never claim complete impact while scanTruncated=true.
7. Specialized: analyze_rename_impact (single identity), find_env_usages, analyze_callsite_contract. Scope monorepos with package or pathPrefix.

Defaults stay compact (detail=summary). Raise detail only when it changes the answer. reindex is a full ripple rebuild, not file-incremental.`

// workflowSpec is the structured form returned by codegraph_help (and kept in
// sync with AgentInstructions). Agents parse keys more reliably than free prose.
func workflowSpec() map[string]any {
	return map[string]any{
		"role": "impact_and_planning_layer",
		"notAReplacementFor": []string{"Read", "Grep/rg", "git", "tests", "editor/LSP"},
		"policy": []string{
			"Prefer one high-level CodeGraph tool first for planning and blast radius.",
			"Use normal tools to read exact source, edit, run tests, and check git.",
			"Treat graph answers as incomplete when graphReliable=false, stale=true, confidence=low, or needsDisambiguation=true.",
			"Do not open whole trees; report mustEditCount/totals, not sample list lengths.",
		},
		"hugeChange": []map[string]any{
			{
				"step": 1,
				"tool": "get_index_freshness",
				"do":   "Check stale and graphReliable. Reindex when the index is cold or after large edit batches if you need CALLS edges.",
			},
			{
				"step": 2,
				"tool": "prepare_change_plan",
				"do":   "Pass symbols[] and/or paths[] (or useDirty=true). Use prepare_feature_context only for a single feature/symbol query.",
				"out":  []string{"mustEdit", "mustVerify", "suggestedOrder", "openNext", "needsDisambiguation", "confidence"},
			},
			{
				"step": 3,
				"tool": "resolve_symbol",
				"when": "needsDisambiguation=true or overloaded names in a monorepo",
				"do":   "Pick with package, pathPrefix, or symbolId; rerun plan/impact with that pick.",
			},
			{
				"step": 4,
				"tool": "normal_agent_tools",
				"do":   "Edit mustEdit in suggestedOrder. Verify mustVerify. Read only openNext paths when implementing.",
			},
			{
				"step": 5,
				"tool": "analyze_path_set_impact",
				"do":   "After edits, pass useDirty=true or the edited paths to refresh blast radius without a full reindex.",
			},
			{
				"step": 6,
				"tool": "reindex",
				"when": "stale=true / graphReliable=false and you need hybrid graph impact again",
				"do":   "Full ripple rebuild (not file-incremental). Then re-run prepare_change_plan or analyze_function_impact.",
			},
		},
		"specialized": []string{
			"prepare_rename_plan — multi-identity app/package rename (path + package + CI/Docker layers)",
			"analyze_function_impact — one-symbol hybrid blast radius",
			"analyze_rename_impact — single oldName only; prefer prepare_rename_plan for app moves",
			"find_env_usages — process.env.NAME runtime reads",
			"analyze_callsite_contract — required precheck before every call",
		},
		"packageRename": []map[string]any{
			{"step": 1, "tool": "prepare_rename_plan", "do": "path=apps/foo packageName=@scope/foo shortName=foo. Do not use bare shortName alone."},
			{"step": 2, "tool": "decisions", "do": "Choose directory+package only vs full CI/Docker identity rename."},
			{"step": 3, "tool": "normal_agent_tools", "do": "Edit mustEdit by layer; skip mustNotTouch."},
			{"step": 4, "tool": "rg", "do": "Verify successCriteria with unbounded rg; CodeGraph list samples can still hide paths in summary."},
		},
		"flags": map[string]string{
			"needsDisambiguation": "Stop broad edits; resolve_symbol then rerun with symbolId/package/pathPrefix.",
			"graphReliable=false": "Prefer text residual; reindex if graph CALLS are required.",
			"stale=true":          "Working tree dirty vs index; path tools still useful, graph may lag.",
			"confidence=low":      "Do not trust completeness; raise scope filters or reindex.",
			"truncated=true":      "Check scanTruncated vs listTruncated. If scanTruncated, raise limit before editing.",
			"scanTruncated=true":  "Matching-file scan hit the limit; impact is incomplete.",
			"listTruncated=true":  "Display samples capped only; uniqueFiles/allFiles still complete when scanTruncated=false.",
		},
		"scope": "On monorepos always pass package or pathPrefix when the name is common.",
	}
}

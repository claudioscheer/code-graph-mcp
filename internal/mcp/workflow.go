package mcp

// AgentInstructions is returned on MCP initialize so clients that honor
// server instructions inject this into the agent system/context without a tool call.
// Keep it short, imperative, and tool-name exact.
const AgentInstructions = `CodeGraph is an impact/planning layer for one indexed TypeScript/JavaScript ripple. It is not a replacement for Read, Grep/rg, git, tests, or the editor.

Policy:
1. Prefer one high-level CodeGraph tool first for planning and blast radius.
2. Use normal tools to read exact source, edit, run tests, and check git.
3. Treat graph answers as incomplete when graphReliable=false, stale=true, confidence=low, or needsDisambiguation=true.
4. Do not open whole trees from CodeGraph; use mustEdit / mustVerify / openNext / suggestedFollowUpReads.

Huge / multi-file change workflow:
1. get_index_freshness — if stale or graphReliable=false, plan to reindex after a batch of edits (or reindex first if the index is cold).
2. prepare_change_plan with symbols[] and/or paths[] (or useDirty=true). For a single feature name, prepare_feature_context is enough.
3. If needsDisambiguation=true → resolve_symbol with package/pathPrefix/symbolId → rerun the plan/impact tool with that pick.
4. Edit only mustEdit in suggestedOrder. Verify mustVerify (callers/tests). Open only openNext (or suggestedFollowUpReads) when implementing.
5. After many edits: analyze_path_set_impact with useDirty=true (or the edited paths). If you need graph CALLS again, reindex (full rebuild; long-running) then re-run prepare_change_plan / analyze_function_impact.
6. Specialized: analyze_rename_impact, find_env_usages, analyze_callsite_contract. Scope monorepos with package or pathPrefix.

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
			"Do not open whole trees; follow mustEdit / mustVerify / openNext / suggestedFollowUpReads.",
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
			"analyze_function_impact — one-symbol hybrid blast radius",
			"analyze_rename_impact — rename/migration buckets",
			"find_env_usages — process.env.NAME runtime reads",
			"analyze_callsite_contract — required precheck before every call",
		},
		"flags": map[string]string{
			"needsDisambiguation": "Stop broad edits; resolve_symbol then rerun with symbolId/package/pathPrefix.",
			"graphReliable=false": "Prefer text residual; reindex if graph CALLS are required.",
			"stale=true":          "Working tree dirty vs index; path tools still useful, graph may lag.",
			"confidence=low":      "Do not trust completeness; raise scope filters or reindex.",
			"truncated=true":      "Raise limit only if it changes the decision; else split the batch.",
		},
		"scope": "On monorepos always pass package or pathPrefix when the name is common.",
	}
}

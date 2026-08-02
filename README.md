# Code Graph MCP

Local code graph MCP, designed as a modular multi-language system.

The Go process owns CLI, project discovery, plugin orchestration, Neo4j ingestion/querying, and MCP. The TypeScript extractor is a Node subprocess that uses `ts-morph`, TypeScript Compiler API data, dependency-cruiser validation, and custom Next.js route extraction. Future language support plugs into the same GraphEvent NDJSON protocol.

## Supported Projects And Languages

Supported now:

- TypeScript and JavaScript repositories
- npm, pnpm, and Yarn package manager detection
- package workspaces declared in `package.json`
- common monorepo layouts using `apps/*` and `packages/*`
- Next.js App Router and Pages Router route extraction
- `.ts`, `.tsx`, `.js`, and `.jsx` source files

Not implemented yet:

- Go, Python, Ruby, Rust, Java, or other language extractors
- non-Next.js framework route extractors
- incremental file-level updates

The extractor protocol is language-neutral, so new language support should be added as a new subprocess extractor that emits the same `codegraph.v1` NDJSON events. The tradeoff is that v1 keeps the Go server stable and modular, but only the TypeScript/JavaScript extractor is production-usable today.

## When To Use CodeGraph (And When Not To)

CodeGraph is a **specialized impact and navigation layer** for agents. It is **not** a replacement for normal agent tools such as Read, Grep/`rg`, bash, git, or an IDE/LSP.

**Recommended policy for agents:**

1. Use CodeGraph **first** for planning, blast radius, rename impact, env usage, and call-site contract questions (one high-level tool).
2. Use normal tools to **read exact source, edit code, run tests, and check git**.
3. Never use CodeGraph as the only code-reading path.
4. Treat graph answers as incomplete when `graphReliable=false`, `stale=true`, `confidence=low`, or `needsDisambiguation=true`.

The same policy is embedded in the MCP so agents see it without reading this README:

- **`initialize.instructions`** — full agent workflow text (clients that honor MCP server instructions).
- **`codegraph_help`** — structured `workflow` object + short router.
- High-level **tool descriptions** — step hints (e.g. prepare_change_plan is primary for huge changes).

### Agent workflow: huge / multi-file changes

```text
1. get_index_freshness
   → note stale / graphReliable

2. prepare_change_plan  { symbols[], paths[] | useDirty: true }
   (single feature only → prepare_feature_context)

3. if needsDisambiguation
   → resolve_symbol { package | pathPrefix | symbolId }
   → rerun step 2 / analyze_function_impact with the pick

4. Implement with normal tools
   → edit mustEdit in suggestedOrder
   → verify mustVerify
   → open only openNext (or suggestedFollowUpReads)

5. After a batch of edits
   → analyze_path_set_impact { useDirty: true }   # cheap, live text + graph if warm

6. If you need hybrid CALLS again and graph is stale
   → reindex { timeoutSec: 300 }                  # full ripple rebuild
   → re-run prepare_change_plan / analyze_function_impact
```

Specialized (not the default path): `analyze_rename_impact`, `find_env_usages`, `analyze_callsite_contract`. On monorepos pass `package` or `pathPrefix` when names are common.

### Why CodeGraph is good

| Strength | What that means for agents |
|---|---|
| Task-shaped tools | One call for “impact of X”, “rename Y”, or “feature context” instead of many exploratory hops |
| Lower planning thrash | Bounds entry points, likely edit files, tests, and callers before the agent opens lots of files |
| Token-conscious defaults | Compact summary text, small list caps, low graph depth/limit; raise `detail` only when needed |
| Classified search | Env runtime reads, rename buckets (runtime/config/tests/docs/scripts), call-site owners |
| Indexed structure | Packages, files, routes, and dependency neighborhoods when the ripple index is good |
| Cross-file recipes | Blast radius and contract checks that are awkward to express as a single ad hoc `rg` |

CodeGraph is strongest when the pain is **agent thrash on large repos** (many tools, still missing callers/tests) rather than “find this string.”

### Why CodeGraph is a bad default for everything

| Limitation | What that means for agents |
|---|---|
| Not a source of truth | Index can be stale after local edits until you reindex; filesystem tools stay authoritative for current text |
| Incomplete graph in practice | Fast mode may skip symbol relationship traversal on large repos; graph “callers” can be partial |
| Overlaps with `rg` | Literal counts, much of impact analysis, and env search are classified filesystem search. Agents that already use Grep well get a lot of that without MCP |
| Weak for reading/editing | Excerpts are capped; implementation work still needs normal Read and the editor |
| Setup and ops cost | Neo4j, extract, index, serve, and reindex. Not free for every session |
| Search noise | Broad graph search can return nested locals/symbols that match by id/path substring |
| No types / no runtime proof | Static analysis only. Dynamic imports, DI, generated code, and runtime-only behavior are outside guarantees |
| Worse than git/LSP for some jobs | History, blame, diffs, and true go-to-definition while editing belong to git and the IDE |

### Prefer CodeGraph for

- Feature planning pack (entry points, likely files, tests, compact blast radius)
- “What breaks if this function/hook/component changes?”
- Rename or migration impact for a name or env var
- “Which files read `process.env.NAME` at runtime?”
- “Every call to X must be preceded by Y”
- Package/file dependency neighborhood for a known node id (with a warm index)

### Prefer normal agent tools for

- Reading exact implementation or applying edits (Read / editor)
- Ad hoc exact-string or regex search (Grep / `rg`)
- Opening one known path
- Git history, blame, status, diffs
- Type-aware navigation while editing (LSP / IDE)
- Freshness-critical answers right after unindexed local changes

### Bottom line

CodeGraph is a **prefetch and impact accelerator**, not a **general tool replacement**.

> Use CodeGraph once to bound the problem. Use normal tools to read and change the code.

If the main need is simple string search, stick with Grep/`rg`. If the main need is fewer exploratory hops and tighter planning context on large TypeScript monorepos, prefer the high-level CodeGraph tools first, then fall through to normal tools.

## Quick Start

```bash
cp .env.example .env
pnpm install
docker compose up -d neo4j
go run ./cmd/codegraph doctor
go run ./cmd/codegraph reset
go run ./cmd/codegraph index --ripple my-app --repo /path/to/repo --language typescript
go run ./cmd/codegraph status --ripple my-app
go run ./cmd/codegraph visualize --ripple my-app --output codegraph-visualization.html
go run ./cmd/codegraph serve --addr :8080
```

Docker-only for this repo:

```bash
docker compose --profile app run --rm app index --ripple code-graph --repo /repo --language typescript
```

Docker-only for another local repo:

```bash
docker compose --profile app run --rm -v /path/to/repo:/target:ro app index --ripple my-app --repo /target --language typescript
```

Neo4j Browser is available at `http://localhost:7474` with `neo4j/password`.

## Indexing Behavior

The TypeScript extractor respects root and nested `.gitignore` files before adding files to the graph. Built-in ignores still exclude generated/vendor paths such as `node_modules`, `.git`, `.next`, `dist`, `build`, coverage folders, and `.d.ts` files.

By default, indexing uses `--analysis-mode fast`. Fast mode stays bounded by using lightweight relative import resolution, skipping full symbol relationship traversal above the configured file limit, skipping dependency-cruiser validation above its configured file limit, and omitting symbol signatures. These limits are configurable:

```bash
CODEGRAPH_NODE_OPTIONS=--max-old-space-size=6144
CODEGRAPH_SYMBOL_RELATION_LIMIT=750
CODEGRAPH_FORCE_SYMBOL_RELATIONSHIPS=false
CODEGRAPH_DEPCRUISE_FILE_LIMIT=1500
CODEGRAPH_IDENTIFIER_REFERENCES=false
```

Use `--analysis-mode full` when CodeGraph needs richer TypeScript resolution and symbol signatures. Full mode is still static analysis. It cannot prove runtime-only relations created through dynamic imports, computed property access, dependency injection containers, generated code that is not checked in, or framework behavior that only exists at runtime. Symbol relationship traversal remains guarded by `CODEGRAPH_SYMBOL_RELATION_LIMIT` because that pass is too memory-heavy on large repositories; set `CODEGRAPH_FORCE_SYMBOL_RELATIONSHIPS=true` only for smaller repos, higher-memory runs, or targeted debugging. Raw identifier references are also disabled by default; set `CODEGRAPH_IDENTIFIER_REFERENCES=true` only when forced symbol relationships are already safe.

## Commands

- `codegraph doctor`: checks Neo4j connection and local extractor config.
- `codegraph reset`: deletes all graph data and ripples from Neo4j.
- `codegraph discover --repo .`: detects package manager, workspaces, and project types.
- `codegraph index --ripple my-app --repo . --language typescript`: creates or replaces a named ripple index for a repo.
- `codegraph update --ripple my-app`: re-indexes an existing ripple using its saved repo, language, and analysis mode.
- `codegraph status --ripple my-app`: shows node and relationship counts for one ripple.
- `codegraph ripples`: lists all indexed ripples in the database.
- `codegraph visualize --ripple my-app --output graph.html`: exports an HTML graph viewer for one ripple.
- `codegraph serve --addr :8080`: starts the HTTP MCP server with `/mcp/{ripple}` endpoints.
- `codegraph mcp --ripple my-app`: starts the stdio MCP server for one ripple.
- `codegraph test-extractor typescript`: validates the TypeScript extractor on the fixture repo.

## Ripples

A ripple is a named index inside the shared Neo4j database. Each ripple stores its repo path and language, and all graph nodes and relationships are scoped to that ripple.

```bash
codegraph index --ripple my-app --repo /path/to/repo --language typescript
codegraph update --ripple my-app
codegraph ripples
```

`update` reuses the stored repo path and language for the ripple, deletes only that ripple's existing graph, and rebuilds it. Other ripples in the same Neo4j database are left untouched.

The HTTP MCP endpoint is scoped by ripple name:

```text
http://localhost:8080/mcp/my-app
```

The stdio MCP command is equivalent:

```bash
codegraph mcp --ripple my-app
```

## OpenCode Installation

OpenCode should connect to an already running CodeGraph HTTP MCP server. Start the server first:

```bash
go run ./cmd/codegraph serve --addr :8080
```

Then add one remote MCP server per ripple you want OpenCode to use.

Example global config at `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "codegraph_my_app": {
      "type": "remote",
      "url": "http://localhost:8080/mcp/my-app",
      "enabled": true,
      "timeout": 15000
    }
  }
}
```

Then verify OpenCode can connect:

```bash
opencode mcp list
```

OpenCode should show the server as connected. In prompts, refer to the configured MCP name and call a task tool directly, for example `use codegraph_my_app, call prepare_feature_context with query billing`. Prefer high-level tools over `codegraph_help`; use help only when tool choice is unclear.

## Visualization

Generate a self-contained HTML visualization from the current Neo4j graph:

```bash
go run ./cmd/codegraph visualize --ripple my-app --output codegraph-visualization.html
```

The visualization plots every indexed node for one ripple on a canvas, groups nodes by label, supports search, and draws the local relationship neighborhood for the selected node. It is designed to remain usable on large graphs where a full force-directed SVG would be slow and unreadable.

## MCP Tools

See [When To Use CodeGraph (And When Not To)](#when-to-use-codegraph-and-when-not-to) for how agents should combine this MCP with Read, Grep, bash, and git.

Results default to **compact summary text** to keep agent token use low. Pass `detail=files`, `detail=lines`, or `raw=true` only when you need more structure. Graph tools default to `depth=1` and `limit=20`. List outputs hard-cap at `maxItems` (default 20) and print `... +N more` when truncated.

### High-level (prefer these)

- `prepare_feature_context`: one-call planning pack for a single feature/symbol query. Prefer `prepare_change_plan` for multi-target work.
- `prepare_change_plan`: **multi-target** plan from `symbols[]` and/or `paths[]` (or `useDirty=true`). Returns `mustEdit`, `mustVerify`, `suggestedOrder`, `openNext`, `packages`, `confidence`, `needsDisambiguation`.
- `resolve_symbol`: disambiguate a symbol name to ranked graph candidates (`package`, `pathPrefix`, or `symbolId`).
- `analyze_function_impact`: **hybrid** blast radius (graph `CALLS`/`IMPORTS_SYMBOL` + filesystem text residual). Returns `resolutionMethod`, `confidence`, `needsDisambiguation`, `graphReliable`, `stale`.
- `analyze_path_set_impact`: blast radius for a **path set** (graph file deps + text importers/tests). Supports `useDirty`.
- `analyze_rename_impact`: rename/migration impact grouped by runtime/config/tests/docs/scripts.
- `analyze_callsite_contract`: find call sites missing a required pre-call check.
- `find_env_usages`: runtime `process.env.NAME` reads only.
- `count_literal_files`: exact string file counts and paths.
- `reindex`: full ripple rebuild from MCP (same as `codegraph update`). Not file-incremental yet; `paths`/`useDirty` are advisory for follow-up impact. Long-running (`timeoutSec`, default 300).

### Graph / source (advanced)

- `search_code`, `find_symbol`, `find_file`: indexed graph search.
- `get_relations`: graph traversal for a known node id (keep depth/limit low).
- `open_file_excerpt`, `open_symbol_body`: source text after paths are known.
- `get_index_freshness`: dirty tree, relation counts, `graphReliable` / `stale`.
- `codegraph_help`: short router only; do not call this first on every task.

Hidden aliases still callable but not advertised: `get_ripple_info`, `list_node_types`, `get_dependencies`, `get_dependents`, `find_paths`, `get_impact`, `get_route_impact`, `get_related_tests`, `search_literal`.

**Hybrid impact policy:** when `needsDisambiguation=true`, rerun with `package`, `pathPrefix`, or `symbolId` from `resolve_symbol` before broad edits. When `graphReliable=false` or `stale=true`, call `reindex` or trust text residual only.


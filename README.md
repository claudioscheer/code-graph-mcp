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

For large repositories, the extractor stays bounded by using lightweight relative import resolution, skipping full symbol relationship traversal, skipping dependency-cruiser validation, and omitting symbol signatures. These limits are configurable:

```bash
CODEGRAPH_NODE_OPTIONS=--max-old-space-size=6144
CODEGRAPH_SYMBOL_RELATION_LIMIT=750
CODEGRAPH_DEPCRUISE_FILE_LIMIT=1500
```

## Commands

- `codegraph doctor`: checks Neo4j connection and local extractor config.
- `codegraph reset`: deletes all graph data and ripples from Neo4j.
- `codegraph discover --repo .`: detects package manager, workspaces, and project types.
- `codegraph index --ripple my-app --repo . --language typescript`: creates or replaces a named ripple index for a repo.
- `codegraph update --ripple my-app`: re-indexes an existing ripple using its saved repo and language.
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

OpenCode supports local MCP servers in `opencode.json` under the `mcp` key. Add one server per ripple you want OpenCode to use.

Example global config at `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "codegraph_my_app": {
      "type": "local",
      "command": [
        "go",
        "run",
        "./cmd/codegraph",
        "mcp",
        "--ripple",
        "my-app"
      ],
      "cwd": "/path/to/code-graph-mcp",
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

OpenCode should show the server as connected. In prompts, refer to the configured MCP name, for example `use codegraph_my_app to find the files related to auth`.

## Visualization

Generate a self-contained HTML visualization from the current Neo4j graph:

```bash
go run ./cmd/codegraph visualize --ripple my-app --output codegraph-visualization.html
```

The visualization plots every indexed node for one ripple on a canvas, groups nodes by label, supports search, and draws the local relationship neighborhood for the selected node. It is designed to remain usable on large graphs where a full force-directed SVG would be slow and unreadable.

## MCP Tools

- `find_symbol`
- `find_file`
- `get_dependencies`
- `get_dependents`
- `get_relations`
- `get_impact`
- `get_route_impact`
- `get_related_tests`
- `find_paths`
- `open_symbol_body`
- `open_file_excerpt`

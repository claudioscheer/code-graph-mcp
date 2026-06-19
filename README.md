# Code Graph MCP

Local code graph MCP, designed as a modular multi-language system.

The Go process owns CLI, project discovery, plugin orchestration, Neo4j ingestion/querying, and MCP. The TypeScript extractor is a Node subprocess that uses `ts-morph`, TypeScript Compiler API data, dependency-cruiser validation, and custom Next.js route extraction. Future language support plugs into the same GraphEvent NDJSON protocol.

## Quick Start

```bash
cp .env.example .env
pnpm install
docker compose up -d neo4j
go run ./cmd/codegraph doctor
go run ./cmd/codegraph reset
go run ./cmd/codegraph index --repo /path/to/repo --language typescript
go run ./cmd/codegraph status
go run ./cmd/codegraph visualize --output codegraph-visualization.html
go run ./cmd/codegraph mcp --repo /path/to/repo
```

Docker-only for this repo:

```bash
docker compose --profile app run --rm app index --repo /repo --language typescript
```

Docker-only for another local repo:

```bash
docker compose --profile app run --rm -v /path/to/repo:/target:ro app index --repo /target --language typescript
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

```bash
codegraph doctor
codegraph reset
codegraph discover --repo .
codegraph index --repo . --language typescript
codegraph status
codegraph visualize --output codegraph-visualization.html
codegraph test-extractor typescript
codegraph mcp --repo .
```

## Visualization

Generate a self-contained HTML visualization from the current Neo4j graph:

```bash
go run ./cmd/codegraph visualize --output codegraph-visualization.html
```

The visualization plots every indexed node on a canvas, groups nodes by label, supports search, and draws the local relationship neighborhood for the selected node. It is designed to remain usable on large graphs where a full force-directed SVG would be slow and unreadable.

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

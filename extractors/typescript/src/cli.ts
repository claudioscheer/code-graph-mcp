import { performance } from "node:perf_hooks";
import { parseArgs } from "./core/args.js";
import { EventBuffer } from "./core/emit.js";
import { summary, warning } from "./core/events.js";
import { loadGitIgnore } from "./core/gitignore.js";
import { extractWorkspace } from "./extract/workspace.js";
import { emitFiles, loadProject } from "./extract/files.js";
import { extractImports } from "./extract/imports.js";
import { extractSymbols, extractSymbolRelationships } from "./extract/symbols.js";
import { extractTests } from "./extract/tests.js";
import { extractNextRoutes } from "./next/routes.js";
import { dependencyCruiserWarnings } from "./validation/dependencyCruiser.js";

process.stdout.on("error", (error: NodeJS.ErrnoException) => {
  if (error.code === "EPIPE") {
    process.exit(0);
  }
  throw error;
});

async function main(): Promise<void> {
  const started = performance.now();
  const args = parseArgs();
  const events = new EventBuffer();
  const symbolRelationshipLimit = intEnv("CODEGRAPH_SYMBOL_RELATION_LIMIT", 750);
  const dependencyCruiserLimit = intEnv("CODEGRAPH_DEPCRUISE_FILE_LIMIT", 1500);
  const phase = phaseLogger(started);

  phase("gitignore:start");
  const gitignore = await loadGitIgnore(args.repo);
  phase("gitignore:done", { files: gitignore.files.length });

  phase("workspace:start");
  const packages = await extractWorkspace(args.repo, events, gitignore);
  phase("workspace:done", { packages: packages.length });

  phase("project:start");
  const { files: sourceFiles } = await loadProject(args.repo, gitignore);
  const files = emitFiles(args.repo, sourceFiles, packages, events);
  phase("project:done", { files: files.length });

  phase("imports:start");
  const richResolution = files.length <= symbolRelationshipLimit;
  extractImports(args.repo, files, packages, events, { richResolution });
  phase("imports:done", { richResolution });

  phase("symbols:start");
  const symbolIndex = extractSymbols(args.repo, files, events, { includeSignature: richResolution });
  phase("symbols:done", { symbols: symbolIndex.size, includeSignature: richResolution });

  if (files.length <= symbolRelationshipLimit) {
    phase("symbol-relationships:start", { limit: symbolRelationshipLimit });
    extractSymbolRelationships(args.repo, files, symbolIndex, events);
    phase("symbol-relationships:done");
  } else {
    events.add(warning("symbol relationship extraction skipped for large repo", { files: files.length, limit: symbolRelationshipLimit }));
    phase("symbol-relationships:skipped", { files: files.length, limit: symbolRelationshipLimit });
  }

  phase("tests:start");
  extractTests(files, events);
  phase("tests:done");

  phase("next-routes:start");
  extractNextRoutes(files, packages, events);
  phase("next-routes:done");

  if (files.length <= dependencyCruiserLimit) {
    phase("dependency-cruiser:start", { limit: dependencyCruiserLimit });
    for (const event of dependencyCruiserWarnings(args.repo)) {
      events.add(event);
    }
    phase("dependency-cruiser:done");
  } else {
    events.add(warning("dependency-cruiser validation skipped for large repo", { files: files.length, limit: dependencyCruiserLimit }));
    phase("dependency-cruiser:skipped", { files: files.length, limit: dependencyCruiserLimit });
  }

  events.add(summary({ events: events.all().length, durationMs: Math.round(performance.now() - started) }));
  phase("write:start", { events: events.all().length });
  events.writeStdout();
  phase("write:done");
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});

function intEnv(name: string, fallback: number): number {
  const raw = process.env[name];
  if (!raw) return fallback;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
}

function phaseLogger(started: number): (name: string, props?: Record<string, unknown>) => void {
  return (name, props = {}) => {
    const elapsedMs = Math.round(performance.now() - started);
    console.error(JSON.stringify({ source: "typescript-extractor", phase: name, elapsedMs, ...props }));
  };
}

import { describe, expect, test } from "vitest";
import path from "node:path";
import { EventBuffer } from "../core/emit.js";
import { extractWorkspace } from "./workspace.js";
import { emitFiles, loadProject } from "./files.js";
import { extractImports } from "./imports.js";
import { extractSymbols, extractSymbolRelationships } from "./symbols.js";
import { extractTests } from "./tests.js";
import { extractNextRoutes } from "../next/routes.js";
import { loadGitIgnore } from "../core/gitignore.js";

const fixtureRoot = path.resolve("testdata/fixtures/typescript/next-app");

describe("typescript extractor", () => {
  test("emits stable graph events for a Next.js fixture", async () => {
    const events = new EventBuffer();
    const gitignore = await loadGitIgnore(fixtureRoot);
    const packages = await extractWorkspace(fixtureRoot, events, gitignore);
    const { files: sourceFiles } = await loadProject(fixtureRoot, gitignore);
    const files = emitFiles(fixtureRoot, sourceFiles, packages, events);
    extractImports(fixtureRoot, files, packages, events);
    const symbols = extractSymbols(fixtureRoot, files, events);
    extractSymbolRelationships(fixtureRoot, files, symbols, events);
    extractTests(files, events);
    extractNextRoutes(files, packages, events);

    const all = events.all();
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "Package", id: "package:@repo/web" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "File", id: "file:packages/auth/src/session.ts" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "Symbol", id: "symbol:packages/auth/src/session.ts#getSession" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "Route", id: "route:apps/web:PAGE:/dashboard" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "IMPORTS_FILE", from: "file:apps/web/src/app/dashboard/page.tsx", to: "file:packages/auth/src/session.ts" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "USES_CONFIG_KEY", from: "file:packages/auth/src/session.ts", to: "config:AUTH_SECRET" }));
    expect(all).not.toContainEqual(expect.objectContaining({ id: "file:ignored-generated/ignored.ts" }));
  });
});

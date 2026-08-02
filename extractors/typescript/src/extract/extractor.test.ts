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
    extractSymbolRelationships(fixtureRoot, files, symbols, events, { includeIdentifierReferences: true });
    extractTests(files, events);
    extractNextRoutes(files, packages, events);

    const all = events.all();
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "Package", id: "package:@repo/web" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "File", id: "file:packages/auth/src/session.ts" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "Symbol", id: "symbol:packages/auth/src/session.ts#getSession" }));
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "node",
        label: "Symbol",
        id: "symbol:packages/auth/src/session.ts#SESSION_TTL_MS",
        props: expect.objectContaining({ kind: "global_variable", scope: "module", code: expect.stringContaining("SESSION_TTL_MS") }),
      }),
    );
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "node",
        label: "Symbol",
        id: "symbol:packages/auth/src/session.ts#normalizeUser",
        props: expect.objectContaining({ kind: "function_variable", code: expect.stringContaining("normalizeUser") }),
      }),
    );
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "node",
        label: "Symbol",
        id: "symbol:packages/auth/src/session.ts#normalizeUser::param:user:12",
        props: expect.objectContaining({ kind: "parameter", ownerId: "symbol:packages/auth/src/session.ts#normalizeUser" }),
      }),
    );
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "node",
        label: "Symbol",
        id: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage::var:session:4",
        props: expect.objectContaining({ kind: "local_variable", ownerId: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage" }),
      }),
    );
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "node",
        label: "Symbol",
        id: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage::var:fallback:5",
        props: expect.objectContaining({ kind: "local_variable", ownerId: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage" }),
      }),
    );
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "node",
        label: "Symbol",
        id: "symbol:packages/auth/src/session.ts#getSession",
        props: expect.objectContaining({ code: expect.stringContaining("getSession") }),
      }),
    );
    expect(all).toContainEqual(expect.objectContaining({ type: "node", label: "Route", id: "route:apps/web:PAGE:/dashboard" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "IMPORTS_FILE", from: "file:apps/web/src/app/dashboard/page.tsx", to: "file:packages/auth/src/session.ts" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "IMPORTS_SYMBOL", from: "file:apps/web/src/app/dashboard/page.tsx", to: "symbol:packages/auth/src/session.ts#getSession" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "IMPORTS_SYMBOL", from: "file:apps/web/src/app/dashboard/page.tsx", to: "symbol:packages/auth/src/session.ts#SESSION_TTL_MS" }));
    const dashboardGetSessionCalls = all.filter(
      (event) =>
        event.type === "edge" &&
        event.rel === "CALLS" &&
        event.from === "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage" &&
        event.to === "symbol:packages/auth/src/session.ts#getSession",
    );
    expect(dashboardGetSessionCalls).toEqual([
      expect.objectContaining({ props: expect.objectContaining({ sourceFile: "apps/web/src/app/dashboard/page.tsx", startLine: 4, endLine: 4 }) }),
      expect.objectContaining({ props: expect.objectContaining({ sourceFile: "apps/web/src/app/dashboard/page.tsx", startLine: 5, endLine: 5 }) }),
    ]);
    expect(all).toContainEqual(
      expect.objectContaining({
        type: "edge",
        rel: "REFERENCES",
        from: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage",
        to: "symbol:packages/auth/src/session.ts#SESSION_TTL_MS",
        props: expect.objectContaining({ sourceFile: "apps/web/src/app/dashboard/page.tsx", startLine: 6, endLine: 6 }),
      }),
    );
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "HAS_PARAMETER", from: "symbol:packages/auth/src/session.ts#normalizeUser", to: "symbol:packages/auth/src/session.ts#normalizeUser::param:user:12" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "DECLARES_VARIABLE", from: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage", to: "symbol:apps/web/src/app/dashboard/page.tsx#DashboardPage::var:session:4" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "CALLS", from: "symbol:packages/auth/src/session.ts#getSession", to: "symbol:packages/auth/src/session.ts#normalizeUser" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "IMPORTS_EXTERNAL_PACKAGE", from: "file:packages/auth/src/session.test.ts", to: "external:npm:vitest" }));
    expect(all).toContainEqual(expect.objectContaining({ type: "edge", rel: "USES_CONFIG_KEY", from: "file:packages/auth/src/session.ts", to: "config:AUTH_SECRET" }));
    expect(all).not.toContainEqual(expect.objectContaining({ id: "file:ignored-generated/ignored.ts" }));
  });
});

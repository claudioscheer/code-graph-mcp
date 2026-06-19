import path from "node:path";
import { edge, node, stableFileId, stableRouteId, stableSymbolId } from "../core/events.js";
import type { EventBuffer } from "../core/emit.js";
import type { FileInfo } from "../extract/files.js";
import type { WorkspacePackage } from "../extract/workspace.js";

export function extractNextRoutes(files: FileInfo[], packages: WorkspacePackage[], events: EventBuffer): void {
  const fileByPath = new Map(files.map((file) => [file.path, file]));
  for (const file of files) {
    const route = routeFromPath(file.path, packages);
    if (!route) continue;
    events.add(
      node("Route", route.id, {
        routePath: route.routePath,
        filePath: file.path,
        routerKind: route.routerKind,
        httpMethod: route.method,
        projectRoot: route.projectRoot,
      }),
    );
    events.add(edge("ROUTE_FILE", route.id, file.id, meta(file.path, 1, "nextjs_route_file", 1)));
    if (file.path.endsWith("/page.tsx") || file.path.endsWith("/page.ts")) {
      events.add(edge("USES_PAGE", route.id, file.id, meta(file.path, 1, "nextjs_page_file", 1)));
    }
    for (const layout of layoutChain(file.path, fileByPath)) {
      events.add(edge("USES_LAYOUT", route.id, stableFileId(layout.path), meta(file.path, 1, "nextjs_layout_chain", 0.9)));
    }
    if (file.path.endsWith("/route.ts") || file.path.endsWith("/route.tsx")) {
      for (const method of ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]) {
        if (file.sourceFile.getFunction(method)) {
          events.add(edge("HANDLES_ROUTE", stableSymbolId(file.path, method), route.id, meta(file.path, file.sourceFile.getFunctionOrThrow(method).getStartLineNumber(), "nextjs_route_handler", 1)));
        }
      }
    }
  }
}

function routeFromPath(filePath: string, packages: WorkspacePackage[]): { id: string; routePath: string; method: string; routerKind: string; projectRoot: string } | undefined {
  const pkg = packages.filter((candidate) => filePath.startsWith(`${candidate.root}/`) || candidate.root === ".").sort((a, b) => b.root.length - a.root.length)[0];
  const projectRoot = pkg?.root ?? ".";
  const appMarker = "/app/";
  const appIndex = filePath.indexOf(appMarker);
  if (appIndex >= 0 && /\/(page|route)\.(ts|tsx)$/.test(filePath)) {
    const afterApp = filePath.slice(appIndex + appMarker.length).replace(/\/(page|route)\.(ts|tsx)$/, "");
    const parts = afterApp.split("/").filter((part) => part && !part.startsWith("(") && !part.startsWith("@"));
    const routePath = `/${parts.join("/")}`.replace(/\/$/, "") || "/";
    const method = filePath.endsWith("/route.ts") || filePath.endsWith("/route.tsx") ? "ANY" : "PAGE";
    return { id: stableRouteId(projectRoot, method, routePath), routePath, method, routerKind: "app", projectRoot };
  }
  const pagesMarker = "/pages/";
  const pagesIndex = filePath.indexOf(pagesMarker);
  if (pagesIndex >= 0 && /\.(ts|tsx|js|jsx)$/.test(filePath)) {
    const afterPages = filePath.slice(pagesIndex + pagesMarker.length).replace(/\.(ts|tsx|js|jsx)$/, "");
    const isAPI = afterPages.startsWith("api/");
    const routePath = `/${afterPages.replace(/(^|\/)index$/, "").replace(/\[(.+?)\]/g, ":$1")}`.replace(/\/$/, "") || "/";
    return { id: stableRouteId(projectRoot, isAPI ? "ANY" : "PAGE", routePath), routePath, method: isAPI ? "ANY" : "PAGE", routerKind: isAPI ? "api" : "pages", projectRoot };
  }
  return undefined;
}

function layoutChain(filePath: string, fileByPath: Map<string, FileInfo>): FileInfo[] {
  const layouts: FileInfo[] = [];
  let dir = path.posix.dirname(filePath);
  while (dir.includes("/app")) {
    for (const name of ["layout.tsx", "layout.ts"]) {
      const layout = fileByPath.get(`${dir}/${name}`);
      if (layout) layouts.push(layout);
    }
    if (dir.endsWith("/app")) break;
    dir = path.posix.dirname(dir);
  }
  return layouts;
}

function meta(sourceFile: string, line: number, reason: string, confidence = 0.9): Record<string, unknown> {
  return { source: "nextjs", sourceFile, startLine: line, endLine: line, confidence, reason, extractor: "typescript", language: "typescript" };
}

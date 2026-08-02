import { readFileSync } from "node:fs";
import path from "node:path";
import fg from "fast-glob";
import { Project, SourceFile } from "ts-morph";
import { edge, node, stableFileId, stableProjectId } from "../core/events.js";
import type { EventBuffer } from "../core/emit.js";
import { normalizePath, relativePath } from "../core/fs.js";
import type { GitIgnoreMatcher } from "../core/gitignore.js";
import { packageForFile, type WorkspacePackage } from "./workspace.js";

export type FileInfo = {
  id: string;
  path: string;
  sourceFile: SourceFile;
  packageId?: string;
};

export async function loadProject(repo: string, gitignore?: GitIgnoreMatcher): Promise<{ project: Project; files: SourceFile[] }> {
  // Avoid tsConfig + addSourceFilesAtPaths directory walks. Yarn/pnpm workspace
  // monorepos can nest circular node_modules links deep enough to hit ENAMETOOLONG.
  // Create files from our own glob so ts-morph never scandir's node_modules.
  const project = new Project({
    skipAddingFilesFromTsConfig: true,
    skipLoadingLibFiles: true,
    useInMemoryFileSystem: false,
    compilerOptions: {
      allowJs: true,
      checkJs: false,
      jsx: 4,
      skipLibCheck: true,
      noResolve: true,
    },
  });
  const paths = await fg(["**/*.{ts,tsx,js,jsx}"], {
    cwd: repo,
    absolute: true,
    ignore: [
      "**/node_modules/**",
      "**/.next/**",
      "**/dist/**",
      "**/build/**",
      "**/coverage/**",
      "**/.turbo/**",
      "**/.cache/**",
      "**/*.d.ts",
    ],
    onlyFiles: true,
    dot: false,
    followSymbolicLinks: false,
  });
  const files: SourceFile[] = [];
  for (const file of paths) {
    const rel = relativePath(repo, file);
    if (rel.split(path.sep).includes("node_modules") || rel.split("/").includes("node_modules")) {
      continue;
    }
    if (gitignore?.ignored(rel)) {
      continue;
    }
    let text: string;
    try {
      text = readFileSync(file, "utf8");
    } catch {
      continue;
    }
    // createSourceFile does not walk parent directories looking for more files.
    files.push(project.createSourceFile(file, text, { overwrite: true, scriptKind: scriptKindForPath(file) }));
  }
  return { project, files };
}

function scriptKindForPath(filePath: string): number | undefined {
  const ext = path.extname(filePath).toLowerCase();
  switch (ext) {
    case ".ts":
      return 3; // ScriptKind.TS
    case ".tsx":
      return 4; // ScriptKind.TSX
    case ".js":
      return 1; // ScriptKind.JS
    case ".jsx":
      return 2; // ScriptKind.JSX
    default:
      return undefined;
  }
}

export function emitFiles(repo: string, files: SourceFile[], packages: WorkspacePackage[], events: EventBuffer): FileInfo[] {
  return files.map((sourceFile) => {
    const rel = relativePath(repo, sourceFile.getFilePath());
    const pkg = packageForFile(rel, packages);
    const kind = fileKind(rel);
    const id = stableFileId(rel);
    events.add(node("File", id, { path: rel, language: sourceFile.getExtension().replace(".", "") || "typescript", kind, packageId: pkg?.id }));
    if (pkg) {
      events.add(edge("CONTAINS_FILE", pkg.id, id, meta(rel, 1, "package_contains_file", 1)));
      events.add(edge("CONTAINS_FILE", stableProjectId(pkg.root), id, meta(rel, 1, "project_contains_file", 1)));
    }
    return { id, path: normalizePath(rel), sourceFile, packageId: pkg?.id };
  });
}

export function fileKind(rel: string): string {
  const base = path.posix.basename(rel);
  if (base === "page.tsx" || base === "page.ts") return "page";
  if (base === "layout.tsx" || base === "layout.ts") return "layout";
  if (base === "route.ts" || base === "route.tsx") return "route";
  if (rel.includes("__tests__/") || rel.includes(".test.") || rel.includes(".spec.")) return "test";
  if (base.includes("config.")) return "config";
  if (rel.endsWith(".tsx") || rel.endsWith(".jsx")) return "component";
  return "source";
}

function meta(sourceFile: string, line: number, reason: string, confidence = 0.8): Record<string, unknown> {
  return { source: "typescript", sourceFile, startLine: line, endLine: line, confidence, reason, extractor: "typescript", language: "typescript" };
}

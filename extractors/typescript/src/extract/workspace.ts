import path from "node:path";
import fg from "fast-glob";
import { edge, node, stableExternalId, stablePackageId, stableProjectId } from "../core/events.js";
import { normalizePath, packageNameFromSpecifier, readJSON } from "../core/fs.js";
import type { GitIgnoreMatcher } from "../core/gitignore.js";
import type { EventBuffer } from "../core/emit.js";

type PackageJSON = {
  name?: string;
  workspaces?: string[];
  dependencies?: Record<string, string>;
  devDependencies?: Record<string, string>;
};

export type WorkspacePackage = {
  id: string;
  name: string;
  root: string;
  packageJsonPath: string;
  dependencies: Record<string, string>;
};

export async function extractWorkspace(repo: string, events: EventBuffer, gitignore?: GitIgnoreMatcher): Promise<WorkspacePackage[]> {
  const rootPackage = readJSON<PackageJSON>(path.join(repo, "package.json"));
  const patterns = rootPackage?.workspaces?.length ? rootPackage.workspaces : ["apps/*", "packages/*"];
  const packageJsons = await fg(["package.json", ...patterns.map((pattern) => `${pattern}/package.json`)], {
    cwd: repo,
    ignore: ["**/node_modules/**", "**/.next/**", "**/dist/**"],
    onlyFiles: true,
  });

  const packages: WorkspacePackage[] = [];
  for (const packageJsonPath of packageJsons.filter((file) => !gitignore?.ignored(file)).sort()) {
    const parsed = readJSON<PackageJSON>(path.join(repo, packageJsonPath));
    if (!parsed) continue;
    const root = normalizePath(path.dirname(packageJsonPath));
    const name = parsed.name ?? root;
    const dependencies = { ...(parsed.dependencies ?? {}), ...(parsed.devDependencies ?? {}) };
    const pkg: WorkspacePackage = {
      id: stablePackageId(name),
      name,
      root,
      packageJsonPath,
      dependencies,
    };
    packages.push(pkg);
    events.add(
      node("Package", pkg.id, {
        name,
        root,
        kind: root === "." || root.startsWith("apps/") ? "app" : root.startsWith("packages/") ? "package" : "unknown",
        packageJsonPath,
        language: "typescript",
      }),
    );
    events.add(node("Project", stableProjectId(root), { root, language: "typescript", packageId: pkg.id }));
    events.add(edge("CONTAINS_PACKAGE", stableProjectId(root), pkg.id, meta(packageJsonPath, 1, "workspace_package")));
  }

  const byName = new Map(packages.map((pkg) => [pkg.name, pkg]));
  for (const pkg of packages) {
    for (const [specifier, version] of Object.entries(pkg.dependencies)) {
      const packageName = packageNameFromSpecifier(specifier);
      const target = byName.get(packageName);
      if (target) {
        events.add(edge("DEPENDS_ON_PACKAGE", pkg.id, target.id, meta(pkg.packageJsonPath, 1, "package_json_workspace_dependency", 1)));
      } else {
        const externalId = stableExternalId("npm", packageName);
        events.add(node("ExternalPackage", externalId, { name: packageName, version, ecosystem: "npm", specifier }));
        events.add(edge("USES_EXTERNAL_PACKAGE", pkg.id, externalId, meta(pkg.packageJsonPath, 1, "package_json_external_dependency", 1)));
      }
    }
  }

  return packages;
}

export function packageForFile(filePath: string, packages: WorkspacePackage[]): WorkspacePackage | undefined {
  return packages
    .filter((pkg) => filePath === pkg.root || filePath.startsWith(`${pkg.root}/`) || pkg.root === ".")
    .sort((a, b) => b.root.length - a.root.length)[0];
}

function meta(sourceFile: string, line: number, reason: string, confidence = 1): Record<string, unknown> {
  return { source: "typescript", sourceFile, startLine: line, endLine: line, confidence, reason, extractor: "typescript", language: "typescript" };
}

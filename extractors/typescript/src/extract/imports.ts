import { Node, SyntaxKind } from "ts-morph";
import path from "node:path";
import { edge, stableFileId, stablePackageId, stableSymbolId } from "../core/events.js";
import type { EventBuffer } from "../core/emit.js";
import { packageNameFromSpecifier, relativePath } from "../core/fs.js";
import type { FileInfo } from "./files.js";
import type { WorkspacePackage } from "./workspace.js";

export type ImportOptions = {
  richResolution: boolean;
};

export function extractImports(repo: string, files: FileInfo[], packages: WorkspacePackage[], events: EventBuffer, options: ImportOptions = { richResolution: true }): void {
  const workspaceNames = new Set(packages.map((pkg) => pkg.name));
  const filePaths = new Set(files.map((file) => file.path));
  for (const file of files) {
    for (const declaration of file.sourceFile.getImportDeclarations()) {
      const specifier = declaration.getModuleSpecifierValue();
      const targetSourceFile = options.richResolution ? declaration.getModuleSpecifierSourceFile() : undefined;
      if (targetSourceFile) {
        const targetPath = relativePath(repo, targetSourceFile.getFilePath());
        events.add(edge("IMPORTS_FILE", file.id, stableFileId(targetPath), meta(file.path, declaration.getStartLineNumber(), "ts_morph_import_resolved", 1)));
      } else if (specifier.startsWith(".")) {
        const targetPath = resolveRelativeImport(file.path, specifier, filePaths);
        if (targetPath) {
          events.add(edge("IMPORTS_FILE", file.id, stableFileId(targetPath), meta(file.path, declaration.getStartLineNumber(), "relative_import_resolved", 0.85)));
        }
      } else {
        emitPackageImport(file, specifier, workspaceNames, events, declaration.getStartLineNumber());
      }
      if (!options.richResolution) continue;
      for (const named of declaration.getNamedImports()) {
        const name = named.getNameNode().getText();
        const declarations = named.getNameNode().getSymbol()?.getDeclarations() ?? [];
        for (const target of declarations) {
          const targetFile = target.getSourceFile();
          if (targetFile.isFromExternalLibrary()) continue;
          const targetPath = relativePath(repo, targetFile.getFilePath());
          events.add(edge("IMPORTS_SYMBOL", file.id, stableSymbolId(targetPath, name), meta(file.path, named.getStartLineNumber(), "typescript_import_symbol_resolved", 1)));
        }
      }
    }
    for (const declaration of file.sourceFile.getExportDeclarations()) {
      const specifier = declaration.getModuleSpecifierValue();
      const targetSourceFile = options.richResolution ? declaration.getModuleSpecifierSourceFile() : undefined;
      if (targetSourceFile) {
        const targetPath = relativePath(repo, targetSourceFile.getFilePath());
        events.add(edge("RE_EXPORTS_FILE", file.id, stableFileId(targetPath), meta(file.path, declaration.getStartLineNumber(), "ts_morph_re_export_resolved", 1)));
      } else if (specifier?.startsWith(".")) {
        const targetPath = resolveRelativeImport(file.path, specifier, filePaths);
        if (targetPath) {
          events.add(edge("RE_EXPORTS_FILE", file.id, stableFileId(targetPath), meta(file.path, declaration.getStartLineNumber(), "relative_re_export_resolved", 0.85)));
        }
      }
    }
    file.sourceFile.forEachDescendant((node) => {
      if (Node.isCallExpression(node) && node.getExpression().getKind() === SyntaxKind.ImportKeyword) {
        const [arg] = node.getArguments();
        if (Node.isStringLiteral(arg)) {
          const target = arg.getLiteralText();
          events.add(edge("DYNAMIC_IMPORTS_FILE", file.id, stableFileId(target), meta(file.path, node.getStartLineNumber(), "dynamic_import_literal", 0.8)));
        }
      }
    });
  }
}

function resolveRelativeImport(fromPath: string, specifier: string, filePaths: Set<string>): string | undefined {
  const base = path.posix.normalize(path.posix.join(path.posix.dirname(fromPath), specifier));
  const candidates = [
    base,
    `${base}.ts`,
    `${base}.tsx`,
    `${base}.js`,
    `${base}.jsx`,
    path.posix.join(base, "index.ts"),
    path.posix.join(base, "index.tsx"),
    path.posix.join(base, "index.js"),
    path.posix.join(base, "index.jsx"),
  ];
  return candidates.find((candidate) => filePaths.has(candidate));
}

function emitPackageImport(file: FileInfo, specifier: string, workspaceNames: Set<string>, events: EventBuffer, line: number): void {
  if (specifier.startsWith(".")) return;
  const packageName = packageNameFromSpecifier(specifier);
  if (!workspaceNames.has(packageName)) return;
  events.add(edge("DEPENDS_ON_PACKAGE", file.packageId ?? stableFileId(file.path), stablePackageId(packageName), meta(file.path, line, "unresolved_source_package_import", 0.6)));
}

function meta(sourceFile: string, line: number, reason: string, confidence: number): Record<string, unknown> {
  return { source: "typescript", sourceFile, startLine: line, endLine: line, confidence, reason, extractor: "typescript", language: "typescript" };
}

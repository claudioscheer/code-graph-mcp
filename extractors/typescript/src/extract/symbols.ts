import { Node, SourceFile } from "ts-morph";
import { edge, node, stableConfigId, stableSymbolId } from "../core/events.js";
import type { EventBuffer } from "../core/emit.js";
import { relativePath } from "../core/fs.js";
import type { FileInfo } from "./files.js";

export type SymbolIndex = Map<string, string>;

export type SymbolOptions = {
  includeSignature: boolean;
};

export function extractSymbols(repo: string, files: FileInfo[], events: EventBuffer, options: SymbolOptions = { includeSignature: true }): SymbolIndex {
  const byDeclaration = new Map<string, string>();
  for (const file of files) {
    const sourceFile = file.sourceFile;
    for (const declaration of topLevelDeclarations(sourceFile)) {
      const name = declarationName(declaration);
      if (!name) continue;
      const id = stableSymbolId(file.path, name);
      byDeclaration.set(declarationKey(declaration), id);
      const kind = symbolKind(name, declaration);
      events.add(
        node("Symbol", id, {
          name,
          kind,
          language: "typescript",
          filePath: file.path,
          packageId: file.packageId,
          startLine: declaration.getStartLineNumber(),
          endLine: declaration.getEndLineNumber(),
          exported: isExported(declaration),
          signature: options.includeSignature ? declaration.getText().split("\n")[0]?.trim() : undefined,
          confidence: 1,
        }),
      );
      events.add(edge("DEFINES", file.id, id, meta(file.path, declaration.getStartLineNumber(), declaration.getEndLineNumber(), "typescript_symbol_declaration", 1)));
      if (isExported(declaration)) {
        events.add(edge("EXPORTS_SYMBOL", file.id, id, meta(file.path, declaration.getStartLineNumber(), declaration.getEndLineNumber(), "typescript_exported_symbol", 1)));
      }
    }
  }
  return byDeclaration;
}

export function extractSymbolRelationships(repo: string, files: FileInfo[], symbolIndex: SymbolIndex, events: EventBuffer): void {
  for (const file of files) {
    file.sourceFile.forEachDescendant((current) => {
      if (Node.isPropertyAccessExpression(current) || Node.isIdentifier(current)) {
        const symbolId = symbolIdForNode(current, repo, symbolIndex);
        const owner = enclosingSymbolId(current, repo, symbolIndex);
        if (symbolId && owner && symbolId !== owner) {
          events.add(edge("REFERENCES", owner, symbolId, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_reference_resolved", 1)));
        }
      }
      if (Node.isCallExpression(current) || Node.isNewExpression(current)) {
        const expression = Node.isCallExpression(current) ? current.getExpression() : current.getExpression();
        const target = expression ? symbolIdForNode(expression, repo, symbolIndex) : undefined;
        const owner = enclosingSymbolId(current, repo, symbolIndex);
        if (target && owner && target !== owner) {
          events.add(edge(Node.isNewExpression(current) ? "INSTANTIATES" : "CALLS", owner, target, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_call_resolved", 1)));
        }
      }
      if (Node.isJsxOpeningElement(current) || Node.isJsxSelfClosingElement(current)) {
        const tag = current.getTagNameNode();
        const target = symbolIdForNode(tag, repo, symbolIndex);
        const owner = enclosingSymbolId(current, repo, symbolIndex);
        if (target && owner && target !== owner) {
          events.add(edge("RENDERS", owner, target, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "jsx_component_resolved", 1)));
        }
      }
      if (Node.isTypeReference(current)) {
        const target = symbolIdForNode(current.getTypeName(), repo, symbolIndex);
        const owner = enclosingSymbolId(current, repo, symbolIndex);
        if (target && owner && target !== owner) {
          events.add(edge("USES_TYPE", owner, target, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_type_reference_resolved", 1)));
        }
      }
      if (Node.isPropertyAccessExpression(current) && current.getExpression().getText() === "process.env") {
        const key = current.getName();
        const configId = stableConfigId(key);
        events.add(node("ConfigKey", configId, { key, source: "process.env" }));
        events.add(edge("USES_CONFIG_KEY", file.id, configId, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "process_env_static_key", 0.9)));
      }
    });
  }
}

function topLevelDeclarations(sourceFile: SourceFile): Node[] {
  const declarations: Node[] = [];
  for (const statement of sourceFile.getStatements()) {
    if (
      Node.isFunctionDeclaration(statement) ||
      Node.isClassDeclaration(statement) ||
      Node.isInterfaceDeclaration(statement) ||
      Node.isTypeAliasDeclaration(statement)
    ) {
      declarations.push(statement);
    }
    if (Node.isVariableStatement(statement)) {
      declarations.push(...statement.getDeclarations());
    }
    if (Node.isClassDeclaration(statement)) {
      declarations.push(...statement.getMethods());
    }
  }
  return declarations;
}

function declarationName(declaration: Node): string | undefined {
  if (
    Node.isFunctionDeclaration(declaration) ||
    Node.isClassDeclaration(declaration) ||
    Node.isInterfaceDeclaration(declaration) ||
    Node.isTypeAliasDeclaration(declaration) ||
    Node.isMethodDeclaration(declaration) ||
    Node.isVariableDeclaration(declaration)
  ) {
    const name = declaration.getName();
    if (name) return name;
  }
  if (Node.isFunctionDeclaration(declaration) && declaration.isDefaultExport()) return "default";
  return undefined;
}

function symbolKind(name: string, declaration: Node): string {
  if (Node.isClassDeclaration(declaration)) return "class";
  if (Node.isInterfaceDeclaration(declaration)) return "interface";
  if (Node.isTypeAliasDeclaration(declaration)) return "type";
  if (Node.isMethodDeclaration(declaration)) return "method";
  if (Node.isVariableDeclaration(declaration)) return /^[A-Z]/.test(name) ? "component" : "constant";
  if (/^[A-Z]/.test(name)) return "component";
  if (name.startsWith("use")) return "hook";
  return "function";
}

function isExported(declaration: Node): boolean {
  if (
    Node.isFunctionDeclaration(declaration) ||
    Node.isClassDeclaration(declaration) ||
    Node.isInterfaceDeclaration(declaration) ||
    Node.isTypeAliasDeclaration(declaration)
  ) {
    return declaration.isExported();
  }
  if (Node.isMethodDeclaration(declaration)) {
    return false;
  }
  if (Node.isVariableDeclaration(declaration)) {
    return declaration.getVariableStatement()?.isExported() ?? false;
  }
  return false;
}

function symbolIdForNode(node: Node, repo: string, symbolIndex: SymbolIndex): string | undefined {
  const declarations = node.getSymbol()?.getDeclarations() ?? node.getType().getSymbol()?.getDeclarations() ?? [];
  for (const declaration of declarations) {
    const id = symbolIndex.get(declarationKey(declaration));
    if (id) return id;
  }
  return undefined;
}

function enclosingSymbolId(node: Node, repo: string, symbolIndex: SymbolIndex): string | undefined {
  let current: Node | undefined = node;
  while (current) {
    if (symbolIndex.has(declarationKey(current))) return symbolIndex.get(declarationKey(current));
    current = current.getParent();
  }
  return undefined;
}

function declarationKey(node: Node): string {
  return `${node.getSourceFile().getFilePath()}:${node.getStart()}:${node.getEnd()}`;
}

function meta(sourceFile: string, startLine: number, endLine: number, reason: string, confidence: number): Record<string, unknown> {
  return { source: "typescript", sourceFile, startLine, endLine, confidence, reason, extractor: "typescript", language: "typescript" };
}

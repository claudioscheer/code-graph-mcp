import { Node, ParameterDeclaration, SourceFile } from "ts-morph";
import { edge, node, stableConfigId, stableSymbolId } from "../core/events.js";
import type { EventBuffer } from "../core/emit.js";
import { relativePath } from "../core/fs.js";
import type { FileInfo } from "./files.js";

export type SymbolIndex = Map<string, string>;

export type SymbolOptions = {
  includeSignature: boolean;
};

export type SymbolRelationshipOptions = {
  includeIdentifierReferences: boolean;
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
          scope: symbolScope(declaration),
          signature: options.includeSignature ? declaration.getText().split("\n")[0]?.trim() : undefined,
          code: options.includeSignature ? declarationCode(declaration) : undefined,
          confidence: 1,
        }),
      );
      events.add(edge("DEFINES", file.id, id, meta(file.path, declaration.getStartLineNumber(), declaration.getEndLineNumber(), "typescript_symbol_declaration", 1)));
      if (isExported(declaration)) {
        events.add(edge("EXPORTS_SYMBOL", file.id, id, meta(file.path, declaration.getStartLineNumber(), declaration.getEndLineNumber(), "typescript_exported_symbol", 1)));
      }
      emitParameters(file, declaration, id, byDeclaration, events, options);
      if (Node.isVariableDeclaration(declaration)) {
        const initializer = declaration.getInitializer();
        if (initializer) {
          emitParameters(file, initializer, id, byDeclaration, events, options);
        }
      }
    }
    emitLocalVariables(file, sourceFile, byDeclaration, events, options);
  }
  return byDeclaration;
}

export function extractSymbolRelationships(repo: string, files: FileInfo[], symbolIndex: SymbolIndex, events: EventBuffer, options: SymbolRelationshipOptions = { includeIdentifierReferences: false }): void {
  const seen = new Set<string>();
  const symbolsByName = uniqueSymbolsByName(symbolIndex);
  const addSymbolEdge = (rel: string, from: string, to: string, sourceFile: string, startLine: number, endLine: number, reason: string, confidence: number) => {
    const key = `${rel}\0${from}\0${to}\0${sourceFile}\0${startLine}\0${endLine}\0${reason}`;
    if (seen.has(key)) return;
    seen.add(key);
    events.add(edge(rel, from, to, meta(sourceFile, startLine, endLine, reason, confidence)));
  };
  for (const file of files) {
    file.sourceFile.forEachDescendant((current) => {
      if (options.includeIdentifierReferences && (Node.isPropertyAccessExpression(current) || Node.isIdentifier(current))) {
        const symbolId = symbolIdForNode(current, symbolIndex, symbolsByName);
        const owner = relationOwnerSymbolId(current, symbolIndex);
        if (symbolId && owner && symbolId !== owner) {
          addSymbolEdge("REFERENCES", owner, symbolId, file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_reference_resolved", 1);
        }
      }
      if (Node.isCallExpression(current) || Node.isNewExpression(current)) {
        const expression = Node.isCallExpression(current) ? current.getExpression() : current.getExpression();
        const target = expression ? symbolIdForNode(expression, symbolIndex, symbolsByName) : undefined;
        const owner = relationOwnerSymbolId(current, symbolIndex);
        if (target && owner && target !== owner) {
          addSymbolEdge(Node.isNewExpression(current) ? "INSTANTIATES" : "CALLS", owner, target, file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_call_text_resolved", 0.75);
        }
      }
      if (Node.isJsxOpeningElement(current) || Node.isJsxSelfClosingElement(current)) {
        const tag = current.getTagNameNode();
        const target = symbolIdForNode(tag, symbolIndex, symbolsByName);
        const owner = relationOwnerSymbolId(current, symbolIndex);
        if (target && owner && target !== owner) {
          addSymbolEdge("RENDERS", owner, target, file.path, current.getStartLineNumber(), current.getEndLineNumber(), "jsx_component_text_resolved", 0.75);
        }
      }
      if (Node.isTypeReference(current)) {
        const target = symbolIdForNode(current.getTypeName(), symbolIndex, symbolsByName);
        const owner = relationOwnerSymbolId(current, symbolIndex);
        if (target && owner && target !== owner) {
          addSymbolEdge("USES_TYPE", owner, target, file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_type_reference_text_resolved", 0.75);
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
  if (Node.isVariableDeclaration(declaration)) {
    const initializer = declaration.getInitializer();
    if (initializer && (Node.isArrowFunction(initializer) || Node.isFunctionExpression(initializer))) {
      if (/^[A-Z]/.test(name)) return "component";
      if (name.startsWith("use")) return "hook";
      return "function_variable";
    }
    if (initializer && Node.isClassExpression(initializer)) return "class";
    if (isTopLevelVariable(declaration)) return "global_variable";
    return "local_variable";
  }
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

function symbolIdForNode(node: Node, symbolIndex: SymbolIndex, symbolsByName: Map<string, string>): string | undefined {
  const symbol = node.getSymbol();
  const declarations = symbol?.getAliasedSymbol()?.getDeclarations() ?? symbol?.getDeclarations() ?? node.getType().getSymbol()?.getDeclarations() ?? [];
  for (const declaration of declarations) {
    const id = symbolIndex.get(declarationKey(declaration));
    if (id) return id;
  }
  const name = referencedName(node);
  if (!name) return undefined;
  return symbolsByName.get(name);
}

function referencedName(node: Node): string | undefined {
  if (Node.isIdentifier(node)) return node.getText();
  if (Node.isPropertyAccessExpression(node)) return node.getName();
  const text = node.getText();
  if (!text) return undefined;
  return text.split(".").at(-1);
}

function declarationCode(declaration: Node): string | undefined {
  if (
    Node.isFunctionDeclaration(declaration) ||
    Node.isClassDeclaration(declaration) ||
    Node.isMethodDeclaration(declaration)
  ) {
    return declaration.getText();
  }
  if (Node.isVariableDeclaration(declaration)) {
    const initializer = declaration.getInitializer();
    if (initializer && (Node.isArrowFunction(initializer) || Node.isFunctionExpression(initializer) || Node.isClassExpression(initializer))) {
      return boundedCode(declaration.getText(), 8000);
    }
    if (isTopLevelVariable(declaration)) {
      return boundedCode(declaration.getText(), 4000);
    }
  }
  return undefined;
}

function emitParameters(file: FileInfo, declaration: Node, ownerId: string, byDeclaration: SymbolIndex, events: EventBuffer, options: SymbolOptions): void {
  if (!hasParameters(declaration)) return;
  for (const parameter of declaration.getParameters()) {
    const name = parameter.getName();
    if (!name) continue;
    const id = childSymbolId(file.path, ownerId, "param", name, parameter.getStartLineNumber());
    byDeclaration.set(declarationKey(parameter), id);
    events.add(
      node("Symbol", id, {
        name,
        kind: "parameter",
        language: "typescript",
        filePath: file.path,
        packageId: file.packageId,
        ownerId,
        scope: "parameter",
        startLine: parameter.getStartLineNumber(),
        endLine: parameter.getEndLineNumber(),
        exported: false,
        signature: options.includeSignature ? parameter.getText().split("\n")[0]?.trim() : undefined,
        code: options.includeSignature ? boundedCode(parameter.getText(), 1000) : undefined,
        confidence: 1,
      }),
    );
    events.add(edge("HAS_PARAMETER", ownerId, id, meta(file.path, parameter.getStartLineNumber(), parameter.getEndLineNumber(), "typescript_function_parameter", 1)));
  }
}

function emitLocalVariables(file: FileInfo, sourceFile: SourceFile, byDeclaration: SymbolIndex, events: EventBuffer, options: SymbolOptions): void {
  sourceFile.forEachDescendant((current) => {
    if (!Node.isVariableDeclaration(current) || isTopLevelVariable(current)) return;
    const name = current.getName();
    if (!name) return;
    const ownerId = enclosingSymbolId(current.getParent(), "", byDeclaration);
    if (!ownerId) return;
    const id = childSymbolId(file.path, ownerId, "var", name, current.getStartLineNumber());
    byDeclaration.set(declarationKey(current), id);
    const kind = symbolKind(name, current);
    events.add(
      node("Symbol", id, {
        name,
        kind,
        language: "typescript",
        filePath: file.path,
        packageId: file.packageId,
        ownerId,
        scope: "function",
        startLine: current.getStartLineNumber(),
        endLine: current.getEndLineNumber(),
        exported: false,
        signature: options.includeSignature ? current.getText().split("\n")[0]?.trim() : undefined,
        code: options.includeSignature ? localVariableCode(current) : undefined,
        confidence: 1,
      }),
    );
    events.add(edge("DECLARES_VARIABLE", ownerId, id, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "typescript_function_variable", 1)));
    const initializer = current.getInitializer();
    if (initializer) {
      emitParameters(file, initializer, id, byDeclaration, events, options);
    }
  });
}

function localVariableCode(declaration: Node): string | undefined {
  if (!Node.isVariableDeclaration(declaration)) return undefined;
  const initializer = declaration.getInitializer();
  if (initializer && (Node.isArrowFunction(initializer) || Node.isFunctionExpression(initializer) || Node.isClassExpression(initializer))) {
    return boundedCode(declaration.getText(), 4000);
  }
  return boundedCode(declaration.getText(), 1000);
}

function hasParameters(node: Node): node is Node & { getParameters(): ParameterDeclaration[] } {
  return (
    Node.isFunctionDeclaration(node) ||
    Node.isMethodDeclaration(node) ||
    Node.isFunctionExpression(node) ||
    Node.isArrowFunction(node) ||
    Node.isConstructorDeclaration(node)
  );
}

function symbolScope(declaration: Node): string {
  if (Node.isVariableDeclaration(declaration) && isTopLevelVariable(declaration)) return "module";
  if (Node.isMethodDeclaration(declaration)) return "class";
  return "module";
}

function isTopLevelVariable(declaration: Node): boolean {
  if (!Node.isVariableDeclaration(declaration)) return false;
  return Node.isSourceFile(declaration.getVariableStatement()?.getParent());
}

function childSymbolId(filePath: string, ownerId: string, kind: string, name: string, line: number): string {
  const owner = ownerId.split("#").at(-1) ?? ownerId;
  return stableSymbolId(filePath, `${owner}::${kind}:${name}:${line}`);
}

function boundedCode(code: string, maxLength: number): string {
  if (code.length <= maxLength) return code;
  return `${code.slice(0, maxLength)}\n/* code truncated at ${maxLength} characters */`;
}

function uniqueSymbolsByName(symbolIndex: SymbolIndex): Map<string, string> {
  const out = new Map<string, string>();
  const duplicated = new Set<string>();
  for (const id of symbolIndex.values()) {
    const name = id.split("#").at(-1);
    if (!name) continue;
    if (out.has(name)) {
      duplicated.add(name);
      out.delete(name);
      continue;
    }
    if (!duplicated.has(name)) {
      out.set(name, id);
    }
  }
  return out;
}

function enclosingSymbolId(node: Node, repo: string, symbolIndex: SymbolIndex): string | undefined {
  let current: Node | undefined = node;
  while (current) {
    if (symbolIndex.has(declarationKey(current))) return symbolIndex.get(declarationKey(current));
    current = current.getParent();
  }
  return undefined;
}

function relationOwnerSymbolId(node: Node, symbolIndex: SymbolIndex): string | undefined {
  let current: Node | undefined = node;
  while (current) {
    if (symbolIndex.has(declarationKey(current)) && relationOwnerEligible(current)) {
      return symbolIndex.get(declarationKey(current));
    }
    current = current.getParent();
  }
  return undefined;
}

function relationOwnerEligible(node: Node): boolean {
  if (Node.isParameterDeclaration(node)) return false;
  if (Node.isVariableDeclaration(node)) {
    const initializer = node.getInitializer();
    return !!initializer && (Node.isArrowFunction(initializer) || Node.isFunctionExpression(initializer) || Node.isClassExpression(initializer));
  }
  return true;
}

function declarationKey(node: Node): string {
  return `${node.getSourceFile().getFilePath()}:${node.getStart()}:${node.getEnd()}`;
}

function meta(sourceFile: string, startLine: number, endLine: number, reason: string, confidence: number): Record<string, unknown> {
  return { source: "typescript", sourceFile, startLine, endLine, confidence, reason, extractor: "typescript", language: "typescript" };
}

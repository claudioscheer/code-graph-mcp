import { Node } from "ts-morph";
import { edge, node } from "../core/events.js";
import type { EventBuffer } from "../core/emit.js";
import type { FileInfo } from "./files.js";

export function extractTests(files: FileInfo[], events: EventBuffer): void {
  for (const file of files) {
    if (!isTestFile(file.path)) continue;
    file.sourceFile.forEachDescendant((current) => {
      if (!Node.isCallExpression(current)) return;
      const expression = current.getExpression().getText();
      if (!["describe", "it", "test"].includes(expression)) return;
      const [nameArg] = current.getArguments();
      const testName = Node.isStringLiteral(nameArg) ? nameArg.getLiteralText() : `${expression}:${current.getStartLineNumber()}`;
      const id = `test:${file.path}#${testName}`;
      events.add(node("Test", id, { filePath: file.path, testName, startLine: current.getStartLineNumber(), endLine: current.getEndLineNumber(), framework: "vitest_or_jest" }));
      events.add(edge("TESTS_FILE", id, file.id, meta(file.path, current.getStartLineNumber(), current.getEndLineNumber(), "test_block_in_file", 0.8)));
    });
  }
}

function isTestFile(path: string): boolean {
  return path.includes("__tests__/") || path.includes(".test.") || path.includes(".spec.");
}

function meta(sourceFile: string, startLine: number, endLine: number, reason: string, confidence: number): Record<string, unknown> {
  return { source: "typescript", sourceFile, startLine, endLine, confidence, reason, extractor: "typescript", language: "typescript" };
}

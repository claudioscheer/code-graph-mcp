import path from "node:path";
import fs from "node:fs";

export function normalizePath(value: string): string {
  return value.replaceAll("\\", "/").replace(/^\.\//, "");
}

export function relativePath(repo: string, filePath: string): string {
  return normalizePath(path.relative(repo, filePath));
}

export function exists(filePath: string): boolean {
  return fs.existsSync(filePath);
}

export function readJSON<T>(filePath: string): T | undefined {
  if (!exists(filePath)) return undefined;
  return JSON.parse(fs.readFileSync(filePath, "utf8")) as T;
}

export function packageNameFromSpecifier(specifier: string): string {
  if (specifier.startsWith("@")) {
    const [scope, name] = specifier.split("/");
    return `${scope}/${name}`;
  }
  return specifier.split("/")[0] ?? specifier;
}

import path from "node:path";
import fs from "node:fs";
import fg from "fast-glob";
import ignore from "ignore";
import { normalizePath } from "./fs.js";

const builtInIgnores = [
  "**/node_modules/**",
  "**/.git/**",
  "**/.next/**",
  "**/dist/**",
  "**/build/**",
  "**/coverage/**",
  "**/*.d.ts",
];

export type GitIgnoreMatcher = {
  files: string[];
  ignored(path: string): boolean;
};

export async function loadGitIgnore(repo: string): Promise<GitIgnoreMatcher> {
  const matcher = ignore();
  matcher.add(builtInIgnores);

  const files = await fg([".gitignore", "**/.gitignore"], {
    cwd: repo,
    ignore: builtInIgnores,
    onlyFiles: true,
    dot: true,
  });

  for (const file of files.sort()) {
    const dir = normalizePath(path.posix.dirname(normalizePath(file)));
    const content = fs.readFileSync(path.join(repo, file), "utf8");
    matcher.add(prefixGitIgnore(content, dir));
  }

  return {
    files: files.sort(),
    ignored(value: string): boolean {
      const normalized = normalizePath(value);
      return matcher.ignores(normalized);
    },
  };
}

function prefixGitIgnore(content: string, dir: string): string[] {
  if (dir === ".") return content.split(/\r?\n/);

  return content.split(/\r?\n/).map((line) => {
    const trimmed = line.trim();
    if (trimmed === "" || trimmed.startsWith("#")) return line;

    const negated = trimmed.startsWith("!");
    const pattern = negated ? trimmed.slice(1) : trimmed;
    const prefixed = pattern.startsWith("/") ? `${dir}/${pattern.slice(1)}` : `${dir}/${pattern}`;
    return negated ? `!${prefixed}` : prefixed;
  });
}

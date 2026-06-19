import { spawnSync } from "node:child_process";
import { warning, type GraphEvent } from "../core/events.js";

export function dependencyCruiserWarnings(repo: string): GraphEvent[] {
  const result = spawnSync("pnpm", ["exec", "depcruise", "--no-config", "--output-type", "json", "--ts-pre-compilation-deps", repo], {
    cwd: process.cwd(),
    encoding: "utf8",
    maxBuffer: 1024 * 1024 * 50,
  });
  if (result.error) {
    return [warning("dependency-cruiser unavailable", { error: result.error.message })];
  }
  if (!result.stdout.trim()) {
    return [warning("dependency-cruiser produced no output", { stderr: result.stderr })];
  }
  try {
    const parsed = JSON.parse(result.stdout) as { summary?: { error?: number; warn?: number; totalCruised?: number } };
    return [
      warning("dependency-cruiser validation completed", {
        totalCruised: parsed.summary?.totalCruised ?? 0,
        errors: parsed.summary?.error ?? 0,
        warnings: parsed.summary?.warn ?? 0,
      }),
    ];
  } catch (error) {
    return [warning("dependency-cruiser output was not parseable", { error: String(error) })];
  }
}

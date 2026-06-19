import type { GraphEvent } from "./events.js";

export class EventBuffer {
  private readonly seen = new Set<string>();
  private readonly events: GraphEvent[] = [];

  add(event: GraphEvent): void {
    const key = JSON.stringify(event);
    if (this.seen.has(key)) return;
    this.seen.add(key);
    this.events.push(event);
  }

  all(): GraphEvent[] {
    const rank = { node: 0, edge: 1, warning: 2, summary: 3 } as const;
    return [...this.events].sort((a, b) => {
      const byType = rank[a.type] - rank[b.type];
      if (byType !== 0) return byType;
      return JSON.stringify(a).localeCompare(JSON.stringify(b));
    });
  }

  writeStdout(): void {
    for (const event of this.all()) {
      process.stdout.write(`${JSON.stringify(event)}\n`);
    }
  }
}

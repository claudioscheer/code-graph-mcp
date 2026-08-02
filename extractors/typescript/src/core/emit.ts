import type { GraphEvent } from "./events.js";

export class EventBuffer {
  private readonly seen = new Set<string>();
  private readonly events: GraphEvent[] = [];

  add(event: GraphEvent): void {
    const key = eventKey(event);
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

function eventKey(event: GraphEvent): string {
  switch (event.type) {
    case "node":
      return `node\0${event.label}\0${event.id}`;
    case "edge":
      return `edge\0${event.rel}\0${event.from}\0${event.to}\0${JSON.stringify(event.props)}`;
    case "warning":
      return `warning\0${event.source}\0${event.message}\0${JSON.stringify(event.props ?? {})}`;
    case "summary":
      return `summary\0${event.source}\0${JSON.stringify(event.props)}`;
  }
}

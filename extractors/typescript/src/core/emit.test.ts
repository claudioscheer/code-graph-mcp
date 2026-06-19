import { describe, expect, test } from "vitest";
import { EventBuffer } from "./emit.js";
import { edge, node, summary, warning } from "./events.js";

describe("EventBuffer", () => {
  test("orders nodes before edges for stores that match endpoints", () => {
    const events = new EventBuffer();

    events.add(edge("IMPORTS_FILE", "file:a.ts", "file:b.ts", { confidence: 1 }));
    events.add(summary({ events: 4 }));
    events.add(warning("example warning"));
    events.add(node("File", "file:b.ts", { path: "b.ts" }));
    events.add(node("File", "file:a.ts", { path: "a.ts" }));

    expect(events.all().map((event) => event.type)).toEqual(["node", "node", "edge", "warning", "summary"]);
  });
});

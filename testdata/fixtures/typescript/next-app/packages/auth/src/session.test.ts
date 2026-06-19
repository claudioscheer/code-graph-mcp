import { describe, expect, test } from "vitest";
import { getSession } from "./session";

describe("getSession", () => {
  test("returns a session", async () => {
    await expect(getSession()).resolves.toEqual({
      user: { id: "1", name: "Ada" },
    });
  });
});

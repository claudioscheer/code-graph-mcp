import { describe, expect, test } from "vitest";
import { parseArgs, parseAnalysisMode } from "./args.js";

describe("parseArgs", () => {
  test("defaults to fast analysis mode", () => {
    expect(parseArgs([]).analysisMode).toBe("fast");
  });

  test("accepts full analysis mode", () => {
    expect(parseArgs(["--analysis-mode", "full"]).analysisMode).toBe("full");
  });

  test("rejects unsupported analysis modes", () => {
    expect(() => parseAnalysisMode("partial")).toThrow(/unsupported analysis mode/);
  });
});

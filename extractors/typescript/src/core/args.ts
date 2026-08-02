export type ExtractorArgs = {
  repo: string;
  protocol: string;
  analysisMode: "full" | "fast";
};

export function parseArgs(argv = process.argv.slice(2)): ExtractorArgs {
  const args: ExtractorArgs = { repo: process.cwd(), protocol: "codegraph.v1", analysisMode: "fast" };
  for (let index = 0; index < argv.length; index++) {
    const value = argv[index];
    if (value === "--repo") {
      args.repo = argv[++index] ?? args.repo;
    }
    if (value === "--protocol") {
      args.protocol = argv[++index] ?? args.protocol;
    }
    if (value === "--analysis-mode") {
      args.analysisMode = parseAnalysisMode(argv[++index]);
    }
  }
  return args;
}

export function parseAnalysisMode(value: string | undefined): "full" | "fast" {
  if (value === "fast") return "fast";
  if (value === "full") return "full";
  if (!value) return "fast";
  throw new Error(`unsupported analysis mode ${JSON.stringify(value)}; expected "full" or "fast"`);
}

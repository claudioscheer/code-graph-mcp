export type ExtractorArgs = {
  repo: string;
  protocol: string;
};

export function parseArgs(argv = process.argv.slice(2)): ExtractorArgs {
  const args: ExtractorArgs = { repo: process.cwd(), protocol: "codegraph.v1" };
  for (let index = 0; index < argv.length; index++) {
    const value = argv[index];
    if (value === "--repo") {
      args.repo = argv[++index] ?? args.repo;
    }
    if (value === "--protocol") {
      args.protocol = argv[++index] ?? args.protocol;
    }
  }
  return args;
}

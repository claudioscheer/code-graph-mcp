export const PROTOCOL = "codegraph.v1" as const;

export type GraphEvent =
  | {
      protocol: typeof PROTOCOL;
      type: "node";
      label: string;
      id: string;
      props: Record<string, unknown>;
    }
  | {
      protocol: typeof PROTOCOL;
      type: "edge";
      rel: string;
      from: string;
      to: string;
      props: Record<string, unknown>;
    }
  | {
      protocol: typeof PROTOCOL;
      type: "warning";
      source: string;
      message: string;
      props?: Record<string, unknown>;
    }
  | {
      protocol: typeof PROTOCOL;
      type: "summary";
      source: string;
      props: Record<string, unknown>;
    };

export function node(label: string, id: string, props: Record<string, unknown>): GraphEvent {
  return { protocol: PROTOCOL, type: "node", label, id, props };
}

export function edge(rel: string, from: string, to: string, props: Record<string, unknown>): GraphEvent {
  return { protocol: PROTOCOL, type: "edge", rel, from, to, props };
}

export function warning(message: string, props?: Record<string, unknown>): GraphEvent {
  return { protocol: PROTOCOL, type: "warning", source: "typescript-extractor", message, props };
}

export function summary(props: Record<string, unknown>): GraphEvent {
  return { protocol: PROTOCOL, type: "summary", source: "typescript-extractor", props };
}

export function stableFileId(relativePath: string): string {
  return `file:${normalizePath(relativePath)}`;
}

export function stableSymbolId(relativePath: string, qualifiedName: string): string {
  return `symbol:${normalizePath(relativePath)}#${qualifiedName}`;
}

export function stablePackageId(packageName: string): string {
  return `package:${packageName}`;
}

export function stableProjectId(projectRoot: string): string {
  return `project:${normalizePath(projectRoot)}`;
}

export function stableRouteId(projectRoot: string, method: string, routePath: string): string {
  return `route:${normalizePath(projectRoot)}:${method || "PAGE"}:${routePath}`;
}

export function stableExternalId(ecosystem: string, packageName: string): string {
  return `external:${ecosystem}:${packageName}`;
}

export function stableConfigId(key: string): string {
  return `config:${key}`;
}

export function normalizePath(value: string): string {
  return value.replaceAll("\\", "/").replace(/^\.\//, "");
}

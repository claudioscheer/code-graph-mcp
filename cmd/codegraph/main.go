package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/claudioscheer/code-graph-mcp/internal/config"
	"github.com/claudioscheer/code-graph-mcp/internal/discovery"
	"github.com/claudioscheer/code-graph-mcp/internal/events"
	"github.com/claudioscheer/code-graph-mcp/internal/graph"
	"github.com/claudioscheer/code-graph-mcp/internal/mcp"
	"github.com/claudioscheer/code-graph-mcp/internal/neo4jstore"
	"github.com/claudioscheer/code-graph-mcp/internal/plugins"
	"github.com/claudioscheer/code-graph-mcp/internal/visualize"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg := config.Load()
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "doctor":
		return doctor(ctx, cfg)
	case "reset":
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		return store.Reset(ctx)
	case "discover":
		fs := flag.NewFlagSet("discover", flag.ContinueOnError)
		repo := fs.String("repo", cfg.Repo, "repo root")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		manifest, err := discovery.Discover(*repo)
		if err != nil {
			return err
		}
		return writeJSON(manifest)
	case "index":
		fs := flag.NewFlagSet("index", flag.ContinueOnError)
		repo := fs.String("repo", cfg.Repo, "repo root")
		language := fs.String("language", "typescript", "language")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *language != "typescript" {
			return errors.New("only typescript is supported in v1")
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		plugin := typescriptPlugin(cfg)
		if err := (plugins.Runner{Stderr: os.Stderr}).Run(ctx, plugin, plugins.ExtractRequest{Repo: *repo, Protocol: events.Protocol}, store); err != nil {
			return err
		}
		return writeJSON(map[string]string{"status": "indexed", "repo": *repo})
	case "status":
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		status, err := store.Status(ctx)
		if err != nil {
			return err
		}
		return writeJSON(status)
	case "visualize":
		fs := flag.NewFlagSet("visualize", flag.ContinueOnError)
		output := fs.String("output", "codegraph-visualization.html", "output HTML file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		if err := (visualize.Exporter{Driver: store.Driver()}).WriteHTML(ctx, *output); err != nil {
			return err
		}
		return writeJSON(map[string]string{"status": "visualization written", "output": *output})
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
		repo := fs.String("repo", cfg.Repo, "repo root")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		return mcp.Server{Query: graph.Service{Driver: store.Driver()}, Repo: *repo}.Serve(ctx, os.Stdin, os.Stdout)
	case "test-extractor":
		if len(args) < 2 || args[1] != "typescript" {
			return errors.New("usage: codegraph test-extractor typescript")
		}
		return (plugins.Runner{Stderr: os.Stderr}).Run(ctx, typescriptPlugin(cfg), plugins.ExtractRequest{Repo: "testdata/fixtures/typescript/next-app", Protocol: events.Protocol}, discardSink{})
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func openStore(ctx context.Context, cfg config.Config) (*neo4jstore.Store, error) {
	store, err := neo4jstore.New(cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword)
	if err != nil {
		return nil, err
	}
	if err := store.CreateConstraints(ctx); err != nil {
		_ = store.Close(ctx)
		return nil, err
	}
	return store, nil
}

func typescriptPlugin(cfg config.Config) plugins.ExtractorPlugin {
	if cfg.TSExtractor == "pnpm" {
		return plugins.ExtractorPlugin{
			Name:     "typescript",
			Language: "typescript",
			Command:  "pnpm",
			Args:     []string{"exec", "tsx", "extractors/typescript/src/cli.ts"},
			Env:      map[string]string{"NODE_OPTIONS": cfg.NodeOptions},
		}
	}
	return plugins.ExtractorPlugin{Name: "typescript", Language: "typescript", Command: cfg.TSExtractor, Env: map[string]string{"NODE_OPTIONS": cfg.NodeOptions}}
}

func doctor(ctx context.Context, cfg config.Config) error {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close(ctx)
	return writeJSON(map[string]any{"neo4j": "ok", "typescriptExtractor": cfg.TSExtractor, "nodeOptions": cfg.NodeOptions})
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usage() {
	fmt.Println(`Usage:
  codegraph doctor
  codegraph reset
  codegraph discover --repo .
  codegraph index --repo . --language typescript
  codegraph status
  codegraph visualize --output codegraph-visualization.html
  codegraph test-extractor typescript
  codegraph mcp --repo .`)
}

type discardSink struct{}

func (discardSink) Emit(ctx context.Context, event events.GraphEvent) error {
	return events.Validate(event)
}
func (discardSink) Flush(ctx context.Context) error { return nil }

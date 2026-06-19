package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
		ripple := fs.String("ripple", "", "ripple name")
		language := fs.String("language", "typescript", "language")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *ripple == "" {
			return errors.New("index requires --ripple")
		}
		if *language != "typescript" {
			return errors.New("only typescript is supported in v1")
		}
		repoPath, err := filepath.Abs(*repo)
		if err != nil {
			return err
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		if err := store.ResetRipple(ctx, *ripple); err != nil {
			return err
		}
		if err := store.SaveRipple(ctx, neo4jstore.Ripple{Name: *ripple, Repo: repoPath, Language: *language}); err != nil {
			return err
		}
		plugin := typescriptPlugin(cfg)
		if err := (plugins.Runner{Stderr: os.Stderr}).Run(ctx, plugin, plugins.ExtractRequest{Repo: repoPath, Protocol: events.Protocol}, store.ForRipple(*ripple)); err != nil {
			return err
		}
		return writeJSON(map[string]string{"status": "indexed", "ripple": *ripple, "repo": repoPath})
	case "update":
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		ripple := fs.String("ripple", "", "ripple name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *ripple == "" {
			return errors.New("update requires --ripple")
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		info, err := store.GetRipple(ctx, *ripple)
		if err != nil {
			return err
		}
		if info.Language != "typescript" {
			return fmt.Errorf("only typescript is supported in v1, ripple %q uses %q", *ripple, info.Language)
		}
		if err := store.ResetRipple(ctx, *ripple); err != nil {
			return err
		}
		if err := store.SaveRipple(ctx, info); err != nil {
			return err
		}
		if err := (plugins.Runner{Stderr: os.Stderr}).Run(ctx, typescriptPlugin(cfg), plugins.ExtractRequest{Repo: info.Repo, Protocol: events.Protocol}, store.ForRipple(*ripple)); err != nil {
			return err
		}
		return writeJSON(map[string]string{"status": "updated", "ripple": *ripple, "repo": info.Repo})
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		ripple := fs.String("ripple", "", "ripple name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		status, err := store.Status(ctx, *ripple)
		if err != nil {
			return err
		}
		return writeJSON(status)
	case "ripples":
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		ripples, err := store.ListRipples(ctx)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"ripples": ripples})
	case "visualize":
		fs := flag.NewFlagSet("visualize", flag.ContinueOnError)
		ripple := fs.String("ripple", "", "ripple name")
		output := fs.String("output", "codegraph-visualization.html", "output HTML file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *ripple == "" {
			return errors.New("visualize requires --ripple")
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		if err := (visualize.Exporter{Driver: store.Driver(), Ripple: *ripple}).WriteHTML(ctx, *output); err != nil {
			return err
		}
		return writeJSON(map[string]string{"status": "visualization written", "ripple": *ripple, "output": *output})
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
		ripple := fs.String("ripple", "", "ripple name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *ripple == "" {
			return errors.New("mcp requires --ripple")
		}
		store, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer store.Close(ctx)
		info, err := store.GetRipple(ctx, *ripple)
		if err != nil {
			return err
		}
		return mcp.Server{Query: graph.Service{Driver: store.Driver(), Ripple: *ripple}, Repo: info.Repo}.Serve(ctx, os.Stdin, os.Stdout)
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		addr := fs.String("addr", ":8080", "HTTP listen address")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return serveHTTP(ctx, cfg, *addr)
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

func serveHTTP(ctx context.Context, cfg config.Config, addr string) error {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		ripple := strings.TrimPrefix(r.URL.Path, "/mcp/")
		ripple = strings.Trim(ripple, "/")
		if ripple == "" {
			http.Error(w, "missing ripple in /mcp/{ripple}", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodGet {
			info, err := store.GetRipple(r.Context(), ripple)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ripple": info, "endpoint": "/mcp/" + ripple})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		info, err := store.GetRipple(r.Context(), ripple)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, ok := (mcp.Server{Query: graph.Service{Driver: store.Driver(), Ripple: ripple}, Repo: info.Repo}).Process(r.Context(), payload)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	fmt.Fprintf(os.Stderr, "listening on %s\n", addr)
	return server.ListenAndServe()
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
  codegraph index --ripple my-app --repo . --language typescript
  codegraph update --ripple my-app
  codegraph status --ripple my-app
  codegraph ripples
  codegraph visualize --ripple my-app --output codegraph-visualization.html
  codegraph serve --addr :8080
  codegraph test-extractor typescript
  codegraph mcp --ripple my-app`)
}

type discardSink struct{}

func (discardSink) Emit(ctx context.Context, event events.GraphEvent) error {
	return events.Validate(event)
}
func (discardSink) Flush(ctx context.Context) error { return nil }

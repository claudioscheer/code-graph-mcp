package mcp

import (
	"context"
	"fmt"
	"time"
)

// ReindexRequest controls a ripple rebuild triggered from MCP.
// Path-scoped incremental extraction is not available yet; Paths are advisory
// (reported on the result and used for post-reindex path impact when requested).
type ReindexRequest struct {
	AnalysisMode string
	Paths        []string
	Timeout      time.Duration
	IncludeImpact bool
}

// Reindexer rebuilds the current ripple index.
type Reindexer interface {
	Reindex(ctx context.Context, req ReindexRequest) (map[string]any, error)
}

func (s Server) reindex(ctx context.Context, args map[string]any) (map[string]any, error) {
	if s.Reindexer == nil {
		return map[string]any{
			"status":  "unavailable",
			"error":   "reindex is not configured on this MCP server",
			"hint":    "Use CLI: codegraph update --ripple <name>. Or restart serve/mcp with store-backed reindex wiring.",
			"success": false,
		}, nil
	}

	timeoutSec := intArg(args, "timeoutSec", 300)
	if timeoutSec < 30 {
		timeoutSec = 30
	}
	if timeoutSec > 1800 {
		timeoutSec = 1800
	}
	paths := normalizePathList(stringSliceArg(args, "paths", "files"))
	if boolArg(args, "useDirty", false) {
		paths = append(paths, gitDirtyFiles(s.Repo)...)
		paths = uniqueSortedPaths(paths)
	}

	req := ReindexRequest{
		AnalysisMode:  firstStringArg(args, "analysisMode", "mode"),
		Paths:         paths,
		Timeout:       time.Duration(timeoutSec) * time.Second,
		IncludeImpact: boolArg(args, "includeImpact", len(paths) > 0),
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	started := time.Now()
	result, err := s.Reindexer.Reindex(runCtx, req)
	if result == nil {
		result = map[string]any{}
	}
	result["durationMs"] = time.Since(started).Milliseconds()
	if err != nil {
		result["success"] = false
		result["status"] = "error"
		result["error"] = err.Error()
		if runCtx.Err() == context.DeadlineExceeded {
			result["error"] = fmt.Sprintf("reindex timed out after %s: %v", req.Timeout, err)
			result["status"] = "timeout"
		}
		// Return structured failure as tool result (not RPC error) so agents can react.
		return result, nil
	}
	result["success"] = true
	if result["status"] == nil {
		result["status"] = "updated"
	}
	result["mode"] = "full_rebuild"
	result["incremental"] = false
	if len(paths) > 0 {
		result["requestedPaths"] = paths
		result["pathScopeNote"] = "Extractor does not support file-level incremental updates yet; full ripple rebuild ran. Paths are advisory for follow-up impact."
	}

	// Post-reindex freshness + optional path impact.
	if freshness, ferr := s.indexFreshness(ctx); ferr == nil {
		result["freshness"] = slimIndex(freshness)
		if v, ok := freshness["graphReliable"].(bool); ok {
			result["graphReliable"] = v
		}
		if v, ok := freshness["stale"].(bool); ok {
			result["stale"] = v
		}
	}
	if req.IncludeImpact && len(paths) > 0 {
		if impact, ierr := s.analyzePathSetImpact(ctx, map[string]any{
			"paths":  paths,
			"detail": "summary",
			"limit":  40,
		}); ierr == nil {
			result["pathImpactSummary"] = map[string]any{
				"dependentCount":   impact["dependentCount"],
				"relatedTestCount": impact["relatedTestCount"],
				"packages":         impact["packages"],
				"confidence":       impact["confidence"],
			}
		}
	}
	result["next"] = "Graph was rebuilt. Re-run prepare_change_plan / analyze_function_impact for planning."
	return result, nil
}

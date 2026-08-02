package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	defaultRenameScanLimit = 500
	maxRenameScanLimit     = 2000
)

// prepareRenamePlan builds a multi-identity rename checklist: path, package name,
// and optional CI/Docker identity stems. Separates layers so agents do not mix
// directory renames with package renames or generic "worker" APIs.
func (s Server) prepareRenamePlan(ctx context.Context, args map[string]any) (map[string]any, error) {
	detail := detailArg(args)
	pathID := strings.TrimSpace(firstStringArg(args, "path", "pathPrefix", "dir", "directory"))
	pathID = strings.TrimPrefix(filepath.ToSlash(pathID), "./")
	packageName := strings.TrimSpace(firstStringArg(args, "packageName", "package", "pkg"))
	shortName := strings.TrimSpace(firstStringArg(args, "shortName", "name", "appName"))
	includeCI := boolArg(args, "includeCiJobNames", true)
	includeDocker := boolArg(args, "includeDockerImageNames", true)
	includeShortLiteral := boolArg(args, "includeShortNameLiteral", false)
	scanLimit := intArg(args, "limit", defaultRenameScanLimit)
	if scanLimit <= 0 {
		scanLimit = defaultRenameScanLimit
	}
	if scanLimit > maxRenameScanLimit {
		scanLimit = maxRenameScanLimit
	}
	maxItems := maxItemsArg(args, 40)
	if detail == "summary" {
		maxItems = min(maxItems, 15)
	}

	// Infer short name from path/package when omitted.
	if shortName == "" {
		shortName = inferShortName(pathID, packageName)
	}
	if pathID == "" && packageName == "" && shortName == "" {
		return map[string]any{
			"planKind":   "rename",
			"confidence": "low",
			"next":       "Pass path (e.g. apps/workers), packageName (e.g. @howdy/workers), and/or shortName (e.g. workers).",
		}, nil
	}

	type identity struct {
		Key    string
		Old    string
		Kind   string
		Layer  string
		Risk   string
		Note   string
	}
	identities := []identity{}
	if pathID != "" {
		identities = append(identities, identity{
			Key: "path", Old: pathID, Kind: "path", Layer: "directory_path", Risk: "high",
			Note: "Directory and import path strings. Required for moving the app folder.",
		})
	}
	if packageName != "" {
		identities = append(identities, identity{
			Key: "package", Old: packageName, Kind: "package", Layer: "package_identity", Risk: "high",
			Note: "package.json name, pnpm/turbo --filter, and workspace package refs.",
		})
	}
	if shortName != "" && includeCI {
		for _, stem := range []struct{ key, old, note string }{
			{"ci_build_job", "build-" + shortName, "GitHub Actions job id and needs: edges"},
			{"ci_deploy_job", "deploy-" + shortName, "GitHub Actions deploy job id and needs: edges"},
			{"ci_buildcache", shortName + "-buildcache", "Registry cache tag refs"},
		} {
			identities = append(identities, identity{
				Key: stem.key, Old: stem.old, Kind: "literal", Layer: "ci_job_identity", Risk: "critical",
				Note: stem.note + ". Optional: keep job/image names if only renaming the directory.",
			})
		}
	}
	if shortName != "" && includeDocker {
		identities = append(identities, identity{
			Key: "docker_app_dir", Old: "APP_DIR=" + shortName, Kind: "literal", Layer: "docker_identity", Risk: "critical",
			Note: "Dockerfile ARG APP_DIR default. Miss = wrong image contents.",
		})
	}
	// Bare short name is extremely noisy (workers vs Worker/BullMQ). Off by default.
	if shortName != "" && includeShortLiteral {
		identities = append(identities, identity{
			Key: "short_literal", Old: shortName, Kind: "literal", Layer: "short_name_literal", Risk: "low",
			Note: "NOISY: matches English/BullMQ/utils. Review mustNotTouch; do not bulk-replace.",
		})
	}

	// Dedupe by Old string (docker identities may collide).
	seenOld := map[string]bool{}
	deduped := []identity{}
	for _, id := range identities {
		if id.Old == "" || seenOld[id.Old] {
			continue
		}
		seenOld[id.Old] = true
		deduped = append(deduped, id)
	}
	identities = deduped

	identityResults := []map[string]any{}
	mustEditByLayer := map[string][]string{}
	allMustEdit := []string{}
	seenEdit := map[string]bool{}
	anyScanTruncated := false
	totalMatches := 0
	riskOrder := []string{}

	for _, id := range identities {
		impactArgs := map[string]any{
			"oldName": id.Old,
			"kind":    id.Kind,
			"detail":  "files",
			"limit":   scanLimit,
			"maxItems": maxItems,
		}
		impact, err := s.analyzeRenameImpact(ctx, id.Old, id.Kind, impactArgs)
		if err != nil {
			return nil, err
		}
		unique := intValue(impact["uniqueFiles"])
		matches := intValue(impact["totalMatches"])
		totalMatches += matches
		scanTrunc, _ := impact["scanTruncated"].(bool)
		if !scanTrunc {
			// backward compatible if only truncated is set
			if t, ok := impact["truncated"].(bool); ok && t {
				// may be list-only truncation; prefer explicit scan flag when present
				if _, has := impact["scanTruncated"]; !has {
					// If uniqueFiles == limit, treat as scan truncation signal
					if unique >= scanLimit {
						scanTrunc = true
					}
				}
			}
		}
		if st, ok := impact["scanTruncated"].(bool); ok {
			scanTrunc = st
		}
		if scanTrunc {
			anyScanTruncated = true
		}

		files := collectRenamePaths(impact)
		for _, path := range files {
			addPath(&allMustEdit, seenEdit, path)
			layerFiles := mustEditByLayer[id.Layer]
			seenLayer := map[string]bool{}
			for _, existing := range layerFiles {
				seenLayer[existing] = true
			}
			if !seenLayer[path] {
				mustEditByLayer[id.Layer] = append(mustEditByLayer[id.Layer], path)
			}
		}

		entry := map[string]any{
			"key":          id.Key,
			"oldName":      id.Old,
			"kind":         id.Kind,
			"layer":        id.Layer,
			"risk":         id.Risk,
			"note":         id.Note,
			"uniqueFiles":  unique,
			"totalMatches": matches,
			"bucketTotals": impact["bucketTotals"],
			"scanTruncated": scanTrunc,
			"truncated":    impact["truncated"],
		}
		if detail != "summary" {
			sample, _ := limitSlice(files, maxItems)
			entry["files"] = sample
		} else {
			sample, _ := limitSlice(files, min(maxItems, 8))
			entry["sampleFiles"] = sample
		}
		if scanTrunc {
			entry["recommendedLimit"] = min(scanLimit*2, maxRenameScanLimit)
			entry["next"] = "Scan incomplete. Rerun prepare_rename_plan or analyze_rename_impact with higher limit."
		}
		identityResults = append(identityResults, entry)
		if unique > 0 || matches > 0 {
			riskOrder = append(riskOrder, fmt.Sprintf("%s (%s): %d files", id.Layer, id.Old, unique))
		}
	}

	mustNotTouch := renameMustNotTouch(s.Repo, shortName, pathID, packageName)
	// Remove mustNotTouch paths from mustEdit when they only hit short_literal layer — keep path/package hits.
	mustEditClean := []string{}
	notTouchSet := map[string]bool{}
	for _, p := range mustNotTouch {
		notTouchSet[p] = true
	}
	for _, p := range allMustEdit {
		// Always keep path/package hits even if in mustNotTouch list (shouldn't overlap usually).
		if notTouchSet[p] && shortName != "" && !strings.Contains(p, pathID) && (packageName == "" || !strings.Contains(p, strings.TrimPrefix(packageName, "@"))) {
			// leave out of mustEdit — false positive utility files
			continue
		}
		mustEditClean = append(mustEditClean, p)
	}
	if len(mustEditClean) == 0 {
		mustEditClean = allMustEdit
	}

	// Suggested edit order by risk layer.
	layerOrder := []string{"docker_identity", "ci_job_identity", "package_identity", "directory_path", "short_name_literal"}
	suggestedOrder := []string{}
	seenOrder := map[string]bool{}
	for _, layer := range layerOrder {
		for _, path := range mustEditByLayer[layer] {
			if notTouchSet[path] && layer == "short_name_literal" {
				continue
			}
			addPath(&suggestedOrder, seenOrder, path)
		}
	}
	for _, path := range mustEditClean {
		addPath(&suggestedOrder, seenOrder, path)
	}
	// Keep samples small so agents never confuse sample length with totals.
	sampleCap := 12
	if detail == "files" || detail == "lines" || detail == "raw" {
		sampleCap = min(maxItems+20, 40)
	}
	suggestedOrder, _ = limitSlice(suggestedOrder, sampleCap)
	mustEditSample, mustEditSampled := limitSlice(mustEditClean, sampleCap)
	mustNotTouchOut, _ := limitSlice(mustNotTouch, 20)

	confidence := "high"
	if anyScanTruncated {
		confidence = "low"
	} else if len(mustEditClean) == 0 {
		confidence = "medium"
	}

	// Decision fork for agents.
	decisions := []map[string]any{
		{
			"id":   "directory_and_package_only",
			"label": "Rename directory + package identity only",
			"do":   "Change path and packageName layers. Leave CI job ids and image/cache tags as workers-* if deploy tooling depends on them.",
			"layers": []string{"directory_path", "package_identity"},
		},
		{
			"id":   "full_identity_rename",
			"label": "Rename directory + package + CI jobs + Docker APP_DIR",
			"do":   "All layers. Highest deploy risk; update every needs: edge and image tag.",
			"layers": []string{"directory_path", "package_identity", "ci_job_identity", "docker_identity"},
		},
	}

	success := []string{}
	if pathID != "" {
		success = append(success, fmt.Sprintf("rg -F %q returns 0 matches (except intentional history/docs)", pathID))
	}
	if packageName != "" {
		success = append(success, fmt.Sprintf("rg -F %q returns 0 matches", packageName))
	}
	success = append(success,
		"pnpm install / turbo filter for new package name succeeds",
		"typecheck and unit tests for the renamed app and importers pass",
		"CI workflow validates (job graph + image build for the app)",
	)

	layersOut := map[string]any{}
	for layer, files := range mustEditByLayer {
		sample, _ := limitSlice(files, maxItems)
		layersOut[layer] = map[string]any{"fileCount": len(files), "files": sample}
	}

	// Headline totals agents must report (never len(mustEditSample)).
	pathFiles := 0
	packageFiles := 0
	for _, entry := range identityResults {
		switch firstAnyString(entry, "layer") {
		case "directory_path":
			pathFiles = intValue(entry["uniqueFiles"])
		case "package_identity":
			packageFiles = intValue(entry["uniqueFiles"])
		}
	}

	response := map[string]any{
		"planKind":          "rename",
		"path":              pathID,
		"packageName":       packageName,
		"shortName":         shortName,
		"identities":        identityResults,
		"identityCount":     len(identityResults),
		"layers":            layersOut,
		// Full unique path count across all identities (not a sample size).
		"mustEditCount":    len(mustEditClean),
		"mustEditSample":   mustEditSample,
		"mustEditIsSample": mustEditSampled,
		"mustNotTouch":     mustNotTouchOut,
		"mustNotTouchCount": len(mustNotTouch),
		"mustNotTouchNote": "Do not bulk-replace generic worker APIs or core utils that share the short name.",
		"suggestedOrder":   suggestedOrder,
		"riskOrder":        riskOrder,
		"decisions":        decisions,
		"successCriteria":  success,
		"scanLimit":        scanLimit,
		"scanTruncated":    anyScanTruncated,
		// listTruncated only means samples were capped for display; scan may still be complete.
		"listTruncated":    mustEditSampled,
		"truncated":        anyScanTruncated,
		"totalMatchesApprox": totalMatches,
		"confidence":       confidence,
		"contextCompleteForPlanning": !anyScanTruncated,
		"detail":           detail,
		// Force agents to quote real totals, not sample array length.
		"totals": map[string]any{
			"mustEditFiles":        len(mustEditClean),
			"directoryPathFiles":   pathFiles,
			"packageIdentityFiles": packageFiles,
			"mustNotTouchFiles":    len(mustNotTouch),
			"totalMatchesApprox":   totalMatches,
		},
		"reportUsing": []string{
			"totals.mustEditFiles (or mustEditCount) — NEVER len(mustEditSample)",
			"identities[].uniqueFiles per layer (directory_path, package_identity, ci_job_identity, docker_identity)",
			"mustNotTouch / mustNotTouchCount",
			"decisions[] and successCriteria",
			"scanTruncated=false before claiming complete impact",
		},
		"stopConditions": []string{
			"If scanTruncated=true on any identity, raise limit and rerun before editing.",
			"Do not report mustEditSample length as total impact; use totals.mustEditFiles / mustEditCount.",
			"Do not search bare shortName without includeShortNameLiteral=true (high false positives).",
			"Pick a decisions[] option before renaming CI job ids or Docker APP_DIR.",
			"Verify with unbounded rg after edits.",
		},
		"next": "Pick decisions[].id. Report totals.* and identities[].uniqueFiles (not sample sizes). Edit by layer; skip mustNotTouch; verify successCriteria with rg.",
	}
	// Never expose a capped list as mustEdit — agents count that array and invent "35 files".
	// Full list only when complete; otherwise sample-only fields.
	if !mustEditSampled {
		response["mustEdit"] = mustEditClean
	} else {
		response["mustEditNote"] = "SAMPLE only in mustEditSample. Total is mustEditCount / totals.mustEditFiles. Do not report sample length."
	}
	if anyScanTruncated {
		response["next"] = "Scan truncated: rerun prepare_rename_plan with limit>=recommendedLimit before claiming complete impact."
		response["recommendedLimit"] = min(scanLimit*2, maxRenameScanLimit)
	}
	return response, nil
}

func inferShortName(pathID string, packageName string) string {
	if pathID != "" {
		parts := strings.Split(strings.Trim(filepath.ToSlash(pathID), "/"), "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	if packageName != "" {
		name := strings.TrimPrefix(packageName, "@")
		if i := strings.LastIndex(name, "/"); i >= 0 {
			return name[i+1:]
		}
		return name
	}
	return ""
}

func collectRenamePaths(impact map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	// filesToChange is map of bucket -> []entries
	if raw, ok := impact["filesToChange"].(map[string]any); ok {
		for _, bucket := range raw {
			for _, item := range mapSlice(bucket) {
				addPath(&out, seen, firstAnyString(item, "path"))
			}
		}
	}
	// allFiles if present (full list for non-truncated scans)
	for _, path := range stringSlice(impact["allFiles"]) {
		addPath(&out, seen, path)
	}
	slices.Sort(out)
	return out
}

func renameMustNotTouch(repo string, shortName string, pathID string, packageName string) []string {
	if shortName == "" {
		return nil
	}
	candidates := []string{
		"packages/core/utils/workers.ts",
		"packages/core/utils/workers.js",
		"packages/core/utils/workers.test.js",
		"packages/core/utils/workers.test.ts",
		"packages/core/__tests__/workers.sentry.test.js",
		"packages/core/__tests__/workers.sentry.test.ts",
	}
	// Also common relative variants
	out := []string{}
	for _, rel := range candidates {
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if _, err := os.Stat(full); err == nil {
			// Skip if this path is literally under the app being renamed.
			if pathID != "" && strings.HasPrefix(rel, strings.TrimSuffix(pathID, "/")+"/") {
				continue
			}
			out = append(out, rel)
		}
	}
	_ = packageName
	return out
}

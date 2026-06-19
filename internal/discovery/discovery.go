package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Manifest struct {
	RepoID         string    `json:"repoId"`
	Root           string    `json:"root"`
	PackageManager string    `json:"packageManager"`
	Projects       []Project `json:"projects"`
}

type Project struct {
	ID          string   `json:"id"`
	Root        string   `json:"root"`
	Language    string   `json:"language"`
	Frameworks  []string `json:"frameworks"`
	ConfigFiles []string `json:"configFiles"`
}

type packageJSON struct {
	Workspaces []string `json:"workspaces"`
}

func Discover(repo string) (Manifest, error) {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		RepoID:         "repo:" + filepath.Base(repo),
		Root:           repo,
		PackageManager: packageManager(repo),
	}

	projectRoots := []string{"."}
	for _, pattern := range workspacePatterns(repo) {
		matches, _ := filepath.Glob(filepath.Join(repo, filepath.FromSlash(pattern), "package.json"))
		for _, match := range matches {
			projectRoots = append(projectRoots, filepath.ToSlash(mustRel(repo, filepath.Dir(match))))
		}
	}
	seen := map[string]bool{}
	for _, root := range projectRoots {
		if seen[root] {
			continue
		}
		seen[root] = true
		project := Project{
			ID:       "project:" + root,
			Root:     root,
			Language: "typescript",
		}
		if exists(filepath.Join(repo, filepath.FromSlash(root), "next.config.js")) ||
			exists(filepath.Join(repo, filepath.FromSlash(root), "next.config.mjs")) ||
			exists(filepath.Join(repo, filepath.FromSlash(root), "next.config.ts")) {
			project.Frameworks = append(project.Frameworks, "nextjs")
		}
		if exists(filepath.Join(repo, filepath.FromSlash(root), "tsconfig.json")) {
			project.ConfigFiles = append(project.ConfigFiles, filepath.ToSlash(filepath.Join(root, "tsconfig.json")))
		}
		manifest.Projects = append(manifest.Projects, project)
	}
	return manifest, nil
}

func workspacePatterns(repo string) []string {
	data, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		return []string{"apps/*", "packages/*"}
	}
	var parsed packageJSON
	if json.Unmarshal(data, &parsed) != nil || len(parsed.Workspaces) == 0 {
		return []string{"apps/*", "packages/*"}
	}
	return parsed.Workspaces
}

func packageManager(repo string) string {
	switch {
	case exists(filepath.Join(repo, "pnpm-lock.yaml")):
		return "pnpm"
	case exists(filepath.Join(repo, "yarn.lock")):
		return "yarn"
	case exists(filepath.Join(repo, "package-lock.json")):
		return "npm"
	default:
		return "unknown"
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustRel(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

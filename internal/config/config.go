package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Neo4jURI      string
	Neo4jUser     string
	Neo4jPassword string
	Repo          string
	TSExtractor   string
	NodeOptions   string
}

func Load() Config {
	loadDotEnv(".env")
	repo := getenv("CODEGRAPH_REPO", ".")
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	return Config{
		Neo4jURI:      getenv("NEO4J_URI", "bolt://localhost:7687"),
		Neo4jUser:     getenv("NEO4J_USER", "neo4j"),
		Neo4jPassword: getenv("NEO4J_PASSWORD", "password"),
		Repo:          repo,
		TSExtractor:   getenv("CODEGRAPH_TS_EXTRACTOR", "pnpm"),
		NodeOptions:   getenv("CODEGRAPH_NODE_OPTIONS", "--max-old-space-size=6144"),
	}
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

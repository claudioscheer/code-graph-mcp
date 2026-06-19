package visualize

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Exporter struct {
	Driver neo4j.DriverWithContext
}

type graphData struct {
	GeneratedAt string         `json:"generatedAt"`
	Nodes       []nodeData     `json:"nodes"`
	Edges       []edgeData     `json:"edges"`
	LabelCounts map[string]int `json:"labelCounts"`
	RelCounts   map[string]int `json:"relCounts"`
}

type nodeData struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Name    string         `json:"name,omitempty"`
	Path    string         `json:"path,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Package string         `json:"packageId,omitempty"`
	Props   map[string]any `json:"props,omitempty"`
}

type edgeData struct {
	From  string         `json:"from"`
	To    string         `json:"to"`
	Type  string         `json:"type"`
	Props map[string]any `json:"props,omitempty"`
}

func (e Exporter) WriteHTML(ctx context.Context, output string) error {
	data, err := e.load(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil && filepath.Dir(output) != "." {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer file.Close()
	return pageTemplate.Execute(file, map[string]any{"GraphJSON": template.JS(payload)})
}

func (e Exporter) load(ctx context.Context) (graphData, error) {
	nodeResult, err := neo4j.ExecuteQuery(ctx, e.Driver, `
		MATCH (n:GraphNode)
		RETURN n AS node
		ORDER BY n.primaryLabel, n.id
	`, nil, neo4j.EagerResultTransformer)
	if err != nil {
		return graphData{}, err
	}
	edgeResult, err := neo4j.ExecuteQuery(ctx, e.Driver, `
		MATCH (from:GraphNode)-[r]->(to:GraphNode)
		RETURN from.id AS from, to.id AS to, type(r) AS type, properties(r) AS props
		ORDER BY type(r), from.id, to.id
	`, nil, neo4j.EagerResultTransformer)
	if err != nil {
		return graphData{}, err
	}

	data := graphData{
		GeneratedAt: time.Now().Format(time.RFC3339),
		LabelCounts: map[string]int{},
		RelCounts:   map[string]int{},
	}
	for _, record := range nodeResult.Records {
		raw := record.AsMap()["node"].(neo4j.Node)
		props := copyMap(raw.Props)
		label := stringProp(props, "primaryLabel")
		if label == "" {
			label = displayLabel(raw.Labels)
		}
		id := stringProp(props, "id")
		if id == "" {
			return graphData{}, fmt.Errorf("node without id")
		}
		data.Nodes = append(data.Nodes, nodeData{
			ID:      id,
			Label:   label,
			Name:    stringProp(props, "name"),
			Path:    stringProp(props, "path"),
			Kind:    stringProp(props, "kind"),
			Package: stringProp(props, "packageId"),
			Props:   props,
		})
		data.LabelCounts[label]++
	}
	for _, record := range edgeResult.Records {
		values := record.AsMap()
		relType := values["type"].(string)
		data.Edges = append(data.Edges, edgeData{
			From:  values["from"].(string),
			To:    values["to"].(string),
			Type:  relType,
			Props: values["props"].(map[string]any),
		})
		data.RelCounts[relType]++
	}
	return data, nil
}

func copyMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func stringProp(props map[string]any, key string) string {
	if value, ok := props[key].(string); ok {
		return value
	}
	return ""
}

func displayLabel(labels []string) string {
	for _, label := range labels {
		if label != "GraphNode" {
			return label
		}
	}
	if len(labels) > 0 {
		return labels[0]
	}
	return "Unknown"
}

var pageTemplate = template.Must(template.New("visualization").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Code Graph Visualization</title>
<style>
:root {
  color-scheme: light;
  --bg: #f7f8fa;
  --panel: #ffffff;
  --ink: #18202b;
  --muted: #667085;
  --line: #d8dee8;
  --accent: #1f7a8c;
}
* { box-sizing: border-box; }
html, body { height: 100%; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--ink);
  font: 13px/1.4 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
.app {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr) 360px;
  min-height: 100%;
}
aside {
  background: var(--panel);
  border-right: 1px solid var(--line);
  padding: 18px;
  overflow: auto;
}
.details {
  border-left: 1px solid var(--line);
  border-right: 0;
}
h1, h2, h3 { margin: 0; letter-spacing: 0; }
h1 { font-size: 18px; }
h2 { margin-top: 22px; font-size: 13px; text-transform: uppercase; color: var(--muted); }
h3 { font-size: 14px; }
.meta { margin-top: 6px; color: var(--muted); }
.stat-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-top: 18px; }
.stat { border: 1px solid var(--line); border-radius: 8px; padding: 10px; background: #fbfcfd; }
.stat strong { display: block; font-size: 20px; line-height: 1.1; }
input {
  width: 100%;
  height: 36px;
  margin-top: 14px;
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0 10px;
  color: var(--ink);
  background: #fff;
}
button {
  border: 1px solid var(--line);
  background: #fff;
  color: var(--ink);
  border-radius: 6px;
  min-height: 30px;
  padding: 6px 9px;
  cursor: pointer;
}
button.active {
  border-color: var(--accent);
  color: var(--accent);
  background: #eef8fa;
}
.chips { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.list { display: grid; gap: 8px; margin-top: 10px; }
.row {
  display: grid;
  gap: 2px;
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 9px;
  background: #fff;
  cursor: pointer;
}
.row:hover { border-color: var(--accent); }
.row .name { font-weight: 650; word-break: break-word; }
.row .sub { color: var(--muted); word-break: break-word; }
main { position: relative; min-width: 0; }
canvas { width: 100%; height: 100vh; display: block; background: #f4f6f9; }
.toolbar {
  position: absolute;
  left: 16px;
  top: 16px;
  display: flex;
  gap: 8px;
  align-items: center;
  background: rgba(255,255,255,0.9);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 8px;
  backdrop-filter: blur(8px);
}
.hint { color: var(--muted); }
pre {
  white-space: pre-wrap;
  word-break: break-word;
  background: #f4f6f9;
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 10px;
  max-height: 42vh;
  overflow: auto;
}
@media (max-width: 980px) {
  .app { grid-template-columns: 1fr; }
  aside, .details { border: 0; border-bottom: 1px solid var(--line); max-height: 40vh; }
  canvas { height: 70vh; }
}
</style>
</head>
<body>
<div class="app">
  <aside>
    <h1>Code Graph</h1>
    <div class="meta" id="generated"></div>
    <div class="stat-grid">
      <div class="stat"><strong id="nodeCount"></strong><span>nodes</span></div>
      <div class="stat"><strong id="edgeCount"></strong><span>relationships</span></div>
    </div>
    <input id="search" type="search" placeholder="Search id, path, or name">
    <h2>Labels</h2>
    <div class="chips" id="labels"></div>
    <h2>Results</h2>
    <div class="list" id="results"></div>
  </aside>
  <main>
    <canvas id="graph"></canvas>
    <div class="toolbar">
      <button id="fit">Fit</button>
      <button id="edges" class="active">Local edges</button>
      <span class="hint">Scroll to zoom, drag to pan, click a node</span>
    </div>
  </main>
  <aside class="details">
    <h2>Selection</h2>
    <div id="selection" class="meta">Click a node or search result.</div>
    <h2>Neighborhood</h2>
    <div class="list" id="neighbors"></div>
    <h2>Properties</h2>
    <pre id="props">{}</pre>
  </aside>
</div>
<script>
const graph = {{.GraphJSON}};
const colors = {
  Package: "#1f7a8c",
  Project: "#5465ff",
  File: "#457b9d",
  Symbol: "#2a9d8f",
  Route: "#e76f51",
  ExternalPackage: "#7d8597",
  ConfigKey: "#9b5de5",
  Meta: "#adb5bd"
};
const canvas = document.getElementById("graph");
const ctx = canvas.getContext("2d");
const state = { scale: 1, x: 0, y: 0, selected: null, label: "All", showEdges: true, dragging: false, lastX: 0, lastY: 0 };
const nodes = graph.nodes.map((node, index) => ({ ...node, index, x: 0, y: 0 }));
const byId = new Map(nodes.map((node) => [node.id, node]));
const edges = graph.edges.filter((edge) => byId.has(edge.from) && byId.has(edge.to));
const adjacent = new Map();
for (const edge of edges) {
  if (!adjacent.has(edge.from)) adjacent.set(edge.from, []);
  if (!adjacent.has(edge.to)) adjacent.set(edge.to, []);
  adjacent.get(edge.from).push(edge);
  adjacent.get(edge.to).push(edge);
}

function labelRank(label) {
  return ["Project", "Package", "Route", "File", "Symbol", "ConfigKey", "ExternalPackage", "Meta"].indexOf(label);
}

function layout() {
  const groups = new Map();
  for (const node of nodes) {
    if (!groups.has(node.label)) groups.set(node.label, []);
    groups.get(node.label).push(node);
  }
  const labels = [...groups.keys()].sort((a, b) => labelRank(a) - labelRank(b));
  const ringGap = 170;
  labels.forEach((label, groupIndex) => {
    const group = groups.get(label);
    const radius = 80 + groupIndex * ringGap;
    group.forEach((node, i) => {
      const angle = (i / Math.max(group.length, 1)) * Math.PI * 2;
      const jitter = hash(node.id) % 60;
      node.x = Math.cos(angle) * (radius + jitter);
      node.y = Math.sin(angle) * (radius + jitter);
    });
  });
}

function hash(value) {
  let out = 0;
  for (let i = 0; i < value.length; i++) out = ((out << 5) - out + value.charCodeAt(i)) | 0;
  return Math.abs(out);
}

function resize() {
  const rect = canvas.getBoundingClientRect();
  canvas.width = Math.floor(rect.width * devicePixelRatio);
  canvas.height = Math.floor(rect.height * devicePixelRatio);
  draw();
}

function visibleNodes() {
  return state.label === "All" ? nodes : nodes.filter((node) => node.label === state.label);
}

function draw() {
  ctx.setTransform(devicePixelRatio, 0, 0, devicePixelRatio, 0, 0);
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  ctx.save();
  ctx.translate(canvas.clientWidth / 2 + state.x, canvas.clientHeight / 2 + state.y);
  ctx.scale(state.scale, state.scale);

  const visible = new Set(visibleNodes().map((node) => node.id));
  if (state.showEdges && state.selected) {
    const localEdges = adjacent.get(state.selected.id) || [];
    ctx.lineWidth = 1 / state.scale;
    for (const edge of localEdges.slice(0, 3000)) {
      const from = byId.get(edge.from);
      const to = byId.get(edge.to);
      if (!from || !to) continue;
      ctx.strokeStyle = edge.from === state.selected.id ? "rgba(31,122,140,0.38)" : "rgba(231,111,81,0.32)";
      ctx.beginPath();
      ctx.moveTo(from.x, from.y);
      ctx.lineTo(to.x, to.y);
      ctx.stroke();
    }
  }

  for (const node of nodes) {
    if (!visible.has(node.id)) continue;
    const selected = state.selected && state.selected.id === node.id;
    ctx.fillStyle = selected ? "#111827" : (colors[node.label] || "#6c757d");
    ctx.globalAlpha = selected ? 1 : 0.72;
    ctx.beginPath();
    ctx.arc(node.x, node.y, selected ? 4.5 : 2.1, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.globalAlpha = 1;
  ctx.restore();
}

function fit() {
  const rect = canvas.getBoundingClientRect();
  const extent = nodes.reduce((max, node) => Math.max(max, Math.abs(node.x), Math.abs(node.y)), 1);
  state.scale = Math.max(0.08, Math.min(rect.width, rect.height) / (extent * 2.2));
  state.x = 0;
  state.y = 0;
  draw();
}

function screenToGraph(x, y) {
  const rect = canvas.getBoundingClientRect();
  return {
    x: (x - rect.left - rect.width / 2 - state.x) / state.scale,
    y: (y - rect.top - rect.height / 2 - state.y) / state.scale
  };
}

function pick(x, y) {
  const point = screenToGraph(x, y);
  let best = null;
  let bestDistance = 9 / state.scale;
  for (const node of visibleNodes()) {
    const distance = Math.hypot(node.x - point.x, node.y - point.y);
    if (distance < bestDistance) {
      best = node;
      bestDistance = distance;
    }
  }
  if (best) select(best);
}

function select(node) {
  state.selected = node;
  document.getElementById("selection").innerHTML = "<h3>" + escapeHtml(displayName(node)) + "</h3><div>" + escapeHtml(node.id) + "</div>";
  document.getElementById("props").textContent = JSON.stringify(node.props || {}, null, 2);
  renderNeighbors(node);
  draw();
}

function renderNeighbors(node) {
  const el = document.getElementById("neighbors");
  const local = adjacent.get(node.id) || [];
  el.innerHTML = "";
  for (const edge of local.slice(0, 30)) {
    const otherId = edge.from === node.id ? edge.to : edge.from;
    const other = byId.get(otherId);
    if (!other) continue;
    const row = document.createElement("div");
    row.className = "row";
    row.innerHTML = "<div class='name'>" + escapeHtml(edge.type) + "</div><div class='sub'>" + escapeHtml(displayName(other)) + "</div>";
    row.onclick = () => select(other);
    el.appendChild(row);
  }
  if (local.length > 30) {
    const more = document.createElement("div");
    more.className = "meta";
    more.textContent = String(local.length - 30) + " more relationships";
    el.appendChild(more);
  }
}

function displayName(node) {
  return node.name || node.path || node.id;
}

function renderLabels() {
  const el = document.getElementById("labels");
  const counts = [["All", nodes.length], ...Object.entries(graph.labelCounts).sort((a, b) => b[1] - a[1])];
  for (const [label, count] of counts) {
    const button = document.createElement("button");
    button.textContent = label + " " + count;
    button.onclick = () => {
      state.label = label;
      for (const child of el.children) child.classList.remove("active");
      button.classList.add("active");
      draw();
    };
    if (label === "All") button.classList.add("active");
    el.appendChild(button);
  }
}

function renderResults(query = "") {
  const el = document.getElementById("results");
  el.innerHTML = "";
  const normalized = query.trim().toLowerCase();
  const results = normalized
    ? nodes.filter((node) => [node.id, node.name, node.path, node.kind].some((value) => String(value || "").toLowerCase().includes(normalized))).slice(0, 50)
    : nodes.filter((node) => node.label === "Package" || node.label === "Route").slice(0, 50);
  for (const node of results) {
    const row = document.createElement("div");
    row.className = "row";
    row.innerHTML = "<div class='name'>" + escapeHtml(displayName(node)) + "</div><div class='sub'>" + escapeHtml(node.label + " | " + node.id) + "</div>";
    row.onclick = () => select(node);
    el.appendChild(row);
  }
}

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#039;" }[char]));
}

document.getElementById("generated").textContent = "Generated " + graph.generatedAt;
document.getElementById("nodeCount").textContent = nodes.length.toLocaleString();
document.getElementById("edgeCount").textContent = edges.length.toLocaleString();
document.getElementById("search").addEventListener("input", (event) => renderResults(event.target.value));
document.getElementById("fit").onclick = fit;
document.getElementById("edges").onclick = () => {
  state.showEdges = !state.showEdges;
  document.getElementById("edges").classList.toggle("active", state.showEdges);
  draw();
};
canvas.addEventListener("mousedown", (event) => {
  state.dragging = true;
  state.lastX = event.clientX;
  state.lastY = event.clientY;
});
window.addEventListener("mouseup", () => state.dragging = false);
window.addEventListener("mousemove", (event) => {
  if (!state.dragging) return;
  state.x += event.clientX - state.lastX;
  state.y += event.clientY - state.lastY;
  state.lastX = event.clientX;
  state.lastY = event.clientY;
  draw();
});
canvas.addEventListener("click", (event) => {
  if (Math.abs(event.clientX - state.lastX) < 4 && Math.abs(event.clientY - state.lastY) < 4) pick(event.clientX, event.clientY);
});
canvas.addEventListener("wheel", (event) => {
  event.preventDefault();
  const factor = event.deltaY < 0 ? 1.12 : 0.89;
  state.scale = Math.max(0.03, Math.min(8, state.scale * factor));
  draw();
}, { passive: false });
window.addEventListener("resize", resize);
layout();
renderLabels();
renderResults();
resize();
fit();
</script>
</body>
</html>`))

package capsule

import (
	"fmt"
	"strings"

	"github.com/kokistudios/card/internal/store"
)

type GraphNode struct {
	ID        string `json:"id"`
	Question  string `json:"question"`
	Choice    string `json:"choice"`
	SessionID string `json:"session_id,omitempty"`
	Distance  int    `json:"distance"` // Distance from root (0 = root)
}

type GraphEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Relationship string `json:"relationship"` // "enables", "constrains", "supersedes"
}

type GraphResult struct {
	Root   GraphNode   `json:"root"`
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
	ASCII  string      `json:"ascii"`
	Depth  int         `json:"depth"`
	Direction string   `json:"direction"`
}

type queueItem struct {
	id       string
	distance int
}

func BuildGraph(st *store.Store, rootID string, depth int, direction string) (*GraphResult, error) {
	if depth <= 0 {
		depth = 2
	}
	if direction == "" {
		direction = "both"
	}

	root, err := Get(st, rootID)
	if err != nil {
		return nil, fmt.Errorf("root capsule not found: %w", err)
	}

	result := &GraphResult{
		Root: GraphNode{
			ID:        root.ID,
			Question:  root.Question,
			Choice:    root.Choice,
			SessionID: root.SessionID,
			Distance:  0,
		},
		Nodes:     []GraphNode{},
		Edges:     []GraphEdge{},
		Depth:     depth,
		Direction: direction,
	}

	allCapsules, err := List(st, Filter{IncludeInvalidated: false})
	if err != nil {
		return nil, err
	}

	enabledByIndex := make(map[string][]string)
	constrainsIndex := make(map[string][]string)
	for _, c := range allCapsules {
		if c.EnabledBy != "" {
			enabledByIndex[c.EnabledBy] = append(enabledByIndex[c.EnabledBy], c.ID)
		}
		for _, constrainedID := range c.Constrains {
			constrainsIndex[constrainedID] = append(constrainsIndex[constrainedID], c.ID)
		}
	}

	visited := make(map[string]bool)
	queue := []queueItem{{id: rootID, distance: 0}}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if visited[item.id] {
			continue
		}
		if item.distance > depth {
			continue
		}
		visited[item.id] = true

		c, err := Get(st, item.id)
		if err != nil {
			continue
		}

		if item.distance > 0 {
			result.Nodes = append(result.Nodes, GraphNode{
				ID:        c.ID,
				Question:  c.Question,
				Choice:    c.Choice,
				SessionID: c.SessionID,
				Distance:  item.distance,
			})
		}

		if direction == "down" || direction == "both" {
			for _, enabledID := range enabledByIndex[c.ID] {
				if !visited[enabledID] {
					result.Edges = append(result.Edges, GraphEdge{
						From:         c.ID,
						To:           enabledID,
						Relationship: "enables",
					})
					queue = append(queue, queueItem{id: enabledID, distance: item.distance + 1})
				}
			}

			for _, constrainedID := range c.Constrains {
				if !visited[constrainedID] {
					result.Edges = append(result.Edges, GraphEdge{
						From:         c.ID,
						To:           constrainedID,
						Relationship: "constrains",
					})
					queue = append(queue, queueItem{id: constrainedID, distance: item.distance + 1})
				}
			}

			for _, supersededID := range c.Supersedes {
				if !visited[supersededID] {
					result.Edges = append(result.Edges, GraphEdge{
						From:         c.ID,
						To:           supersededID,
						Relationship: "supersedes",
					})
					queue = append(queue, queueItem{id: supersededID, distance: item.distance + 1})
				}
			}
		}

		if direction == "up" || direction == "both" {
			if c.EnabledBy != "" && !visited[c.EnabledBy] {
				result.Edges = append(result.Edges, GraphEdge{
					From:         c.EnabledBy,
					To:           c.ID,
					Relationship: "enables",
				})
				queue = append(queue, queueItem{id: c.EnabledBy, distance: item.distance + 1})
			}

			for _, constrainerID := range constrainsIndex[c.ID] {
				if !visited[constrainerID] {
					result.Edges = append(result.Edges, GraphEdge{
						From:         constrainerID,
						To:           c.ID,
						Relationship: "constrains",
					})
					queue = append(queue, queueItem{id: constrainerID, distance: item.distance + 1})
				}
			}

			if c.SupersededBy != "" && !visited[c.SupersededBy] {
				result.Edges = append(result.Edges, GraphEdge{
					From:         c.SupersededBy,
					To:           c.ID,
					Relationship: "supersedes",
				})
				queue = append(queue, queueItem{id: c.SupersededBy, distance: item.distance + 1})
			}
		}
	}

	result.ASCII = buildASCII(result)

	return result, nil
}

func buildASCII(g *GraphResult) string {
	var sb strings.Builder

	nodesByDistance := make(map[int][]GraphNode)
	nodesByDistance[0] = []GraphNode{g.Root}
	for _, n := range g.Nodes {
		nodesByDistance[n.Distance] = append(nodesByDistance[n.Distance], n)
	}

	edgesFrom := make(map[string][]GraphEdge)
	for _, e := range g.Edges {
		edgesFrom[e.From] = append(edgesFrom[e.From], e)
	}

	for dist := 0; dist <= g.Depth; dist++ {
		nodes := nodesByDistance[dist]
		if len(nodes) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("Level %d:\n", dist))
		for _, n := range nodes {
			marker := "  "
			if dist == 0 {
				marker = "* "
			}
			sb.WriteString(fmt.Sprintf("%s[%s] %s\n", marker, truncateID(n.ID), truncateString(n.Question, 40)))

			for _, e := range edgesFrom[n.ID] {
				sb.WriteString(fmt.Sprintf("    --%s--> [%s]\n", e.Relationship, truncateID(e.To)))
			}
		}
	}

	if len(g.Nodes) == 0 && len(g.Edges) == 0 {
		sb.WriteString("No dependency relationships found.\n")
	}

	return sb.String()
}

func truncateID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "..." + id[len(id)-8:]
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

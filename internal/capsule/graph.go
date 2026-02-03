package capsule

import (
	"fmt"
	"strings"

	"github.com/kokistudios/card/internal/store"
)

// GraphNode represents a node in the dependency graph.
type GraphNode struct {
	ID           string       `json:"id"`
	Question     string       `json:"question"`
	Choice       string       `json:"choice"`
	Significance Significance `json:"significance,omitempty"`
	SessionID    string       `json:"session_id,omitempty"`
	Distance     int          `json:"distance"` // Distance from root (0 = root)
}

// GraphEdge represents an edge in the dependency graph.
type GraphEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Relationship string `json:"relationship"` // "enables", "constrains", "supersedes"
}

// GraphResult is the result of building a dependency graph.
type GraphResult struct {
	Root   GraphNode   `json:"root"`
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
	ASCII  string      `json:"ascii"`
	Depth  int         `json:"depth"`
	Direction string   `json:"direction"`
}

// queueItem is used for BFS traversal.
type queueItem struct {
	id       string
	distance int
}

// BuildGraph traverses dependency relationships from a root capsule to the specified depth.
// direction can be "up" (traverse EnabledBy), "down" (traverse Enables/Constrains), or "both".
func BuildGraph(st *store.Store, rootID string, depth int, direction string) (*GraphResult, error) {
	// Default values
	if depth <= 0 {
		depth = 2
	}
	if direction == "" {
		direction = "both"
	}

	// Get root capsule
	root, err := Get(st, rootID)
	if err != nil {
		return nil, fmt.Errorf("root capsule not found: %w", err)
	}

	result := &GraphResult{
		Root: GraphNode{
			ID:           root.ID,
			Question:     root.Question,
			Choice:       root.Choice,
			Significance: root.Significance,
			SessionID:    root.SessionID,
			Distance:     0,
		},
		Nodes:     []GraphNode{},
		Edges:     []GraphEdge{},
		Depth:     depth,
		Direction: direction,
	}

	// Get all capsules once for reverse lookups (who enables this? who constrains this?)
	allCapsules, err := List(st, Filter{IncludeInvalidated: false})
	if err != nil {
		return nil, err
	}

	// Build index for reverse lookups
	enabledByIndex := make(map[string][]string) // capsuleID -> IDs of capsules that enable it
	constrainsIndex := make(map[string][]string) // capsuleID -> IDs of capsules that constrain it
	for _, c := range allCapsules {
		if c.EnabledBy != "" {
			enabledByIndex[c.EnabledBy] = append(enabledByIndex[c.EnabledBy], c.ID)
		}
		for _, constrainedID := range c.Constrains {
			constrainsIndex[constrainedID] = append(constrainsIndex[constrainedID], c.ID)
		}
	}

	// BFS traversal
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

		// Add node (skip root, it's already set)
		if item.distance > 0 {
			result.Nodes = append(result.Nodes, GraphNode{
				ID:           c.ID,
				Question:     c.Question,
				Choice:       c.Choice,
				Significance: c.Significance,
				SessionID:    c.SessionID,
				Distance:     item.distance,
			})
		}

		// Traverse DOWN (enables, constrains)
		if direction == "down" || direction == "both" {
			// This capsule enables others (forward lookup)
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

			// This capsule constrains others (from the capsule's own Constrains field)
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

			// Supersedes relationships
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

		// Traverse UP (enabled by, constrained by)
		if direction == "up" || direction == "both" {
			// This capsule is enabled by another
			if c.EnabledBy != "" && !visited[c.EnabledBy] {
				result.Edges = append(result.Edges, GraphEdge{
					From:         c.EnabledBy,
					To:           c.ID,
					Relationship: "enables",
				})
				queue = append(queue, queueItem{id: c.EnabledBy, distance: item.distance + 1})
			}

			// This capsule is constrained by others (reverse lookup)
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

			// This capsule is superseded by another
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

	// Build ASCII representation
	result.ASCII = buildASCII(result)

	return result, nil
}

// buildASCII creates a simple ASCII visualization of the graph.
func buildASCII(g *GraphResult) string {
	var sb strings.Builder

	// Group nodes by distance
	nodesByDistance := make(map[int][]GraphNode)
	nodesByDistance[0] = []GraphNode{g.Root}
	for _, n := range g.Nodes {
		nodesByDistance[n.Distance] = append(nodesByDistance[n.Distance], n)
	}

	// Build edge lookup
	edgesFrom := make(map[string][]GraphEdge)
	for _, e := range g.Edges {
		edgesFrom[e.From] = append(edgesFrom[e.From], e)
	}

	// Print by level
	for dist := 0; dist <= g.Depth; dist++ {
		nodes := nodesByDistance[dist]
		if len(nodes) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("Level %d:\n", dist))
		for _, n := range nodes {
			marker := "  "
			if dist == 0 {
				marker = "* " // Root marker
			}
			sb.WriteString(fmt.Sprintf("%s[%s] %s\n", marker, truncateID(n.ID), truncateString(n.Question, 40)))

			// Show edges from this node
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

// truncateID shortens a capsule ID for display.
func truncateID(id string) string {
	if len(id) <= 16 {
		return id
	}
	// Show first 8 and last 8 chars with ellipsis
	return id[:8] + "..." + id[len(id)-8:]
}

// truncateString shortens a string for display.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

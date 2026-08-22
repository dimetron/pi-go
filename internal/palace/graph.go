package palace

import (
	"context"
	"sort"
	"strings"
)

// Graph provides BFS traversal and tunnel discovery across the palace.
type Graph struct {
	store PalaceStore
}

// NewGraph creates a new Graph wrapping the given store.
func NewGraph(store PalaceStore) *Graph {
	return &Graph{store: store}
}

// graphNode holds aggregated info about a room across all wings.
type graphNode struct {
	room  string
	wings map[string]bool
	halls map[string]bool
	count int
}

// buildGraph loads all drawers and builds a room → node map.
func (g *Graph) buildGraph(ctx context.Context) (map[string]*graphNode, error) {
	drawers, err := g.store.ListDrawers(ctx, DrawerFilter{})
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]*graphNode)
	for _, d := range drawers {
		if d.Room == "general" {
			continue
		}
		n, ok := nodes[d.Room]
		if !ok {
			n = &graphNode{
				room:  d.Room,
				wings: make(map[string]bool),
				halls: make(map[string]bool),
			}
			nodes[d.Room] = n
		}
		n.wings[d.Wing] = true
		if d.Hall != "" {
			n.halls[d.Hall] = true
		}
		n.count++
	}
	return nodes, nil
}

// wingRooms returns the set of rooms belonging to a given wing.
func wingRooms(nodes map[string]*graphNode, wing string) []string {
	var rooms []string
	for room, n := range nodes {
		if n.wings[wing] {
			rooms = append(rooms, room)
		}
	}
	return rooms
}

// Traverse performs BFS from startRoom, expanding through shared wings.
// maxHops caps the traversal depth (default 2 if <= 0). Results are sorted
// by (hops ASC, drawer count DESC) and capped at 50.
func (g *Graph) Traverse(ctx context.Context, startRoom string, maxHops int) ([]TraverseResult, error) {
	if maxHops <= 0 {
		maxHops = 2
	}

	nodes, err := g.buildGraph(ctx)
	if err != nil {
		return nil, err
	}

	start, ok := nodes[startRoom]
	if !ok {
		return g.suggestRooms(nodes, startRoom), nil
	}

	visited := map[string]int{startRoom: 0}
	frontier := []string{startRoom}

	for hop := 1; hop <= maxHops && len(frontier) > 0; hop++ {
		frontier = expandFrontier(nodes, frontier, visited, hop)
	}

	results := traverseResults(nodes, visited, startRoom)

	// Include start room info at position 0 for context.
	startResult := TraverseResult{
		Room:        startRoom,
		Wings:       sortedKeys(start.wings),
		DrawerCount: start.count,
		Hops:        0,
	}
	return append([]TraverseResult{startResult}, results...), nil
}

// expandFrontier walks one BFS ring outward: every room sharing a wing with a
// frontier room and not yet visited is recorded at this hop and becomes the
// next frontier. A room already visited keeps the hop it was first given, so
// the shortest path wins.
func expandFrontier(nodes map[string]*graphNode, frontier []string, visited map[string]int, hop int) []string {
	var next []string
	for _, room := range frontier {
		node := nodes[room]
		for wing := range node.wings {
			for _, neighbor := range wingRooms(nodes, wing) {
				if _, seen := visited[neighbor]; seen {
					continue
				}
				visited[neighbor] = hop
				next = append(next, neighbor)
			}
		}
	}
	return next
}

// traverseResults turns the visited set into results sorted by
// (hops ASC, drawer count DESC) and capped at 50. The start room is excluded;
// the caller prepends it.
func traverseResults(nodes map[string]*graphNode, visited map[string]int, startRoom string) []TraverseResult {
	results := make([]TraverseResult, 0, len(visited)-1)
	for room, hops := range visited {
		if room == startRoom {
			continue
		}
		n := nodes[room]
		results = append(results, TraverseResult{
			Room:        room,
			Wings:       sortedKeys(n.wings),
			DrawerCount: n.count,
			Hops:        hops,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Hops != results[j].Hops {
			return results[i].Hops < results[j].Hops
		}
		return results[i].DrawerCount > results[j].DrawerCount
	})

	if len(results) > 50 {
		results = results[:50]
	}
	return results
}

// suggestRooms returns approximate matches when startRoom is not found.
func (g *Graph) suggestRooms(nodes map[string]*graphNode, query string) []TraverseResult {
	query = strings.ToLower(query)
	var suggestions []TraverseResult
	for room, n := range nodes {
		if strings.Contains(strings.ToLower(room), query) || strings.Contains(query, strings.ToLower(room)) {
			suggestions = append(suggestions, TraverseResult{
				Room:        room,
				Wings:       sortedKeys(n.wings),
				DrawerCount: n.count,
				Hops:        -1, // indicates suggestion, not traversal
			})
		}
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].DrawerCount > suggestions[j].DrawerCount
	})
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}
	return suggestions
}

// FindTunnels returns rooms that appear in 2+ wings, optionally filtered to
// rooms connecting wingA and wingB.
func (g *Graph) FindTunnels(ctx context.Context, wingA, wingB string) ([]Tunnel, error) {
	nodes, err := g.buildGraph(ctx)
	if err != nil {
		return nil, err
	}

	var tunnels []Tunnel
	for _, n := range nodes {
		if len(n.wings) < 2 {
			continue
		}
		if wingA != "" && !n.wings[wingA] {
			continue
		}
		if wingB != "" && !n.wings[wingB] {
			continue
		}
		tunnels = append(tunnels, Tunnel{
			Room:        n.room,
			Wings:       sortedKeys(n.wings),
			DrawerCount: n.count,
		})
	}

	sort.Slice(tunnels, func(i, j int) bool {
		return tunnels[i].DrawerCount > tunnels[j].DrawerCount
	})
	return tunnels, nil
}

// Stats returns aggregate graph statistics.
func (g *Graph) Stats(ctx context.Context) (*GraphStats, error) {
	nodes, err := g.buildGraph(ctx)
	if err != nil {
		return nil, err
	}

	tunnels, err := g.FindTunnels(ctx, "", "")
	if err != nil {
		return nil, err
	}

	// Count edges: for each room, count distinct wing memberships as edges.
	edges := 0
	for _, n := range nodes {
		wc := len(n.wings)
		if wc > 1 {
			// Each pair of wings sharing this room is an edge.
			edges += wc * (wc - 1) / 2
		}
	}

	topNames := make([]string, 0, min(3, len(tunnels)))
	for i := range tunnels {
		if i >= 3 {
			break
		}
		topNames = append(topNames, tunnels[i].Room)
	}

	return &GraphStats{
		TotalRooms:  len(nodes),
		TunnelCount: len(tunnels),
		EdgeCount:   edges,
		TopTunnels:  topNames,
	}, nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

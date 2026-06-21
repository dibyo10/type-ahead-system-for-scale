package main

import (
	"fmt"

	"typeahead/internal/cache"
)

func main() {
	// Build a ring with 3 nodes, 100 vnodes each.
	r := cache.NewRing(100)
	r.AddNode("node0")
	r.AddNode("node1")
	r.AddNode("node2")

	// A bunch of sample prefixes to track.
	prefixes := []string{"goog", "ip", "java", "map", "ebay", "yahoo", "amaz", "face", "twit", "red", "net", "you"}

	// Record which node owns each prefix with 3 nodes.
	before := make(map[string]string)
	fmt.Println("--- with 3 nodes ---")
	for _, p := range prefixes {
		owner := r.GetNode(p)
		before[p] = owner
		fmt.Printf("%-6s -> %s\n", p, owner)
	}

	// Add a 4th node and see what moves.
	r.AddNode("node3")
	fmt.Println("\n--- after adding node3 ---")
	moved := 0
	for _, p := range prefixes {
		owner := r.GetNode(p)
		status := "(same)"
		if owner != before[p] {
			status = "(MOVED from " + before[p] + ")"
			moved++
		}
		fmt.Printf("%-6s -> %s %s\n", p, owner, status)
	}

	fmt.Printf("\n%d of %d prefixes moved when adding a node (~1/N expected)\n",
		moved, len(prefixes))
}
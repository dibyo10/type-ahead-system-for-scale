package cache

import (
	"time"

	"typeahead/internal/trie"
)


type Cache struct {
	ring  *Ring
	nodes map[string]*node
}


func New(numNodes, replicas int, ttl time.Duration) *Cache {
	c := &Cache{
		ring:  NewRing(replicas),
		nodes: make(map[string]*node),
	}
	for i := 0; i < numNodes; i++ {
		name := "node" + itoa(i)
		c.ring.AddNode(name)
		c.nodes[name] = newNode(name, ttl)
	}
	return c
}


func (c *Cache) Get(prefix string) ([]trie.Suggestion, bool) {
	owner := c.ring.GetNode(prefix)
	return c.nodes[owner].get(prefix)
}


func (c *Cache) Set(prefix string, s []trie.Suggestion) {
	owner := c.ring.GetNode(prefix)
	c.nodes[owner].set(prefix, s)
}


func (c *Cache) OwnerOf(prefix string) string {
	return c.ring.GetNode(prefix)
}


func (c *Cache) Debug(prefix string) (owner string, hit bool) {
	owner = c.ring.GetNode(prefix)
	_, hit = c.nodes[owner].get(prefix)
	return owner, hit
}


func (c *Cache) AllStats() []stats {
	out := make([]stats, 0, len(c.nodes))
	for _, n := range c.nodes {
		out = append(out, n.getStats())
	}
	return out
}


func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
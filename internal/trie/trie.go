package trie

import (
	"sort"
	"sync"
)


type Trie struct{
	mu sync.RWMutex
	root *node
	topK int
}

type node struct {
	children map[rune]*node
	isWord bool
	word string
	count int64
	topK []Suggestion
}

type Suggestion struct {
	Query string `json:"query"`
	Count int64  `json:"count"`
}

func New(topK int) *Trie {
	return &Trie{
		root: &node{children: make(map[rune]*node)},
		topK: topK,
	}
}

func (t *Trie) Insert(query string, count int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cur := t.root
	for _, ch := range query { 
		next, ok := cur.children[ch]
		if !ok {
			next = &node{children: make(map[rune]*node)}
			cur.children[ch] = next
		}
		cur = next
	}
	cur.isWord = true
	cur.word = query
	cur.count = count
}

func (t *Trie) Build() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.computeTopK(t.root)
}

func (t *Trie) computeTopK(n *node) []Suggestion {
	
	var candidates []Suggestion
	for ch, child := range n.children {
		_ = ch
		childTop := t.computeTopK(child) 
		candidates = append(candidates, childTop...)
	}

	
	if n.isWord {
		candidates = append(candidates, Suggestion{Query: n.word, Count: n.count})
	}

	
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Count != candidates[j].Count {
			return candidates[i].Count > candidates[j].Count
		}
		return candidates[i].Query < candidates[j].Query
	})

	
	if len(candidates) > t.topK {
		candidates = candidates[:t.topK]
	}
	n.topK = candidates
	return candidates
}

func (t *Trie) Search(prefix string) []Suggestion {
	t.mu.RLock() 
	defer t.mu.RUnlock()

	cur := t.root
	for _, ch := range prefix {
		next, ok := cur.children[ch]
		if !ok {
			return nil 
		}
		cur = next
	}
	
	out := make([]Suggestion, len(cur.topK))
	copy(out, cur.topK)
	return out
}


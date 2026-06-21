package cache

import (
	"time"

	"typeahead/internal/trie"
)


type entry struct {
	suggestions []trie.Suggestion
	expiresAt   time.Time
}


type node struct {
	name  string
	store map[string]entry
	ttl   time.Duration

	
	getCh chan getReq
	setCh chan setReq

	
	hits   int
	misses int
	statCh chan chan stats 
}


type getReq struct {
	prefix string
	reply  chan getResp
}

type getResp struct {
	suggestions []trie.Suggestion
	hit         bool 
}


type setReq struct {
	prefix      string
	suggestions []trie.Suggestion
}

type stats struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	Hits   int    `json:"hits"`
	Misses int    `json:"misses"`
}


func newNode(name string, ttl time.Duration) *node {
	n := &node{
		name:   name,
		store:  make(map[string]entry),
		ttl:    ttl,
		getCh:  make(chan getReq),
		setCh:  make(chan setReq),
		statCh: make(chan chan stats),
	}
	go n.loop()
	return n
}


func (n *node) loop() {
	for {
		select {
		case req := <-n.getCh:
			n.handleGet(req)
		case req := <-n.setCh:
			n.store[req.prefix] = entry{
				suggestions: req.suggestions,
				expiresAt:   time.Now().Add(n.ttl),
			}
		case replyCh := <-n.statCh:
			replyCh <- stats{Name: n.name, Size: len(n.store), Hits: n.hits, Misses: n.misses}
		}
	}
}


func (n *node) handleGet(req getReq) {
	e, ok := n.store[req.prefix]
	if !ok {
		n.misses++
		req.reply <- getResp{hit: false}
		return
	}
	
	if time.Now().After(e.expiresAt) {
		delete(n.store, req.prefix)
		n.misses++
		req.reply <- getResp{hit: false}
		return
	}
	n.hits++
	req.reply <- getResp{suggestions: e.suggestions, hit: true}
}



func (n *node) get(prefix string) ([]trie.Suggestion, bool) {
	reply := make(chan getResp)
	n.getCh <- getReq{prefix: prefix, reply: reply}
	resp := <-reply
	return resp.suggestions, resp.hit
}

func (n *node) set(prefix string, s []trie.Suggestion) {
	n.setCh <- setReq{prefix: prefix, suggestions: s}
}

func (n *node) getStats() stats {
	reply := make(chan stats)
	n.statCh <- reply
	return <-reply
}
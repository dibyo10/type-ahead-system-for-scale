package cache

import (
	"hash/crc32"
	"sort"
	"strconv"
)

type Ring struct {
	replicas int            
	keys     []uint32       
	hashMap  map[uint32]string 
}

func NewRing(replicas int) *Ring {
	return &Ring{
		replicas: replicas,
		hashMap:  make(map[uint32]string),
	}
}

func (r *Ring) hash(s string) uint32 {
	return crc32.ChecksumIEEE([]byte(s))
}

func (r *Ring) AddNode(name string) {

	for i := 0; i < r.replicas; i++ {
		vnodeKey := name + "#" + strconv.Itoa(i)
		h := r.hash(vnodeKey)
		r.keys = append(r.keys, h)
		r.hashMap[h] = name
	}
	
	sort.Slice(r.keys, func(i, j int) bool { return r.keys[i] < r.keys[j] })
}

func (r *Ring) RemoveNode(name string) {
	newKeys := r.keys[:0] // reuse the underlying array
	for _, h := range r.keys {
		if r.hashMap[h] == name {
			delete(r.hashMap, h) 
			continue
		}
		newKeys = append(newKeys, h)
	}
	r.keys = newKeys
}

func (r *Ring) GetNode(key string) string {
	if len(r.keys) == 0 {
		return "" 
	}
	h := r.hash(key)

	
	idx := sort.Search(len(r.keys), func(i int) bool {
		return r.keys[i] >= h
	})

	
	if idx == len(r.keys) {
		idx = 0
	}

	return r.hashMap[r.keys[idx]]
}
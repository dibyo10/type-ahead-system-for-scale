package trending

import (
	"math"
	"sort"
	"sync"
	"time"
)


type Scorer struct {
	mu     sync.Mutex
	scores map[string]*recScore

	lambda float64       
	boost  float64       
}

type recScore struct {
	score       float64
	lastUpdated time.Time
}


func New(halfLife time.Duration) *Scorer {
	return &Scorer{
		scores: make(map[string]*recScore),
		lambda: math.Ln2 / halfLife.Seconds(), 
		boost:  1.0,
	}
}


func (s *Scorer) Record(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	rs, ok := s.scores[query]
	if !ok {
		
		s.scores[query] = &recScore{score: s.boost, lastUpdated: now}
		return
	}
	// decay the old score forward to now, THEN add the new boost
	rs.score = rs.score*s.decayFactor(now.Sub(rs.lastUpdated)) + s.boost
	rs.lastUpdated = now
}


func (s *Scorer) ScoreOf(query string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	rs, ok := s.scores[query]
	if !ok {
		return 0
	}
	return rs.score * s.decayFactor(time.Since(rs.lastUpdated))
}


func (s *Scorer) decayFactor(dt time.Duration) float64 {
	return math.Exp(-s.lambda * dt.Seconds())
}


type Scored struct {
	Query string
	Count int64
	Final float64
}

func (s *Scorer) Rerank(items []Scored, w float64) []Scored {
	for i := range items {
		rec := s.ScoreOf(items[i].Query)
		items[i].Final = float64(items[i].Count) + w*rec
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Final != items[j].Final {
			return items[i].Final > items[j].Final
		}
		return items[i].Query < items[j].Query 
	})
	return items
}
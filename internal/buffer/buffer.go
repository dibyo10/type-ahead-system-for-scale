package buffer

import (
	"context"
	"log"
	"sync"
	"time"
)

type Flusher interface {
	UpsertCounts(ctx context.Context, increments map[string]int) error
}

type Buffer struct {
	mu      sync.Mutex     
	counts  map[string]int 
	flusher       Flusher
	maxSize       int
	flushInterval time.Duration
    totalAdds   int 
	totalFlush  int 
	totalWrites int
    stopCh chan struct{}  
	wg     sync.WaitGroup 
}

func New(f Flusher, maxSize int, flushInterval time.Duration) *Buffer {
	b := &Buffer{
		counts:        make(map[string]int),
		flusher:       f,
		maxSize:       maxSize,
		flushInterval: flushInterval,
		stopCh:        make(chan struct{}),
	}
	b.wg.Add(1)
	go b.loop() 
	return b
}

func (b *Buffer) Add(query string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.counts[query]++ 
	b.totalAdds++

	
	if len(b.counts) >= b.maxSize {
		b.flushLocked() 
	}
}

func (b *Buffer) loop() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C: 
			b.flush()
		case <-b.stopCh: 
			b.flush() 
			return
		}
	}
}



func (b *Buffer) flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushLocked()
}

func (b *Buffer) flushLocked() {
	if len(b.counts) == 0 {
		return
	}

	batch := b.counts
	b.counts = make(map[string]int)

	
	b.totalFlush++
	b.totalWrites += len(batch)

	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.flusher.UpsertCounts(ctx, batch); err != nil {
		
		log.Printf("buffer flush failed (%d queries lost): %v", len(batch), err)
	}
}

func (b *Buffer) Stats() (adds, flushes, writes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalAdds, b.totalFlush, b.totalWrites
}

func (b *Buffer) Close() {
	close(b.stopCh) 
	b.wg.Wait()     
}
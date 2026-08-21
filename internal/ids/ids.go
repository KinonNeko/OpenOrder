// Package ids generates 64-bit snowflake IDs, time-ordered and JSON-safe
// when encoded as decimal strings (see docs/PROTOCOL.md §1.3).
package ids

import (
	"strconv"
	"sync"
	"time"
)

// Custom epoch: 2026-01-01T00:00:00Z.
const epochMillis = 1767225600000

type Generator struct {
	mu     sync.Mutex
	node   uint64 // 10 bits
	lastMs int64
	seq    uint64 // 12 bits
}

func NewGenerator(node uint64) *Generator {
	return &Generator{node: node & 0x3FF}
}

// Next returns a new snowflake as a decimal string.
func (g *Generator) Next() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now().UnixMilli()
	if now < g.lastMs {
		now = g.lastMs // clock went backwards; reuse last ms and rely on seq
	}
	if now == g.lastMs {
		g.seq = (g.seq + 1) & 0xFFF
		if g.seq == 0 {
			for now <= g.lastMs {
				time.Sleep(200 * time.Microsecond)
				now = time.Now().UnixMilli()
			}
		}
	} else {
		g.seq = 0
	}
	g.lastMs = now
	id := uint64(now-epochMillis)<<22 | g.node<<12 | g.seq
	return strconv.FormatUint(id, 10)
}

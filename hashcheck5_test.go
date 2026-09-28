package splitter

import (
	"fmt"
	"math/rand"
	"testing"
)

func directRoute(nodeMap map[string]nodeEnt, u uint64) string {
	best := -1.0
	name := ""
	var bseed uint64
	for nm, ent := range nodeMap {
		if ent.state != StateUp {
			continue
		}
		seed := seedOf(nm)
		v := float64(fmix64(u^seed)>>11) * 0x1.0p-53
		score := ent.weight * v
		if score > best || (score == best && seed < bseed) {
			best = score
			name = nm
			bseed = seed
		}
	}
	return name
}

func TestSnapshotVsDirect(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const n = 1000
	nodeMap := make(map[string]nodeEnt, n)
	for i := 0; i < n; i++ {
		nodeMap[fmt.Sprintf("backend-%04d", i)] = nodeEnt{weight: float64(r.Intn(9900)+100) / 100, state: StateUp}
	}
	s := buildSnapshot(nodeMap)
	if len(s.names) != n {
		t.Fatalf("snapshot has %d names, want %d", len(s.names), n)
	}
	bad := 0
	for i := 0; i < 100000; i++ {
		u := r.Uint64()
		want := directRoute(nodeMap, u)
		got := s.route(u)
		if got != want {
			if bad < 5 {
				t.Logf("u=%d: snapshot=%s want=%s", u, got, want)
			}
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d/100000 mismatches", bad)
	}
}

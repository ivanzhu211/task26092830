package poolsplit

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// TestShareAccuracy: 1000 nodes, random weights in [1,100]; millions of
// random IPv4 clients. Sum of |actual-expected| shares <= 0.06, and every
// node with weight >= 10 lands within [0.5, 1.5]x of its fair share.
func TestShareAccuracy(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	nodes := make([]Node, 1000)
	totalW := 0.0
	for i := range nodes {
		w := float64(100+rng.Intn(9901)) / 100 // 1.00 .. 100.00
		nodes[i] = Node{Name: fmt.Sprintf("n%d", i), Weight: w, State: StateUp}
		totalW += w
	}
	p := mustPool(t, nodes)

	const samples = 2_000_000
	hits := make(map[string]int, len(nodes))
	for i := 0; i < samples; i++ {
		addr := fmt.Sprintf("%d.%d.%d.%d", rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256))
		name, err := p.Lookup(addr)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		hits[name]++
	}

	sumAbsDev := 0.0
	for _, n := range nodes {
		expected := n.Weight / totalW
		actual := float64(hits[n.Name]) / samples
		sumAbsDev += math.Abs(actual - expected)
		if n.Weight >= 10 {
			ratio := actual / expected
			if ratio < 0.5 || ratio > 1.5 {
				t.Errorf("node %s (w=%.2f): ratio %.3f outside [0.5,1.5]", n.Name, n.Weight, ratio)
			}
		}
	}
	t.Logf("sum |dev| = %.4f (limit 0.06)", sumAbsDev)
	if sumAbsDev > 0.06 {
		t.Errorf("sum of abs deviations %.4f exceeds 0.06", sumAbsDev)
	}
}

// TestConsecutiveSpread: a full /16 of consecutive addresses across 20
// equal-weight nodes; each node gets 0.8..1.2x of its fair share.
func TestConsecutiveSpread(t *testing.T) {
	nodes := make([]Node, 20)
	for i := range nodes {
		nodes[i] = Node{Name: fmt.Sprintf("edge-%d", i), Weight: 50, State: StateUp}
	}
	p := mustPool(t, nodes)

	hits := make(map[string]int, 20)
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo < 256; lo++ {
			name, err := p.Lookup(fmt.Sprintf("10.0.%d.%d", hi, lo))
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			hits[name]++
		}
	}
	const total = 65536
	const fair = 1.0 / 20
	for _, n := range nodes {
		ratio := (float64(hits[n.Name]) / total) / fair
		if ratio < 0.8 || ratio > 1.2 {
			t.Errorf("node %s: ratio %.3f outside [0.8,1.2]", n.Name, ratio)
		}
	}
}

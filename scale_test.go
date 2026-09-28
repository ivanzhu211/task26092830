package poolsplit

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

func bigPool(t *testing.T, n int, weight func(i int) float64) *Pool {
	t.Helper()
	nodes := make([]Node, n)
	for i := range nodes {
		nodes[i] = Node{Name: fmt.Sprintf("backend-%d", i), Weight: weight(i), State: StateUp}
	}
	return mustPool(t, nodes)
}

// TestScalePerformance covers the 10,000-node non-functional requirements:
// lookup latency, single-change latency, full-round batch latency, memory.
func TestScalePerformance(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	p := bigPool(t, 10000, func(i int) float64 {
		return float64(100+rng.Intn(9901)) / 100 // 1.00..100.00
	})

	// Lookup latency: average well under 10us.
	addrs := make([]string, 4096)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("%d.%d.%d.%d", rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256))
	}
	const lookups = 300_000
	start := time.Now()
	for i := 0; i < lookups; i++ {
		if _, err := p.Lookup(addrs[i%len(addrs)]); err != nil {
			t.Fatalf("lookup: %v", err)
		}
	}
	avg := time.Since(start) / lookups
	t.Logf("avg lookup: %v", avg)
	if !raceEnabled && avg > 10*time.Microsecond {
		t.Errorf("avg lookup %v exceeds 10us", avg)
	}

	// Single-node change must take effect within 100ms.
	start = time.Now()
	if err := p.Apply([]Change{{Op: OpSetWeight, Name: "backend-0", Weight: 55.5}}); err != nil {
		t.Fatalf("single change: %v", err)
	}
	single := time.Since(start)
	t.Logf("single-node change: %v", single)
	if !raceEnabled && single > 100*time.Millisecond {
		t.Errorf("single-node change took %v, exceeds 100ms", single)
	}

	// A full round: every node's weight changes in one batch, within 2s.
	batch := make([]Change, 10000)
	for i := range batch {
		batch[i] = Change{Op: OpSetWeight, Name: fmt.Sprintf("backend-%d", i),
			Weight: float64(100+rng.Intn(9901)) / 100}
	}
	start = time.Now()
	if err := p.Apply(batch); err != nil {
		t.Fatalf("full round: %v", err)
	}
	round := time.Since(start)
	t.Logf("full-round batch (10k weight changes): %v", round)
	if !raceEnabled && round > 2*time.Second {
		t.Errorf("full-round batch took %v, exceeds 2s", round)
	}
}

// TestScaleMemory: 10,000 nodes all at weight 100; steady-state heap < 512MiB.
func TestScaleMemory(t *testing.T) {
	p := bigPool(t, 10000, func(i int) float64 { return 100 })
	// Trigger a change so one rebuild cycle completes, then measure steady state.
	if err := p.Apply([]Change{{Op: OpSetWeight, Name: "backend-0", Weight: 99.9}}); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	const limit = 512 << 20
	t.Logf("HeapAlloc=%d MiB HeapInuse=%d MiB (limit 512 MiB)",
		ms.HeapAlloc>>20, ms.HeapInuse>>20)
	if ms.HeapInuse > limit {
		t.Errorf("HeapInuse %d MiB exceeds 512 MiB", ms.HeapInuse>>20)
	}
	if _, err := p.Lookup("8.8.8.8"); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentLookupDuringApply: lookups racing a batch apply must return
// either the pre-change or the post-change mapping, never block excessively,
// and must follow the new state once Apply has returned.
func TestConcurrentLookupDuringApply(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	nodes := make([]Node, 500)
	for i := range nodes {
		nodes[i] = Node{Name: fmt.Sprintf("w-%d", i), Weight: float64(100+rng.Intn(9901)) / 100, State: StateUp}
	}
	p := mustPool(t, nodes)

	clients := make([]string, 2000)
	for i := range clients {
		clients[i] = fmt.Sprintf("%d.%d.%d.%d", rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256))
	}
	before := make(map[string]string, len(clients))
	for _, c := range clients {
		name, err := p.Lookup(c)
		if err != nil {
			t.Fatal(err)
		}
		before[c] = name
	}

	// Final state after the batch, computed independently.
	batch := validBatch(rng, p.Nodes(), 0)
	finalNodes := nodeMapAfter(p.Nodes(), batch)
	afterPool := mustPool(t, finalNodes)
	after := make(map[string]string, len(clients))
	for _, c := range clients {
		name, err := afterPool.Lookup(c)
		if err != nil {
			t.Fatal(err)
		}
		after[c] = name
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	violations := 0
	maxLatency := time.Duration(0)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(seed)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				c := clients[r.Intn(len(clients))]
				t0 := time.Now()
				name, err := p.Lookup(c)
				lat := time.Since(t0)
				if err != nil {
					t.Errorf("lookup err: %v", err)
					return
				}
				mu.Lock()
				if lat > maxLatency {
					maxLatency = lat
				}
				if name != before[c] && name != after[c] {
					violations++
				}
				mu.Unlock()
			}
		}(g)
	}
	time.Sleep(20 * time.Millisecond)
	if err := p.Apply(batch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	if violations > 0 {
		t.Errorf("%d lookups returned a node outside {before, after} mappings", violations)
	}
	t.Logf("max lookup latency during apply: %v", maxLatency)
	if maxLatency > 10*time.Millisecond {
		t.Errorf("lookup latency during apply reached %v, exceeds 10ms", maxLatency)
	}

	// After Apply returned, mapping must follow the new state.
	for _, c := range clients {
		name, err := p.Lookup(c)
		if err != nil {
			t.Fatal(err)
		}
		if name != after[c] {
			t.Fatalf("post-apply lookup %s -> %s, want %s", c, name, after[c])
		}
	}
}

// nodeMapAfter applies a valid batch to a node list, returning the result.
func nodeMapAfter(nodes []Node, batch []Change) []Node {
	m := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		m[n.Name] = n
	}
	for _, c := range batch {
		switch c.Op {
		case OpAdd:
			m[c.Name] = Node{Name: c.Name, Weight: c.Weight, State: c.State}
		case OpRemove:
			delete(m, c.Name)
		case OpSetWeight:
			n := m[c.Name]
			n.Weight = c.Weight
			m[c.Name] = n
		case OpSetState:
			n := m[c.Name]
			n.State = c.State
			m[c.Name] = n
		}
	}
	out := make([]Node, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	return out
}

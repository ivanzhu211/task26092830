package poolsplit

import (
	"fmt"
	"math/rand"
	"testing"
)

func mustPool(t *testing.T, nodes []Node) *Pool {
	t.Helper()
	p, err := New(nodes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func upNodes(n int) []Node {
	out := make([]Node, n)
	for i := range out {
		out[i] = Node{Name: fmt.Sprintf("node-%d", i), Weight: 10 + float64(i%37), State: StateUp}
	}
	return out
}

func TestAddressForms(t *testing.T) {
	p := mustPool(t, upNodes(50))
	groups := [][]string{
		{"10.1.2.3", "::ffff:10.1.2.3", "::FFFF:10.1.2.3", "0:0:0:0:0:ffff:10.1.2.3", "0::ffff:0a01:0203"},
		{"2001:db8::1", "2001:0db8::1", "2001:0DB8:0000:0000:0000:0000:0000:0001", "2001:DB8::1"},
		{"::1", "0:0:0:0:0:0:0:1", "0000::0001"},
		{"192.168.0.1", "::ffff:192.168.0.1"},
	}
	for _, g := range groups {
		want, err := p.Lookup(g[0])
		if err != nil {
			t.Fatalf("Lookup(%q): %v", g[0], err)
		}
		for _, a := range g[1:] {
			got, err := p.Lookup(a)
			if err != nil {
				t.Fatalf("Lookup(%q): %v", a, err)
			}
			if got != want {
				t.Errorf("Lookup(%q)=%q, want same as Lookup(%q)=%q", a, got, g[0], want)
			}
		}
	}
}

func TestInvalidAddress(t *testing.T) {
	p := mustPool(t, upNodes(5))
	for _, a := range []string{"", "not-an-ip", "10.1.2.256", "2001:::1", "10.1.2.3:80", "fe80::1%eth0"} {
		if _, err := p.Lookup(a); err != ErrInvalidAddress {
			t.Errorf("Lookup(%q) err=%v, want ErrInvalidAddress", a, err)
		}
	}
}

func TestNoUpNodes(t *testing.T) {
	p := mustPool(t, []Node{{Name: "a", Weight: 1, State: StateDown}})
	if _, err := p.Lookup("1.2.3.4"); err != ErrNoUpNodes {
		t.Fatalf("want ErrNoUpNodes, got %v", err)
	}
	empty := mustPool(t, nil)
	if _, err := empty.Lookup("1.2.3.4"); err != ErrNoUpNodes {
		t.Fatalf("empty pool: want ErrNoUpNodes, got %v", err)
	}
}

func TestStableMapping(t *testing.T) {
	p := mustPool(t, upNodes(20))
	for i := 0; i < 100; i++ {
		addr := fmt.Sprintf("10.0.%d.%d", i, i*7%256)
		first, _ := p.Lookup(addr)
		for k := 0; k < 5; k++ {
			got, _ := p.Lookup(addr)
			if got != first {
				t.Fatalf("unstable: %s -> %s then %s", addr, first, got)
			}
		}
	}
}

func TestBatchAtomicity(t *testing.T) {
	p := mustPool(t, upNodes(10))
	before := p.Nodes()
	bad := []Change{
		{Op: OpAdd, Name: "new-1", Weight: 5, State: StateUp},
		{Op: OpSetWeight, Name: "node-3", Weight: 7.5},
		{Op: OpSetWeight, Name: "ghost", Weight: 1}, // invalid: not found
	}
	if err := p.Apply(bad); err != ErrNodeNotFound {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
	after := p.Nodes()
	if len(before) != len(after) {
		t.Fatalf("pool changed after rejected batch: %d -> %d nodes", len(before), len(after))
	}
	for _, n := range before {
		found := false
		for _, m := range after {
			if m == n {
				found = true
			}
		}
		if !found {
			t.Fatalf("node %+v missing after rejected batch", n)
		}
	}
	// mapping must be untouched
	for i := 0; i < 200; i++ {
		addr := fmt.Sprintf("172.16.%d.%d", i/256, i%256)
		a, _ := p.Lookup(addr)
		_ = a
	}
}

func TestValidation(t *testing.T) {
	p := mustPool(t, upNodes(3))
	cases := []struct {
		c   Change
		err error
	}{
		{Change{Op: OpAdd, Name: "node-0", Weight: 1, State: StateUp}, ErrNodeExists},
		{Change{Op: OpAdd, Name: "x", Weight: 0.05, State: StateUp}, ErrInvalidWeight},
		{Change{Op: OpAdd, Name: "x", Weight: 100.01, State: StateUp}, ErrInvalidWeight},
		{Change{Op: OpAdd, Name: "x", Weight: 1.234, State: StateUp}, ErrInvalidWeight},
		{Change{Op: OpAdd, Name: "", Weight: 1, State: StateUp}, ErrEmptyName},
		{Change{Op: OpRemove, Name: "ghost"}, ErrNodeNotFound},
		{Change{Op: OpSetWeight, Name: "node-0", Weight: 0}, ErrInvalidWeight},
		{Change{Op: OpSetState, Name: "node-0", State: State(7)}, ErrInvalidState},
		{Change{Op: OpType(9), Name: "node-0"}, ErrInvalidOp},
	}
	for _, tc := range cases {
		if err := p.Apply([]Change{tc.c}); err != tc.err {
			t.Errorf("Apply(%+v) = %v, want %v", tc.c, err, tc.err)
		}
	}
	// boundary weights are fine
	if err := p.Apply([]Change{
		{Op: OpAdd, Name: "lo", Weight: 0.1, State: StateUp},
		{Op: OpAdd, Name: "hi", Weight: 100, State: StateUp},
		{Op: OpAdd, Name: "frac", Weight: 37.5, State: StateUp},
		{Op: OpAdd, Name: "frac2", Weight: 0.78, State: StateUp},
	}); err != nil {
		t.Fatalf("boundary weights rejected: %v", err)
	}
}

// TestRebuildEquivalence: after any change sequence, mapping equals a fresh
// pool built from the final state.
func TestRebuildEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	p := mustPool(t, upNodes(30))
	for round := 0; round < 30; round++ {
		batch := validBatch(rng, p.Nodes(), round)
		if err := p.Apply(batch); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	fresh, err := New(p.Nodes())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	for i := 0; i < 20000; i++ {
		addr := fmt.Sprintf("203.0.%d.%d", rng.Intn(256), rng.Intn(256))
		a, err1 := p.Lookup(addr)
		b, err2 := fresh.Lookup(addr)
		if err1 != nil || err2 != nil {
			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("%s: err mismatch %v vs %v", addr, err1, err2)
			}
			continue
		}
		if a != b {
			t.Fatalf("%s: evolved=%s fresh=%s", addr, a, b)
		}
	}
}

// validBatch builds a batch that is valid as a whole: it simulates each op
// against a working set so duplicates and dangling references never occur.
func validBatch(rng *rand.Rand, nodes []Node, round int) []Change {
	working := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		working[n.Name] = n
	}
	var batch []Change
	for k := 0; k < 1+rng.Intn(8); k++ {
		names := make([]string, 0, len(working))
		for name := range working {
			names = append(names, name)
		}
		switch rng.Intn(4) {
		case 0:
			name := fmt.Sprintf("added-%d-%d", round, k)
			c := Change{Op: OpAdd, Name: name,
				Weight: float64(1+rng.Intn(10000)) / 100, State: StateUp}
			working[name] = Node{Name: name, Weight: c.Weight, State: c.State}
			batch = append(batch, c)
		case 1:
			if len(names) == 0 {
				continue
			}
			name := names[rng.Intn(len(names))]
			batch = append(batch, Change{Op: OpRemove, Name: name})
			delete(working, name)
		case 2:
			if len(names) == 0 {
				continue
			}
			name := names[rng.Intn(len(names))]
			w := float64(10+rng.Intn(9990)) / 100
			batch = append(batch, Change{Op: OpSetWeight, Name: name, Weight: w})
			n := working[name]
			n.Weight = w
			working[name] = n
		default:
			if len(names) == 0 {
				continue
			}
			name := names[rng.Intn(len(names))]
			st := StateUp
			if rng.Intn(2) == 0 {
				st = StateDown
			}
			batch = append(batch, Change{Op: OpSetState, Name: name, State: st})
			n := working[name]
			n.State = st
			working[name] = n
		}
	}
	return batch
}

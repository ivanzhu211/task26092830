package poolsplit

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestMovementRules runs a long random sequence of batches (valid and
// invalid) and checks every client move against the movement rules:
// a client may move A->B only if A was removed/downed/de-weighted in this
// batch, or B was added/upped/re-weighted in this batch.
func TestMovementRules(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p := mustPool(t, upNodes(40))

	clients := make([]string, 0, 4000)
	for i := 0; i < 3000; i++ {
		clients = append(clients, fmt.Sprintf("%d.%d.%d.%d",
			rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256)))
	}
	for i := 0; i < 1000; i++ {
		clients = append(clients, fmt.Sprintf("2001:%x::%x:%x",
			rng.Intn(0x10000), rng.Intn(0x10000), rng.Intn(0x10000)))
	}

	snapshotMap := func() map[string]string {
		m := make(map[string]string, len(clients))
		for _, c := range clients {
			name, err := p.Lookup(c)
			if err == ErrNoUpNodes {
				continue
			}
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			m[c] = name
		}
		return m
	}
	nodeMap := func() map[string]Node {
		m := make(map[string]Node)
		for _, n := range p.Nodes() {
			m[n.Name] = n
		}
		return m
	}

	before := snapshotMap()
	for round := 0; round < 120; round++ {
		beforeNodes := nodeMap()
		batch := validBatch(rng, p.Nodes(), round)

		// Every third round, poison the batch: it must be rejected wholesale.
		poisoned := round%3 == 2
		if poisoned {
			batch = append(batch, Change{Op: OpSetWeight, Name: "ghost-node", Weight: 5})
		}
		err := p.Apply(batch)
		if poisoned {
			if err == nil {
				t.Fatalf("round %d: poisoned batch accepted", round)
			}
			after := snapshotMap()
			for c, a := range before {
				if after[c] != a {
					t.Fatalf("round %d: rejected batch moved %s: %s->%s", round, c, a, after[c])
				}
			}
			continue
		}
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}

		afterNodes := nodeMap()
		after := snapshotMap()

		downgraded := func(name string) bool {
			b, okB := beforeNodes[name]
			a, okA := afterNodes[name]
			if !okA {
				return true // removed
			}
			if !okB {
				return false
			}
			if b.State == StateUp && a.State == StateDown {
				return true // downed
			}
			return a.Weight < b.Weight // de-weighted
		}
		upgraded := func(name string) bool {
			b, okB := beforeNodes[name]
			a, okA := afterNodes[name]
			if !okB {
				return true // added
			}
			if !okA {
				return false
			}
			if b.State == StateDown && a.State == StateUp {
				return true // upped
			}
			return a.Weight > b.Weight // re-weighted
		}

		moves := 0
		for c, a := range before {
			b, ok := after[c]
			if !ok {
				continue // pool had no UP nodes after
			}
			if a == b {
				continue
			}
			moves++
			if !downgraded(a) && !upgraded(b) {
				t.Fatalf("round %d: illegal move %s: %s->%s (batch=%+v)",
					round, c, a, b, batch)
			}
		}
		_ = moves
		before = after
	}
}

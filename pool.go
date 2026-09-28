package poolsplit

import (
	"math"
	"sync"
	"sync/atomic"
)

// Pool is a source-address sharding pool. Lookups never block on changes:
// Apply swaps in a fully built immutable snapshot with a single pointer store.
type Pool struct {
	snap atomic.Pointer[snapshot]

	mu       sync.Mutex // serializes Apply
	slotName []string   // slot -> name, "" = free
	freeSlot []uint32
	nodeSlot map[string]uint32
}

func validWeight(w float64) bool {
	if w < minWeight || w > maxWeight {
		return false
	}
	return math.Abs(w*100-math.Round(w*100)) < 1e-6
}

func validState(s State) bool { return s == StateUp || s == StateDown }

func validateNode(n Node, seen map[string]bool) error {
	if n.Name == "" {
		return ErrEmptyName
	}
	if seen[n.Name] {
		return ErrNodeExists
	}
	seen[n.Name] = true
	if !validWeight(n.Weight) {
		return ErrInvalidWeight
	}
	if !validState(n.State) {
		return ErrInvalidState
	}
	return nil
}

// New creates a pool from an initial node list.
func New(nodes []Node) (*Pool, error) {
	if len(nodes) > maxNodes {
		return nil, ErrTooManyNodes
	}
	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if err := validateNode(n, seen); err != nil {
			return nil, err
		}
	}
	p := &Pool{nodeSlot: make(map[string]uint32, len(nodes))}
	s := &snapshot{nodes: make(map[string]Node, len(nodes))}
	var ring []vnode
	for _, n := range nodes {
		slot := uint32(len(p.slotName))
		p.slotName = append(p.slotName, n.Name)
		p.nodeSlot[n.Name] = slot
		s.slotName = append(s.slotName, n.Name)
		s.nodes[n.Name] = n
		if n.State == StateUp {
			ring = genVnodes(ring, n.Name, n.Weight, slot)
		}
	}
	sortVnodes(ring)
	s.ring = ring
	p.snap.Store(s)
	return p, nil
}

// Apply atomically applies a batch of changes. If any change is invalid the
// whole batch is rejected and the pool is left unchanged.
func (p *Pool) Apply(changes []Change) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cur := p.snap.Load()
	nodes := make(map[string]Node, len(cur.nodes))
	for name, n := range cur.nodes {
		nodes[name] = n
	}

	// Validate + apply the whole batch against the cloned state first.
	for _, c := range changes {
		n, exists := nodes[c.Name]
		switch c.Op {
		case OpAdd:
			if c.Name == "" {
				return ErrEmptyName
			}
			if exists {
				return ErrNodeExists
			}
			if len(nodes) >= maxNodes {
				return ErrTooManyNodes
			}
			if !validWeight(c.Weight) {
				return ErrInvalidWeight
			}
			if !validState(c.State) {
				return ErrInvalidState
			}
			nodes[c.Name] = Node{Name: c.Name, Weight: c.Weight, State: c.State}
		case OpRemove:
			if !exists {
				return ErrNodeNotFound
			}
			delete(nodes, c.Name)
		case OpSetWeight:
			if !exists {
				return ErrNodeNotFound
			}
			if !validWeight(c.Weight) {
				return ErrInvalidWeight
			}
			n.Weight = c.Weight
			nodes[c.Name] = n
		case OpSetState:
			if !exists {
				return ErrNodeNotFound
			}
			if !validState(c.State) {
				return ErrInvalidState
			}
			n.State = c.State
			nodes[c.Name] = n
		default:
			return ErrInvalidOp
		}
	}

	// Diff old vs new to find slots whose ring entries must change.
	changed := make(map[uint32]bool)
	var fresh []vnode
	var removedSlots []uint32

	for name, oldN := range cur.nodes {
		newN, ok := nodes[name]
		slot := p.nodeSlot[name]
		if !ok { // removed
			changed[slot] = true
			p.slotName[slot] = ""
			removedSlots = append(removedSlots, slot)
			delete(p.nodeSlot, name)
			continue
		}
		if oldN.Weight != newN.Weight || oldN.State != newN.State {
			changed[slot] = true
			if newN.State == StateUp {
				fresh = genVnodes(fresh, name, newN.Weight, slot)
			}
		}
	}
	for name, newN := range nodes {
		if _, ok := cur.nodes[name]; ok {
			continue
		}
		var slot uint32
		if len(p.freeSlot) > 0 {
			slot = p.freeSlot[len(p.freeSlot)-1]
			p.freeSlot = p.freeSlot[:len(p.freeSlot)-1]
			p.slotName[slot] = name
		} else {
			slot = uint32(len(p.slotName))
			p.slotName = append(p.slotName, name)
		}
		p.nodeSlot[name] = slot
		changed[slot] = true
		if newN.State == StateUp {
			fresh = genVnodes(fresh, name, newN.Weight, slot)
		}
	}
	p.freeSlot = append(p.freeSlot, removedSlots...)
	newSlotName := append([]string{}, p.slotName...)

	if len(changed) == 0 {
		// No ring-relevant change; still refresh the node table.
		p.snap.Store(&snapshot{ring: cur.ring, slotName: newSlotName, nodes: nodes})
		return nil
	}

	sortVnodes(fresh)
	ring := rebuildRing(cur.ring, changed, fresh)
	p.snap.Store(&snapshot{ring: ring, slotName: newSlotName, nodes: nodes})
	return nil
}

// Lookup returns the name of the node a client address maps to.
func (p *Pool) Lookup(clientAddr string) (string, error) {
	h, err := hashAddr(clientAddr)
	if err != nil {
		return "", err
	}
	return p.snap.Load().lookup(h)
}

// Nodes returns the current node list (unordered).
func (p *Pool) Nodes() []Node {
	s := p.snap.Load()
	out := make([]Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	return out
}

package poolsplit

import (
	"math"
	"sort"
)

// vnode is one point on the hash ring.
type vnode struct {
	hash uint64
	slot uint32 // index into snapshot.slotName
}

// snapshot is an immutable view of the pool; swapped in atomically.
// Ring content depends only on the current set of (name, weight, state):
// vnode hashes derive from node name + vnode index, nothing else.
type snapshot struct {
	ring     []vnode         // sorted by hash, UP nodes only
	slotName []string        // slot -> node name ("" = inactive slot)
	nodes    map[string]Node // full pool state
}

// vnodeCount maps a weight to a number of ring points.
// Depends only on the node's own weight, so unrelated nodes never move.
func vnodeCount(w float64) int {
	n := int(math.Round(w * vnodesPerW))
	if n < 1 {
		n = 1
	}
	return n
}

// genVnodes appends the ring points of one node to dst.
func genVnodes(dst []vnode, name string, weight float64, slot uint32) []vnode {
	seed := hashName(name)
	for j := 0; j < vnodeCount(weight); j++ {
		dst = append(dst, vnode{hash: vnodeHash(seed, uint32(j)), slot: slot})
	}
	return dst
}

// sortVnodes sorts vnodes by hash with an LSD radix sort (8-bit passes).
// Falls back to insertion sort for tiny inputs.
func sortVnodes(v []vnode) {
	if len(v) < 64 {
		for i := 1; i < len(v); i++ {
			for j := i; j > 0 && v[j].hash < v[j-1].hash; j-- {
				v[j], v[j-1] = v[j-1], v[j]
			}
		}
		return
	}
	buf := make([]vnode, len(v))
	var count [256]int
	src, dst := v, buf
	for shift := uint(0); shift < 64; shift += 8 {
		for i := range count {
			count[i] = 0
		}
		for _, e := range src {
			count[byte(e.hash>>shift)]++
		}
		sum := 0
		for i := range count {
			count[i], sum = sum, sum+count[i]
		}
		for _, e := range src {
			d := byte(e.hash >> shift)
			dst[count[d]] = e
			count[d]++
		}
		src, dst = dst, src
	}
	if &src[0] != &v[0] {
		copy(v, src)
	}
}

// rebuildRing produces a new sorted ring: old entries of changed slots are
// dropped, new vnodes (already sorted) are merged in. O(len(old)+len(fresh)).
func rebuildRing(old []vnode, changed map[uint32]bool, fresh []vnode) []vnode {
	out := make([]vnode, 0, len(old)+len(fresh))
	i, j := 0, 0
	for i < len(old) {
		e := old[i]
		if changed[e.slot] {
			i++
			continue
		}
		for j < len(fresh) && fresh[j].hash < e.hash {
			out = append(out, fresh[j])
			j++
		}
		out = append(out, e)
		i++
	}
	out = append(out, fresh[j:]...)
	return out
}

// lookup returns the node name owning the ring arc containing h.
func (s *snapshot) lookup(h uint64) (string, error) {
	n := len(s.ring)
	if n == 0 {
		return "", ErrNoUpNodes
	}
	i := sort.Search(n, func(i int) bool { return s.ring[i].hash >= h })
	if i == n {
		i = 0 // wrap around the ring
	}
	return s.slotName[s.ring[i].slot], nil
}

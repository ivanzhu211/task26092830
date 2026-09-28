package splitter

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestHashDebug3(t *testing.T) {
	const n = 1000
	seeds := make([]uint64, n)
	for i := 0; i < n; i++ {
		seeds[i] = seedOf(fmt.Sprintf("backend-%04d", i))
	}
	r := rand.New(rand.NewSource(1))
	wins := make([]int, n)
	const trials = 2_000_000
	for t_ := 0; t_ < trials; t_++ {
		u := r.Uint64()
		best, bi := uint64(0), -1
		for j := 0; j < n; j++ {
			if v := fmix64(u ^ seeds[j]); v > best {
				best, bi = v, j
			}
		}
		wins[bi]++
	}
	distinct := 0
	maxW, minW := 0, trials
	for _, w := range wins {
		if w > 0 {
			distinct++
		}
		if w > maxW {
			maxW = w
		}
		if w < minW {
			minW = w
		}
	}
	t.Logf("fmix(u^seed): distinct winners %d/%d, max wins %d, min wins %d, expect %d each", distinct, n, maxW, minW, trials/n)

	// 对照：每次用独立随机数（理想情况）
	wins2 := make([]int, n)
	for t_ := 0; t_ < trials; t_++ {
		best, bi := uint64(0), -1
		for j := 0; j < n; j++ {
			if v := r.Uint64(); v > best {
				best, bi = v, j
			}
		}
		wins2[bi]++
	}
	distinct2 := 0
	for _, w := range wins2 {
		if w > 0 {
			distinct2++
		}
	}
	t.Logf("ideal independent: distinct winners %d/%d", distinct2, n)
}

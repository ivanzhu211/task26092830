package splitter

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestHashDebug4(t *testing.T) {
	const n = 1000
	seeds := make([]uint64, n)
	for i := 0; i < n; i++ {
		seeds[i] = seedOf(fmt.Sprintf("backend-%04d", i))
	}
	r := rand.New(rand.NewSource(7))
	wins := make([]int, n)
	const trials = 2_000_000
	for t_ := 0; t_ < trials; t_++ {
		// 模拟 parseClient 的 IPv4 路径：k1=0，k2 含 32 位地址熵
		k2 := uint64(0xff)<<16 | uint64(0xff)<<24 |
			uint64(r.Intn(256))<<32 | uint64(r.Intn(256))<<40 |
			uint64(r.Intn(256))<<48 | uint64(r.Intn(256))<<56
		u := fmix64(0 ^ fmix64(k2))
		best, bi := uint64(0), -1
		for j := 0; j < n; j++ {
			if v := fmix64(u ^ seeds[j]); v > best {
				best, bi = v, j
			}
		}
		wins[bi]++
	}
	distinct := 0
	for _, w := range wins {
		if w > 0 {
			distinct++
		}
	}
	t.Logf("IPv4-path u: distinct winners %d/%d", distinct, n)
}

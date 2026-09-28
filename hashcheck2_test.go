package splitter

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestHashDebug2(t *testing.T) {
	seeds := make([]uint64, 20)
	for i := 0; i < 20; i++ {
		seeds[i] = seedOf(fmt.Sprintf("node-%02d", i))
	}
	countWins := func(us []uint64) []int {
		wins := make([]int, 20)
		for _, u := range us {
			best, bi := uint64(0), -1
			for j, s := range seeds {
				if v := u ^ s; v > best {
					best, bi = v, j
				}
			}
			wins[bi]++
		}
		return wins
	}
	// 随机 u
	r := rand.New(rand.NewSource(1))
	us := make([]uint64, 65536)
	for i := range us {
		us[i] = r.Uint64()
	}
	t.Logf("random u wins: %v", countWins(us))

	// 连续地址的 u
	us2 := make([]uint64, 0, 65536)
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo < 256; lo++ {
			k2 := uint64(0xff)<<16 | uint64(0xff)<<24 | uint64(10)<<32 | uint64(hi)<<48 | uint64(lo)<<56
			us2 = append(us2, fmix64(fmix64(k2)))
		}
	}
	t.Logf("fmix(fmix(k2)) wins: %v", countWins(us2))

	// u 的高 32 位分布直方图（65536 桶太细，用 256 桶）
	hist := make([]int, 256)
	for _, u := range us2 {
		hist[u>>56]++
	}
	t.Logf("u top8bits hist[0:16]: %v", hist[:16])
}

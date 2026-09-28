package splitter

import (
	"fmt"
	"testing"
)

// 检查同一客户端下各节点 v 值是否均匀、不同客户端的胜者是否均匀。
func TestHashDebug(t *testing.T) {
	// 20 个节点的种子
	seeds := make([]uint64, 20)
	for i := 0; i < 20; i++ {
		seeds[i] = seedOf(fmt.Sprintf("node-%02d", i))
	}
	// 检查 1：固定客户端，v 的高 8 位分布
	u := fmix64(12345)
	hist := make([]int, 16)
	for _, s := range seeds {
		v := (u ^ s) >> 60 // 高 4 位
		hist[v]++
	}
	t.Logf("one client, top4bits of v across 20 seeds: %v", hist)

	// 检查 2：连续地址的 u 值高 16 位是否均匀
	histU := make([]int, 16)
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo < 256; lo++ {
			k2 := uint64(0xff)<<16 | uint64(0xff)<<24 | uint64(10)<<32 | uint64(hi)<<48 | uint64(lo)<<56
			u := fmix64(0 ^ fmix64(k2))
			histU[u>>60]++
		}
	}
	t.Logf("consecutive addrs, top4bits of u: %v", histU)

	// 检查 3：连续地址下每个节点胜出的次数（直接算 argmax，不走 snapshot）
	wins := make([]int, 20)
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo < 256; lo++ {
			k2 := uint64(0xff)<<16 | uint64(0xff)<<24 | uint64(10)<<32 | uint64(hi)<<48 | uint64(lo)<<56
			u := fmix64(fmix64(k2))
			best, bi := uint64(0), -1
			for j, s := range seeds {
				v := u ^ s
				if v > best {
					best, bi = v, j
				}
			}
			wins[bi]++
		}
	}
	t.Logf("direct argmax wins per node: %v", wins)
}

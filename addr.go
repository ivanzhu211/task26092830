package splitter

import (
	"encoding/binary"
	"net/netip"
)

// parseClient 把 IPv4/IPv6 文本地址归一化为 64 位哈希键。
// 同一地址的不同写法（IPv6 缩写/完整/大小写、IPv4 映射写法）得到相同的键。
func parseClient(addr string) (uint64, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, ErrInvalidAddress
	}
	if a.Zone() != "" {
		return 0, ErrInvalidAddress
	}
	// IPv4 映射写法（::ffff:10.1.2.3）归一化为 IPv4。
	a = a.Unmap()
	b := a.As16()
	k1 := binary.LittleEndian.Uint64(b[0:8])
	k2 := binary.LittleEndian.Uint64(b[8:16])
	return fmix64(k1 ^ fmix64(k2)), nil
}

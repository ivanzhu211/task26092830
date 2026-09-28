package poolsplit

import (
	"encoding/binary"
	"hash/fnv"
	"net/netip"
)

// splitmix64 finalizer: deterministic, good avalanche, very fast.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// hashName returns a deterministic per-node seed (stable across processes).
func hashName(name string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	return h.Sum64()
}

// vnodeHash returns the ring position of the j-th vnode of a node.
func vnodeHash(nameSeed uint64, j uint32) uint64 {
	return mix64(nameSeed + uint64(j)*0x9e3779b97f4a7c15)
}

// hashAddr parses a client address (IPv4/IPv6 text, any canonical form)
// and returns a deterministic 64-bit hash of its normalized form.
// IPv4-mapped IPv6 addresses normalize to the same value as plain IPv4.
func hashAddr(addr string) (uint64, error) {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, ErrInvalidAddress
	}
	if ip.Zone() != "" {
		return 0, ErrInvalidAddress
	}
	ip = ip.Unmap()
	if ip.Is4() {
		b := ip.As4()
		return mix64(uint64(binary.BigEndian.Uint32(b[:])) + 0x04), nil
	}
	b := ip.As16()
	lo := binary.BigEndian.Uint64(b[:8])
	hi := binary.BigEndian.Uint64(b[8:])
	return mix64(lo ^ mix64(hi+0x06)), nil
}

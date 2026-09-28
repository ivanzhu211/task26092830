package splitter

import "sort"

// blockShift 决定选路块大小（16 个节点一块）。
const blockShift = 4
const blockSize = 1 << blockShift

// block 是选路数组上一段连续区间，按块内最大权重降序排列，
// 选路时当前最优分已不低于块最大权重的块可整块跳过。
type block struct {
	start, end int32
	maxW       float64
}

// snapshot 是某一时刻池子状态的不可变选路视图。
// 选路只读 snapshot，Apply 构建新 snapshot 后原子替换。
type snapshot struct {
	names   []string
	seeds   []uint64
	weights []float64
	blocks  []block
}

// buildSnapshot 由当前节点表构建选路视图（只含 UP 节点）。
// 节点顺序是状态的纯函数（按名字排序后分块、按块最大权重稳定排序），
// 与节点加入先后、历史变更无关。
func buildSnapshot(nodes map[string]nodeEnt) *snapshot {
	up := make([]string, 0, len(nodes))
	for name, n := range nodes {
		if n.state == StateUp {
			up = append(up, name)
		}
	}
	sort.Strings(up)

	n := len(up)
	s := &snapshot{
		names:   make([]string, n),
		seeds:   make([]uint64, n),
		weights: make([]float64, n),
	}
	if n == 0 {
		return s
	}

	type blk struct {
		start, end int32
		maxW       float64
	}
	nblocks := (n + blockSize - 1) / blockSize
	blks := make([]blk, 0, nblocks)
	for start := 0; start < n; start += blockSize {
		end := start + blockSize
		if end > n {
			end = n
		}
		maxW := 0.0
		for i := start; i < end; i++ {
			if w := nodes[up[i]].weight; w > maxW {
				maxW = w
			}
		}
		blks = append(blks, blk{int32(start), int32(end), maxW})
	}
	sort.SliceStable(blks, func(a, b int) bool {
		return blks[a].maxW > blks[b].maxW
	})

	s.blocks = make([]block, len(blks))
	pos := 0
	for bi, b := range blks {
		s.blocks[bi] = block{int32(pos), int32(pos + int(b.end-b.start)), b.maxW}
		for i := b.start; i < b.end; i++ {
			name := up[i]
			s.names[pos] = name
			s.seeds[pos] = seedOf(name)
			s.weights[pos] = nodes[name].weight
			pos++
		}
	}
	return s
}

// route 对 64 位客户端键选路，返回节点名。调用前保证有 UP 节点。
//
// 加权会合哈希：score_i = weight_i * v(fmix64(u ^ seed_i))，fmix64 把
// u^seed_i 雪崩成近似独立的均匀哈希（裸 u^seed_i 的 argmax 概率由种子间
// 结构决定，严重不均，必须再混合），v 把 64 位均匀映射到 [0,1)，取 argmax。
// 每个客户端命中节点 i 的概率恰为 weight_i / Σweight。
// 分数只取决于当前节点名与权重，因此映射与历史无关；未变化的节点分数不变，
// 客户端只在源节点降权/下线/删除或目标节点升权/上线/新增时才会移动。
func (s *snapshot) route(u uint64) string {
	names := s.names
	seeds := s.seeds
	weights := s.weights

	best := -1.0
	bestIdx := -1
	var bestSeed uint64
	for bi := range s.blocks {
		blk := &s.blocks[bi]
		if blk.maxW <= best {
			break // 块按 maxW 降序，之后的块都不可能胜出
		}
		for j := blk.start; j < blk.end; j++ {
			w := weights[j]
			if w <= best {
				continue // score = w*v < w，不可能胜出
			}
			v := float64(fmix64(u^seeds[j])>>11) * 0x1.0p-53
			score := w * v
			if score > best || (score == best && seeds[j] < bestSeed) {
				best = score
				bestIdx = int(j)
				bestSeed = seeds[j]
			}
		}
	}
	return names[bestIdx]
}

// fmix64 是 murmur3 的 64 位最终混合函数，雪崩特性好。
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// seedOf 由节点名确定性地导出 64 位种子，跨进程稳定。
func seedOf(name string) uint64 {
	h := uint64(fnvOffset64)
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= fnvPrime64
	}
	return fmix64(h ^ 0x9E3779B97F4A7C15)
}

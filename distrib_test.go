package splitter

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// TestShareAccuracy 份额标准：1000 个节点、权重 1..100 随机，
// 几百万随机 IPv4 下，各节点实际份额与应得份额之差的绝对值之和不超过 0.06；
// 权重不低于 10 的节点，实际份额在应得份额的 [0.5, 1.5] 倍之间。
func TestShareAccuracy(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const nNodes = 1000
	specs := make([]NodeSpec, 0, nNodes)
	totalW := 0.0
	for i := 0; i < nNodes; i++ {
		w := float64(r.Intn(9900)+100) / 100 // 1.00..100.00
		specs = append(specs, NodeSpec{Name: fmt.Sprintf("backend-%04d", i), Weight: w, State: StateUp})
		totalW += w
	}
	p, err := New(specs)
	if err != nil {
		t.Fatal(err)
	}

	const nClients = 4_000_000
	counts := make(map[string]int, nNodes)
	for i := 0; i < nClients; i++ {
		addr := fmt.Sprintf("%d.%d.%d.%d", r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256))
		name, err := p.Route(addr)
		if err != nil {
			t.Fatal(err)
		}
		counts[name]++
	}
	if len(counts) != nNodes {
		t.Fatalf("only %d/%d nodes received traffic", len(counts), nNodes)
	}

	sumDev := 0.0
	worstRatio, worstNode := 1.0, ""
	for _, spec := range specs {
		want := spec.Weight / totalW
		got := float64(counts[spec.Name]) / nClients
		sumDev += math.Abs(got - want)
		if spec.Weight >= 10 {
			ratio := got / want
			if ratio < 0.5 || ratio > 1.5 {
				t.Fatalf("node %s (w=%v): share ratio %v out of [0.5,1.5]", spec.Name, spec.Weight, ratio)
			}
			if d := math.Abs(ratio - 1); d > math.Abs(worstRatio-1) {
				worstRatio, worstNode = ratio, spec.Name
			}
		}
	}
	t.Logf("sum|dev| = %.4f (limit 0.06), worst w>=10 ratio = %.3f (%s)", sumDev, worstRatio, worstNode)
	if sumDev > 0.06 {
		t.Fatalf("sum of abs share deviations %.4f exceeds 0.06", sumDev)
	}
}

// TestConsecutiveAddresses 连续地址不能扎堆：10.0.0.0/16 全部 65536 个地址
// 分到 20 个权重都是 50 的节点，每个节点份额在应得份额的 [0.8, 1.2] 倍之间。
func TestConsecutiveAddresses(t *testing.T) {
	const nNodes = 20
	specs := make([]NodeSpec, 0, nNodes)
	for i := 0; i < nNodes; i++ {
		specs = append(specs, NodeSpec{Name: fmt.Sprintf("node-%02d", i), Weight: 50, State: StateUp})
	}
	p, err := New(specs)
	if err != nil {
		t.Fatal(err)
	}

	const total = 65536
	counts := make(map[string]int, nNodes)
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo < 256; lo++ {
			name, err := p.Route(fmt.Sprintf("10.0.%d.%d", hi, lo))
			if err != nil {
				t.Fatal(err)
			}
			counts[name]++
		}
	}
	want := float64(total) / nNodes
	for _, spec := range specs {
		got := float64(counts[spec.Name])
		ratio := got / want
		if ratio < 0.8 || ratio > 1.2 {
			t.Fatalf("node %s: share ratio %.3f out of [0.8,1.2] (got %d, want %.0f)", spec.Name, ratio, counts[spec.Name], want)
		}
	}
	t.Logf("per-node counts min=%d max=%d (want %.0f each)", minCount(counts), maxCount(counts), want)
}

func minCount(m map[string]int) int {
	min := math.MaxInt
	for _, v := range m {
		if v < min {
			min = v
		}
	}
	return min
}

func maxCount(m map[string]int) int {
	max := 0
	for _, v := range m {
		if v > max {
			max = v
		}
	}
	return max
}

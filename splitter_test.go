package splitter

import (
	"errors"
	"testing"
)

func mustPool(t *testing.T, specs ...NodeSpec) *Pool {
	t.Helper()
	p, err := New(specs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestRouteStableAndUpOnly(t *testing.T) {
	p := mustPool(t,
		NodeSpec{Name: "a", Weight: 10, State: StateUp},
		NodeSpec{Name: "b", Weight: 20, State: StateUp},
		NodeSpec{Name: "c", Weight: 30, State: StateDown},
	)
	got, err := p.Route("192.168.1.1")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if got != "a" && got != "b" {
		t.Fatalf("routed to DOWN or unknown node %q", got)
	}
	for i := 0; i < 10; i++ {
		again, _ := p.Route("192.168.1.1")
		if again != got {
			t.Fatalf("unstable route: %q then %q", got, again)
		}
	}
}

func TestRouteInvalidAddress(t *testing.T) {
	p := mustPool(t, NodeSpec{Name: "a", Weight: 1, State: StateUp})
	for _, bad := range []string{"", "10.1.2.256", ":::", "hello", "10.1.2", "fe80::1%eth0", "1.2.3.4.5"} {
		if _, err := p.Route(bad); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("addr %q: want ErrInvalidAddress, got %v", bad, err)
		}
	}
}

func TestRouteNoUpNode(t *testing.T) {
	p := mustPool(t, NodeSpec{Name: "a", Weight: 1, State: StateDown})
	if _, err := p.Route("10.0.0.1"); !errors.Is(err, ErrNoUpNode) {
		t.Fatalf("want ErrNoUpNode, got %v", err)
	}
	empty := mustPool(t)
	if _, err := empty.Route("10.0.0.1"); !errors.Is(err, ErrNoUpNode) {
		t.Fatalf("empty pool: want ErrNoUpNode, got %v", err)
	}
}

func TestAddressForms(t *testing.T) {
	p := mustPool(t,
		NodeSpec{Name: "a", Weight: 10, State: StateUp},
		NodeSpec{Name: "b", Weight: 20, State: StateUp},
		NodeSpec{Name: "c", Weight: 30, State: StateUp},
	)
	groups := [][]string{
		{"10.1.2.3", "::ffff:10.1.2.3", "::ffff:0a01:0203", "::FFFF:10.1.2.3"},
		{"2001:db8::1", "2001:DB8::1", "2001:db8:0:0:0:0:0:1", "2001:0db8:0000:0000:0000:0000:0000:0001"},
		{"::1", "0:0:0:0:0:0:0:1", "0000::0001"},
		{"0.0.0.1", "::ffff:0.0.0.1"},
	}
	for _, group := range groups {
		want, err := p.Route(group[0])
		if err != nil {
			t.Fatalf("Route %q: %v", group[0], err)
		}
		for _, addr := range group[1:] {
			got, err := p.Route(addr)
			if err != nil {
				t.Fatalf("Route %q: %v", addr, err)
			}
			if got != want {
				t.Fatalf("%q -> %q, but %q -> %q", addr, got, group[0], want)
			}
		}
	}
	// 不同地址不应全部被强制到同一节点（ sanity：至少两种结果）。
	seen := map[string]bool{}
	for _, group := range groups {
		r, _ := p.Route(group[0])
		seen[r] = true
	}
	if len(seen) < 2 {
		t.Fatalf("all distinct addresses routed to %v", seen)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New([]NodeSpec{{Name: "a", Weight: 1}, {Name: "a", Weight: 2}}); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("dup name: %v", err)
	}
	if _, err := New([]NodeSpec{{Name: "", Weight: 1}}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty name: %v", err)
	}
	for _, w := range []float64{0, 0.09, 100.01, 101, -1, 1.234, 0.555} {
		if _, err := New([]NodeSpec{{Name: "a", Weight: w}}); !errors.Is(err, ErrInvalidWeight) {
			t.Fatalf("weight %v: want ErrInvalidWeight, got %v", w, err)
		}
	}
	for _, w := range []float64{0.1, 0.78, 37.5, 100, 1, 99.99} {
		if _, err := New([]NodeSpec{{Name: "a", Weight: w}}); err != nil {
			t.Fatalf("weight %v should be valid: %v", w, err)
		}
	}
	if _, err := New([]NodeSpec{{Name: "a", Weight: 1, State: State(7)}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("bad state: %v", err)
	}
}

func TestApplyAtomicRejection(t *testing.T) {
	p := mustPool(t,
		NodeSpec{Name: "a", Weight: 10, State: StateUp},
		NodeSpec{Name: "b", Weight: 20, State: StateUp},
	)
	before, _ := p.Route("8.8.8.8")

	badBatches := [][]Change{
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpAdd, Name: "a", Weight: 1}},                 // 新增已存在
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpRemove, Name: "ghost"}},                     // 删除不存在
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpSetWeight, Name: "ghost", Weight: 1}},       // 改不存在
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpSetWeight, Name: "b", Weight: 0.01}},        // 权重越界
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpSetWeight, Name: "b", Weight: 100.001}},     // 权重越界
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpSetWeight, Name: "b", Weight: 33.333}},      // 超过两位小数
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: OpSetState, Name: "b", State: State(3)}},      // 非法状态
		{{Op: OpSetWeight, Name: "a", Weight: 50}, {Op: Op(99), Name: "b"}},                           // 非法操作
		{{Op: OpAdd, Name: "n", Weight: 5}, {Op: OpAdd, Name: "n", Weight: 6}},                        // 重复新增已存在
	}
	for i, batch := range badBatches {
		if err := p.Apply(batch); err == nil {
			t.Fatalf("batch %d should be rejected", i)
		}
		if len(p.nodes) != 2 || p.nodes["a"].weight != 10 || p.nodes["b"].weight != 20 {
			t.Fatalf("batch %d mutated pool on rejection", i)
		}
		after, _ := p.Route("8.8.8.8")
		if after != before {
			t.Fatalf("batch %d changed mapping on rejection", i)
		}
	}
}

func TestApplyMixedBatch(t *testing.T) {
	p := mustPool(t,
		NodeSpec{Name: "a", Weight: 10, State: StateUp},
		NodeSpec{Name: "b", Weight: 20, State: StateDown},
	)
	err := p.Apply([]Change{
		{Op: OpAdd, Name: "c", Weight: 37.5, State: StateUp},
		{Op: OpRemove, Name: "a"},
		{Op: OpSetWeight, Name: "b", Weight: 0.78},
		{Op: OpSetState, Name: "b", State: StateUp},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(p.nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(p.nodes))
	}
	if p.nodes["b"].weight != 0.78 || p.nodes["b"].state != StateUp {
		t.Fatalf("b = %+v", p.nodes["b"])
	}
	if p.nodes["c"].weight != 37.5 || p.nodes["c"].state != StateUp {
		t.Fatalf("c = %+v", p.nodes["c"])
	}
	for i := 0; i < 100; i++ {
		got, err := p.Route("10.9.8.7")
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if got != "b" && got != "c" {
			t.Fatalf("routed to removed node %q", got)
		}
	}
}

func TestDownUpRoundTripRestoresMapping(t *testing.T) {
	p := mustPool(t,
		NodeSpec{Name: "a", Weight: 10, State: StateUp},
		NodeSpec{Name: "b", Weight: 20, State: StateUp},
		NodeSpec{Name: "c", Weight: 30, State: StateUp},
	)
	addrs := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5"}
	before := map[string]string{}
	for _, a := range addrs {
		r, _ := p.Route(a)
		before[a] = r
	}
	// 下线再上线、降权再改回，映射必须和原来一模一样。
	if err := p.Apply([]Change{{Op: OpSetState, Name: "b", State: StateDown}}); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply([]Change{{Op: OpSetWeight, Name: "a", Weight: 55.5}}); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply([]Change{
		{Op: OpSetState, Name: "b", State: StateUp},
		{Op: OpSetWeight, Name: "a", Weight: 10},
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		got, _ := p.Route(a)
		if got != before[a] {
			t.Fatalf("%s: was %q, now %q after round trip", a, before[a], got)
		}
	}
}

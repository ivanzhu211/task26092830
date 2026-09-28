package splitter

import (
	"fmt"
	"math/rand"
	"testing"
)

// effWeight 节点的有效权重：DOWN 或不存在时为 0。
func effWeight(n nodeEnt, ok bool) float64 {
	if !ok || n.state != StateUp {
		return 0
	}
	return n.weight
}

func randWeight(r *rand.Rand) float64 {
	// 0.1 到 100，两位小数
	return float64(r.Intn(9990)+10) / 100
}

func randClients(r *rand.Rand, n int) []string {
	clients := make([]string, 0, n)
	for len(clients) < n {
		switch r.Intn(4) {
		case 0: // IPv4
			clients = append(clients, fmt.Sprintf("%d.%d.%d.%d", r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256)))
		case 1: // IPv4 映射写法
			clients = append(clients, fmt.Sprintf("::ffff:%d.%d.%d.%d", r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256)))
		case 2: // IPv6 缩写
			clients = append(clients, fmt.Sprintf("2001:db8:%x::%x", r.Intn(65536), r.Intn(65536)))
		default: // IPv6 完整写法
			clients = append(clients, fmt.Sprintf("fd00:%04x:%04x:%04x:%04x:%04x:%04x:%04x",
				r.Intn(65536), r.Intn(65536), r.Intn(65536), r.Intn(65536), r.Intn(65536), r.Intn(65536), r.Intn(65536)))
		}
	}
	return clients
}

func routeAll(t *testing.T, p *Pool, clients []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(clients))
	for _, c := range clients {
		name, err := p.Route(c)
		if err != nil {
			t.Fatalf("Route(%q): %v", c, err)
		}
		m[c] = name
	}
	return m
}

func cloneNodes(src map[string]nodeEnt) map[string]nodeEnt {
	dst := make(map[string]nodeEnt, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// genBatch 生成一个随机批次。valid 为 false 时注入一条非法变更。
func genBatch(r *rand.Rand, nodes map[string]nodeEnt, counter *int, valid bool) []Change {
	// avail 跟踪批内顺序语义下当前存在的节点，保证生成的批次合法。
	avail := map[string]bool{}
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
		avail[name] = true
	}
	k := 1 + r.Intn(15)
	batch := make([]Change, 0, k)
	for len(batch) < k {
		switch r.Intn(4) {
		case 0: // Add
			if len(avail) >= 260 {
				continue
			}
			*counter++
			name := fmt.Sprintf("n%d", *counter)
			st := StateUp
			if r.Intn(4) == 0 {
				st = StateDown
			}
			batch = append(batch, Change{Op: OpAdd, Name: name, Weight: randWeight(r), State: st})
			avail[name] = true
			names = append(names, name)
		case 1: // Remove
			if len(names) <= 40 {
				continue
			}
			name := names[r.Intn(len(names))]
			if !avail[name] {
				continue
			}
			batch = append(batch, Change{Op: OpRemove, Name: name})
			delete(avail, name)
		case 2: // SetWeight
			name := names[r.Intn(len(names))]
			if !avail[name] {
				continue
			}
			batch = append(batch, Change{Op: OpSetWeight, Name: name, Weight: randWeight(r)})
		default: // SetState
			name := names[r.Intn(len(names))]
			if !avail[name] {
				continue
			}
			st := StateUp
			if r.Intn(2) == 0 {
				st = StateDown
			}
			batch = append(batch, Change{Op: OpSetState, Name: name, State: st})
		}
	}
	if !valid {
		removed := map[string]bool{}
		for _, c := range batch {
			if c.Op == OpRemove {
				removed[c.Name] = true
			}
		}
		inject := r.Intn(3)
		if inject == 0 { // 新增已存在且本批未删除的名字
			cands := make([]string, 0, len(names))
			for _, n := range names {
				if !removed[n] {
					cands = append(cands, n)
				}
			}
			if len(cands) > 0 {
				batch = append(batch, Change{Op: OpAdd, Name: cands[r.Intn(len(cands))], Weight: randWeight(r)})
			} else {
				inject = 1
			}
		}
		if inject == 1 { // 删除不存在的节点
			batch = append(batch, Change{Op: OpRemove, Name: "ghost-node-never-exists"})
		}
		if inject == 2 { // 权重越界
			batch = append(batch, Change{Op: OpSetWeight, Name: names[r.Intn(len(names))], Weight: 1000.5})
		}
	}
	return batch
}

// upCount 返回 UP 节点数。
func upCount(nodes map[string]nodeEnt) int {
	c := 0
	for _, n := range nodes {
		if n.state == StateUp {
			c++
		}
	}
	return c
}

func TestMovementRulesAndHistoryIndependence(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	counter := 0
	specs := make([]NodeSpec, 0, 200)
	for i := 0; i < 200; i++ {
		counter++
		st := StateUp
		if r.Intn(5) == 0 {
			st = StateDown
		}
		specs = append(specs, NodeSpec{Name: fmt.Sprintf("n%d", counter), Weight: randWeight(r), State: st})
	}
	p, err := New(specs)
	if err != nil {
		t.Fatal(err)
	}
	clients := randClients(r, 5000)

	const batches = 300
	for round := 0; round < batches; round++ {
		// 保证池子里始终有足够的 UP 节点，Route 才有意义。
		if upCount(p.nodes) < 20 {
			if err := p.Apply(fixUpBatch(p.nodes)); err != nil {
				t.Fatal(err)
			}
		}
		before := cloneNodes(p.nodes)
		beforeRoute := routeAll(t, p, clients)

		valid := r.Intn(4) != 0 // 1/4 概率生成非法批次
		batch := genBatch(r, p.nodes, &counter, valid)
		err := p.Apply(batch)
		if !valid {
			if err == nil {
				t.Fatalf("round %d: invalid batch accepted: %+v", round, batch)
			}
			after := routeAll(t, p, clients)
			for c, want := range beforeRoute {
				if after[c] != want {
					t.Fatalf("round %d: rejected batch moved client %s: %s -> %s", round, c, want, after[c])
				}
			}
			continue
		}
		if err != nil {
			t.Fatalf("round %d: valid batch rejected: %v\nbatch=%+v", round, err, batch)
		}
		after := cloneNodes(p.nodes)
		afterRoute := routeAll(t, p, clients)

		// 挪动规则：客户端 A->B 只在 A 有效权重下降或 B 有效权重上升时允许。
		moved := 0
		for c, a := range beforeRoute {
			b := afterRoute[c]
			if a == b {
				continue
			}
			moved++
			aBefore := effWeight(before[a], true)
			aAfter, aOK := after[a]
			bBefore, bOK := before[b]
			bAfter := effWeight(after[b], true)
			aDegraded := !aOK || effWeight(aAfter, true) < aBefore
			bImproved := !bOK || bAfter > effWeight(bBefore, true)
			if !aDegraded && !bImproved {
				t.Fatalf("round %d: client %s moved %s(eff %v->%v) -> %s(eff %v->%v), neither degraded source nor improved target",
					round, c, a, aBefore, effWeight(aAfter, aOK), b, effWeight(bBefore, bOK), bAfter)
			}
		}
		if round%50 == 0 {
			t.Logf("round %d: %d/%d clients moved", round, moved, len(clients))
		}
	}

	// 历史无关：任意一串变更后的映射，与用同样最终状态直接新建的池子完全一致。
	finalSpecs := make([]NodeSpec, 0, len(p.nodes))
	for name, n := range p.nodes {
		finalSpecs = append(finalSpecs, NodeSpec{Name: name, Weight: n.weight, State: n.state})
	}
	fresh, err := New(finalSpecs)
	if err != nil {
		t.Fatal(err)
	}
	if upCount(p.nodes) > 0 {
		got := routeAll(t, p, clients)
		want := routeAll(t, fresh, clients)
		for c, w := range want {
			if got[c] != w {
				t.Fatalf("history-dependent mapping for %s: pool=%s fresh=%s", c, got[c], w)
			}
		}
	}
}

// fixUpBatch 把若干 DOWN 节点拉上线，保证池子有 UP 节点。
func fixUpBatch(nodes map[string]nodeEnt) []Change {
	batch := make([]Change, 0, 30)
	for name, n := range nodes {
		if n.state == StateDown {
			batch = append(batch, Change{Op: OpSetState, Name: name, State: StateUp})
			if len(batch) >= 30 {
				break
			}
		}
	}
	return batch
}

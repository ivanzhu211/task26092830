// Package splitter 提供按客户端地址分流到后端节点的内存库。
// 使用加权会合哈希（weighted rendezvous hashing）保证：
// 同一客户端稳定落到同一节点；映射只取决于当前节点名、权重、状态；
// 变更只挪动必须挪动的客户端。
package splitter

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
)

// State 节点状态。
type State int

const (
	// StateUp 节点在线，接收流量。
	StateUp State = iota
	// StateDown 节点下线，不接收流量。
	StateDown
)

// NodeSpec 描述一个节点的完整规格，用于新建池子。
type NodeSpec struct {
	Name   string
	Weight float64
	State  State
}

// Op 变更操作类型。
type Op int

const (
	// OpAdd 新增节点（名字不得已存在）。
	OpAdd Op = iota
	// OpRemove 删除节点（名字必须存在）。
	OpRemove
	// OpSetWeight 修改权重（名字必须存在）。
	OpSetWeight
	// OpSetState 修改状态（名字必须存在）。
	OpSetState
)

// Change 一条变更。Apply 接受一批 Change，整批原子生效或整批拒绝。
// 批内各条按顺序依次校验并作用于同一临时状态：前一条的效果对后一条可见
// （例如可以先 OpAdd 再对同一节点 OpSetWeight）。
type Change struct {
	Op     Op
	Name   string
	Weight float64 // OpAdd、OpSetWeight 使用
	State  State   // OpAdd、OpSetState 使用
}

// 错误定义。
var (
	ErrNoUpNode       = errors.New("splitter: no UP node in pool")
	ErrInvalidAddress = errors.New("splitter: invalid client address")
	ErrNodeExists     = errors.New("splitter: node already exists")
	ErrNodeNotFound   = errors.New("splitter: node not found")
	ErrInvalidWeight  = errors.New("splitter: weight out of range or precision")
	ErrInvalidState   = errors.New("splitter: invalid state")
	ErrInvalidOp      = errors.New("splitter: invalid change op")
	ErrEmptyName      = errors.New("splitter: empty node name")
)

const (
	minWeight = 0.1
	maxWeight = 100.0
)

// nodeEnt 池内节点的当前状态（可变，仅由 Apply 在锁内修改）。
type nodeEnt struct {
	weight float64
	state  State
}

// Pool 分流池。对并发 Route 安全；Apply 与 Route 可并发。
type Pool struct {
	mu    sync.Mutex
	nodes map[string]nodeEnt
	snap  atomic.Pointer[snapshot]
}

// New 用给定节点清单新建池子。节点名必须唯一，权重必须合法。
func New(nodes []NodeSpec) (*Pool, error) {
	m := make(map[string]nodeEnt, len(nodes))
	for _, spec := range nodes {
		if spec.Name == "" {
			return nil, ErrEmptyName
		}
		if _, ok := m[spec.Name]; ok {
			return nil, fmt.Errorf("%w: %q", ErrNodeExists, spec.Name)
		}
		if !validWeight(spec.Weight) {
			return nil, fmt.Errorf("%w: %q weight %v", ErrInvalidWeight, spec.Name, spec.Weight)
		}
		if spec.State != StateUp && spec.State != StateDown {
			return nil, fmt.Errorf("%w: %q state %d", ErrInvalidState, spec.Name, spec.State)
		}
		m[spec.Name] = nodeEnt{weight: spec.Weight, state: spec.State}
	}
	p := &Pool{nodes: m}
	p.snap.Store(buildSnapshot(m))
	return p, nil
}

// Route 返回客户端地址应去的节点名。
// 地址非法返回 ErrInvalidAddress；池内无 UP 节点返回 ErrNoUpNode。
func (p *Pool) Route(clientAddr string) (string, error) {
	u, err := parseClient(clientAddr)
	if err != nil {
		return "", err
	}
	s := p.snap.Load()
	if s == nil || len(s.names) == 0 {
		return "", ErrNoUpNode
	}
	return s.route(u), nil
}

// Apply 原子地应用一批变更：任一条不合法则整批拒绝，池子保持原样。
// Apply 返回后，之后的选路一律按新状态走。
func (p *Pool) Apply(changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	next := make(map[string]nodeEnt, len(p.nodes)+len(changes))
	for name, n := range p.nodes {
		next[name] = n
	}
	for i := range changes {
		c := &changes[i]
		if c.Name == "" {
			return ErrEmptyName
		}
		n, exists := next[c.Name]
		switch c.Op {
		case OpAdd:
			if exists {
				return fmt.Errorf("%w: %q", ErrNodeExists, c.Name)
			}
			if !validWeight(c.Weight) {
				return fmt.Errorf("%w: %q weight %v", ErrInvalidWeight, c.Name, c.Weight)
			}
			if c.State != StateUp && c.State != StateDown {
				return fmt.Errorf("%w: %q state %d", ErrInvalidState, c.Name, c.State)
			}
			next[c.Name] = nodeEnt{weight: c.Weight, state: c.State}
		case OpRemove:
			if !exists {
				return fmt.Errorf("%w: %q", ErrNodeNotFound, c.Name)
			}
			delete(next, c.Name)
		case OpSetWeight:
			if !exists {
				return fmt.Errorf("%w: %q", ErrNodeNotFound, c.Name)
			}
			if !validWeight(c.Weight) {
				return fmt.Errorf("%w: %q weight %v", ErrInvalidWeight, c.Name, c.Weight)
			}
			n.weight = c.Weight
			next[c.Name] = n
		case OpSetState:
			if !exists {
				return fmt.Errorf("%w: %q", ErrNodeNotFound, c.Name)
			}
			if c.State != StateUp && c.State != StateDown {
				return fmt.Errorf("%w: %q state %d", ErrInvalidState, c.Name, c.State)
			}
			n.state = c.State
			next[c.Name] = n
		default:
			return fmt.Errorf("%w: %d", ErrInvalidOp, c.Op)
		}
	}
	p.nodes = next
	p.snap.Store(buildSnapshot(next))
	return nil
}

// validWeight 检查权重是否在 [0.1, 100] 且最多两位小数。
func validWeight(w float64) bool {
	if w < minWeight || w > maxWeight || math.IsNaN(w) {
		return false
	}
	return math.Abs(w*100-math.Round(w*100)) < 1e-6
}

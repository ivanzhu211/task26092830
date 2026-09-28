package poolsplit

import "errors"

// State is the health state of a node. Only StateUp nodes receive traffic.
type State int

const (
	StateDown State = iota
	StateUp
)

// Node describes a single backend node in the pool.
type Node struct {
	Name   string
	Weight float64
	State  State
}

// OpType is the kind of a single change inside a batch.
type OpType int

const (
	OpAdd       OpType = iota // add a new node (Name must not exist)
	OpRemove                  // remove an existing node
	OpSetWeight               // change weight of an existing node
	OpSetState                // change state of an existing node
)

// Change is a single mutation; a batch of changes is applied atomically.
type Change struct {
	Op     OpType
	Name   string
	Weight float64 // OpAdd / OpSetWeight
	State  State   // OpAdd / OpSetState
}

var (
	ErrInvalidAddress = errors.New("poolsplit: invalid client address")
	ErrNoUpNodes      = errors.New("poolsplit: no UP nodes in pool")
	ErrNodeExists     = errors.New("poolsplit: node already exists")
	ErrNodeNotFound   = errors.New("poolsplit: node not found")
	ErrInvalidWeight  = errors.New("poolsplit: weight out of range or precision")
	ErrTooManyNodes   = errors.New("poolsplit: node count exceeds limit")
	ErrEmptyName      = errors.New("poolsplit: empty node name")
	ErrInvalidOp      = errors.New("poolsplit: unknown change op")
	ErrInvalidState   = errors.New("poolsplit: unknown node state")
)

const (
	minWeight  = 0.1
	maxWeight  = 100.0
	maxNodes   = 10000
	vnodesPerW = 10 // vnode points per unit of weight
)

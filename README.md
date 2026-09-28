# poolsplit

按客户端地址分流到后端节点的 Go 库。纯内存实现，不落盘，由上层流量调度进程直接调用，无接口层。

## 用法

```go
pool, err := poolsplit.New([]poolsplit.Node{
    {Name: "backend-a", Weight: 37.5, State: poolsplit.StateUp},
    {Name: "backend-b", Weight: 0.78, State: poolsplit.StateDown},
})

// 选路：同一地址（含 IPv6 缩写/全写、大小写、IPv4 映射写法）永远得到同一节点
name, err := pool.Lookup("::ffff:10.1.2.3")

// 批量变更：新增/删除/改权重/改状态可混合，整批原子生效；
// 任何一条不合法则整批拒绝，池子保持原样
err = pool.Apply([]poolsplit.Change{
    {Op: poolsplit.OpAdd, Name: "backend-c", Weight: 10, State: poolsplit.StateUp},
    {Op: poolsplit.OpSetWeight, Name: "backend-a", Weight: 18.75},
    {Op: poolsplit.OpSetState, Name: "backend-b", State: poolsplit.StateUp},
    {Op: poolsplit.OpRemove, Name: "backend-c"},
})
```

## 设计

- **加权一致性哈希环**：每个 UP 节点按自身权重放置 `round(weight*10)` 个虚拟节点，
  位置由 `mix64(fnv64a(name), vnode序号)` 决定——只取决于节点名、权重、状态，
  与加入顺序、变更历史、进程无关，天然满足确定性映射。
- **最小挪动**：未变更节点的虚拟节点位置与数量完全不变，因此只有被降权/下线/删除
  节点的客户端会迁出，只有升权/上线/新增节点会接收客户端。
- **快照隔离**：`Apply` 在后台构建完整新快照后用一次原子指针交换生效；
  `Lookup` 只读当前快照，变更期间零阻塞，结果必为变更前或变更后的合法映射。
- **性能**：环用基数排序（LSD, 8 轮）构建，增量变更走"过滤 + 归并"O(环长)；
  查找为一次二分。10,000 节点（权重 1–100 随机）实测：选路均值约 250ns，
  单节点变更约 35ms，全量权重轮换约 200ms，权重全 100 时堆内存约 155MiB。

## 约束

- 权重：0.1–100，最多两位小数；节点数上限 10,000。
- 地址：IPv4/IPv6 文本；`::ffff:a.b.c.d` 与 `a.b.c.d` 视为同一客户端；带 zone 的地址视为非法。

## 测试

```sh
go test ./...        # 功能 + 份额精度 + 连续地址散布 + 挪动规则 + 规模性能
go test -race ./...  # 并发正确性（race 模式下跳过耗时断言）
```

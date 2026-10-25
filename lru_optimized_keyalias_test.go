/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-10-25 11:07:52
 * @FilePath: \go-cachex\lru_optimized_keyalias_test.go
 * @Description: LRUOptimizedHandler 键别名回归测试
 *
 * 覆盖缺陷：SetWithTTLAndCtx 曾以 zeroAllocByteToString 产出的 unsafe 别名作为 map 键存储，
 * 调用方复用 key 缓冲区后键原地变异，导致条目落入错误哈希桶 → 静默丢数据 →
 * 条目被归还 sync.Pool 后仍可被损坏键解析到 → removeEntry 空指针解引用 → 整进程 SIGSEGV。
 *
 * 该缺陷属内存别名而非数据竞争，-race 检测不到，必须由本测试覆盖。
 */

package cachex

import (
	"fmt"
	"testing"
)

// TestLRUOptimized_ReusedKeyBufferNoDataLoss 复用 key 缓冲区不得丢数据
//
// Handler 契约（cache.go: Set(key, value []byte) error）未禁止调用方复用入参缓冲区，
// 且其余 handler 均按值拷贝键，故此处行为必须一致。
func TestLRUOptimized_ReusedKeyBufferNoDataLoss(t *testing.T) {
	h := NewLRUOptimizedHandler(256)

	// 同一缓冲区反复承载不同键——池化热路径的常规写法
	buf := make([]byte, 8, 256)

	copy(buf, []byte("alpha-01"))
	if err := h.Set(buf, []byte("A")); err != nil {
		t.Fatalf("写入 alpha-01 失败: %v", err)
	}

	copy(buf, []byte("beta--02"))
	if err := h.Set(buf, []byte("B")); err != nil {
		t.Fatalf("写入 beta--02 失败: %v", err)
	}

	// 缺陷存在时：alpha-01 的 map 键已随 buf 变异为 beta--02，首条记录查不到
	got, err := h.Get([]byte("alpha-01"))
	if err != nil {
		t.Fatalf("alpha-01 丢失（键别名缺陷）: %v", err)
	}
	if string(got) != "A" {
		t.Fatalf("alpha-01 值错误: 期望 A，实得 %q", got)
	}

	got, err = h.Get([]byte("beta--02"))
	if err != nil {
		t.Fatalf("beta--02 丢失: %v", err)
	}
	if string(got) != "B" {
		t.Fatalf("beta--02 值错误: 期望 B，实得 %q", got)
	}
}

// TestLRUOptimized_ReusedKeyBufferUnderEviction 复用缓冲区 + 触发淘汰不得崩溃
//
// 缺陷存在时本用例以 SIGSEGV 终止（removeEntry 解引用已归还 sync.Pool 的条目），
// 而非普通断言失败——所以它同时是崩溃回归与数据完整性回归。
func TestLRUOptimized_ReusedKeyBufferUnderEviction(t *testing.T) {
	const (
		capacity = 256
		writes   = 50000 // 远超容量，持续触发淘汰路径
	)
	h := NewLRUOptimizedHandler(capacity)

	buf := make([]byte, 16, 256)
	for i := 0; i < writes; i++ {
		copy(buf, fmt.Sprintf("%016d", i))
		if err := h.Set(buf, []byte("v")); err != nil {
			t.Fatalf("第 %d 次写入失败: %v", i, err)
		}
	}

	// 能走到这里即未崩溃；再校验容量账目未被损坏键带偏
	if n := entryCount(t, h); n > capacity {
		t.Fatalf("条目数 %d 超出容量上限 %d（键损坏导致淘汰失效）", n, capacity)
	}
}

// entryCount 从 Stats 读取当前条目数
func entryCount(t *testing.T, h *LRUOptimizedHandler) int {
	t.Helper()
	raw, ok := h.Stats()["entries"]
	if !ok {
		t.Fatalf("Stats 缺少 entries 字段")
	}
	switch n := raw.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	default:
		t.Fatalf("entries 类型非预期: %T", raw)
		return 0
	}
}

// TestLRUOptimized_FreshKeyBufferControl 对照组：每次新建键切片
//
// 该路径在缺陷存在时同样通过（MultiLevelCache 正是这么调用的，故线上未暴露），
// 用于证明上面两个用例的失败确由缓冲区复用引起，而非容量或淘汰逻辑本身的问题。
func TestLRUOptimized_FreshKeyBufferControl(t *testing.T) {
	const capacity = 256
	h := NewLRUOptimizedHandler(capacity)

	for i := 0; i < 50000; i++ {
		if err := h.Set([]byte(fmt.Sprintf("%016d", i)), []byte("v")); err != nil {
			t.Fatalf("第 %d 次写入失败: %v", i, err)
		}
	}

	if n := entryCount(t, h); n > capacity {
		t.Fatalf("条目数 %d 超出容量上限 %d", n, capacity)
	}
}

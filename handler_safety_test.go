/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-11-02 18:02:03
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-11-02 18:02:03
 * @FilePath: \go-cachex\handler_safety_test.go
 * @Description: handler 的并发安全与缓冲区隔离 | Handler concurrency safety and buffer isolation
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ==== 关闭标志的并发读写 | Concurrent access to the closed flag ====

// closed 曾是受 mu 保护的普通 bool，但 GetWithCtx 这类入口把它当实参传给校验函数，
// 实参在函数体取锁之前就求值了，于是读发生在锁外，与 Close 的写构成真竞争
// 现有用例一个都没覆盖「Get 与 Close 并发」这个场景，所以 -race 跑十个种子也抓不到
//
// [EN] The closed flag used to be a plain bool guarded by mu, but entry points pass it by
// value to the validation helper and an argument is evaluated before the body takes any lock,
// so the read happened outside it and raced with Close. No existing case covered a Get
// concurrent with a Close, which is why ten shuffle seeds under -race never caught it
func TestHandlerCloseIsRaceFreeWithGet(t *testing.T) {
	cases := []struct {
		name string
		make func() Handler
	}{
		{"LRU", func() Handler { return NewLRUHandler(128) }},
		{"Expiring", func() Handler { return NewExpiringHandler(time.Hour) }},
		{"LRUOptimized", func() Handler { return NewLRUOptimizedHandler(128) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			for i := 0; i < 100; i++ {
				h := tc.make()
				if err := h.SetWithCtx(ctx, []byte("k"), []byte("v")); err != nil {
					t.Fatalf("写入失败：%v", err)
				}

				var wg sync.WaitGroup
				wg.Add(4)
				go func() { defer wg.Done(); _, _ = h.GetWithCtx(ctx, []byte("k")) }()
				go func() { defer wg.Done(); _, _ = h.GetTTLWithCtx(ctx, []byte("k")) }()
				go func() { defer wg.Done(); _ = h.DelWithCtx(ctx, []byte("k")) }()
				go func() { defer wg.Done(); _ = h.Close() }()
				wg.Wait()
			}
		})
	}
}

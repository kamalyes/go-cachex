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

// ==== 缓冲区隔离 | Buffer isolation ====

// 每个 handler 都必须在写入时拷贝入参、在读取时拷贝出参，否则调用方复用自己的缓冲区
// 或改写拿到的切片，都会直接改掉缓存里的内容
// RistrettoHandler 两个方向都没拷贝过，而同文件的 BatchGetWithCtx 一直是拷贝的
//
// [EN] Every handler has to copy on the way in and on the way out, or a caller reusing its
// own buffer, or writing to the slice it received, rewrites what is in the cache.
// RistrettoHandler copied in neither direction while BatchGetWithCtx in the same file did
func TestHandlerDoesNotShareBufferWithCaller(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T) Handler
	}{
		{"LRU", func(*testing.T) Handler { return NewLRUHandler(128) }},
		{"Expiring", func(*testing.T) Handler { return NewExpiringHandler(time.Hour) }},
		{"LRUOptimized", func(*testing.T) Handler { return NewLRUOptimizedHandler(128) }},
		{"Ristretto", func(t *testing.T) Handler {
			h, err := NewRistrettoHandler(nil)
			if err != nil {
				t.Fatalf("建 Ristretto handler 失败：%v", err)
			}
			return h
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			defer func() { _ = h.Close() }()

			ctx := context.Background()
			key := []byte("isolation-key")
			const want = "ORIGINAL-VALUE"

			// 写入用的切片，之后故意复用它
			input := []byte(want)
			if err := h.SetWithCtx(ctx, key, input); err != nil {
				t.Fatalf("写入失败：%v", err)
			}

			// 调用方复用自己的缓冲区 —— 不该影响缓存
			for i := range input {
				input[i] = 'A'
			}
			got, err := h.GetWithCtx(ctx, key)
			if err != nil {
				t.Fatalf("改写入参后读取失败：%v", err)
			}
			if string(got) != want {
				t.Errorf("写入没拷贝：调用方复用缓冲区后缓存变成 %q，应为 %q", got, want)
			}

			// 调用方改写读到的切片 —— 也不该影响缓存
			for i := range got {
				got[i] = 'X'
			}
			again, err := h.GetWithCtx(ctx, key)
			if err != nil {
				t.Fatalf("改写返回值后读取失败：%v", err)
			}
			if string(again) != want {
				t.Errorf("读取没拷贝：改写返回的切片后缓存变成 %q，应为 %q", again, want)
			}
		})
	}
}

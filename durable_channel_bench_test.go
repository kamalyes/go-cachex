/**
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-03-07 00:01:26
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-03-07 00:55:26
 * @FilePath: \go-cachex\durable_channel_bench_test.go
 * @Description: DurableChannel 性能测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

var (
	benchError error
)

// BenchmarkDurableChannel_Send 测试不同持久化模式的发送性能
func BenchmarkDurableChannel_Send(b *testing.B) {
	client := setupRedisClient(b)
	defer client.Close()

	modes := []PersistMode{PersistSync, PersistAsync, PersistHybrid}

	for _, mode := range modes {
		b.Run(mode.String(), func(b *testing.B) {
			b.ReportAllocs()

			config := DefaultDurableChannelConfig("bench-send")
			config.PersistenceMode = mode
			config.RecoveryOnStart = false

			dc := NewDurableChannel[string](client, config)
			defer dc.Close()

			// 必须有人消费 memChan，否则 PersistAsync 这一档一定挂住
			//
			// PersistAsync 在缓冲满时会阻塞等位，这是有意的背压（Send_ContextTimeout
			// 那个测试就在钉这条语义），而缓冲只有 BufferSize 条
			// 基准既不消费又传 context.Background()，于是第 BufferSize+1 次 Send
			// 永久阻塞 —— 实测进程连 -test.timeout 都没能把它杀掉，
			// 只能等平台级超时，既烧 runner 时长又拿不到可诊断的栈
			//
			// 顺带给 ctx 加截止：真挂住时它会以错误退出而不是悬停
			//
			// [EN] Something has to drain memChan or the PersistAsync case is certain to hang.
			// That mode blocks for space when the buffer is full, which is deliberate
			// back-pressure pinned by the Send_ContextTimeout test, and the buffer holds only
			// BufferSize records. A benchmark that never drains and passes a background context
			// therefore blocks forever on the next send, and the process was observed outliving
			// even -test.timeout, burning runner time without yielding a usable stack. The
			// context also carries a deadline so a genuine stall fails instead of hanging
			// 消费者用独立的 stop 通道退出，不能靠 Close 关闭 memChan 来退
			//
			// defer 是后进先出：这个 defer 注册在 defer dc.Close() 之后，所以它先跑
			// 若消费者只在 memChan 被关闭时退出，就成了
			// Wait 等消费者、消费者等 Close、而 Close 的 defer 还没轮到 —— 自己造一个死锁
			// 实测踩过：进程 0% CPU 睡在那里，和它本来要验的那个挂住一模一样
			//
			// [EN] The drainer exits through its own stop channel rather than by waiting for
			// Close to shut memChan. Defers run last-in-first-out, so this one runs before
			// dc.Close(), and a drainer that only exits on a closed channel would deadlock:
			// Wait waits for the drainer, the drainer waits for Close, and Close has not run
			// yet. This was hit in practice, with the process asleep at zero CPU in exactly
			// the shape it was meant to verify is gone
			stop := make(chan struct{})
			var drained sync.WaitGroup
			drained.Add(1)
			go func() {
				defer drained.Done()
				ch := dc.Receive()
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							return
						}
					case <-stop:
						return
					}
				}
			}()
			defer func() {
				close(stop)
				drained.Wait()
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchError = dc.Send(ctx, "test message")
				if benchError != nil {
					b.Fatalf("第 %d 次 Send 失败：%v", i, benchError)
				}
			}
		})
	}
}

// BenchmarkDurableChannel_SendReceive 测试发送和接收的端到端性能
func BenchmarkDurableChannel_SendReceive(b *testing.B) {
	client := setupRedisClient(b)
	defer client.Close()

	b.ReportAllocs()

	config := DefaultDurableChannelConfig("bench-send-receive")
	config.PersistenceMode = PersistHybrid
	config.RecoveryOnStart = false
	config.BufferSize = 1000

	dc := NewDurableChannel[string](client, config)
	defer dc.Close()

	ctx := context.Background()

	// 启动消费者
	go func() {
		for range dc.Receive() {
			// 消费消息
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchError = dc.Send(ctx, "test message")
	}
}

// BenchmarkDurableChannel_Concurrent 测试并发发送性能
func BenchmarkDurableChannel_Concurrent(b *testing.B) {
	client := setupRedisClient(b)
	defer client.Close()

	modes := []PersistMode{PersistSync, PersistAsync, PersistHybrid}

	for _, mode := range modes {
		b.Run(mode.String(), func(b *testing.B) {
			b.ReportAllocs()

			config := DefaultDurableChannelConfig("bench-concurrent")
			config.PersistenceMode = mode
			config.RecoveryOnStart = false
			config.BufferSize = 1000

			dc := NewDurableChannel[string](client, config)
			defer dc.Close()

			ctx := context.Background()

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					benchError = dc.Send(ctx, "test message")
				}
			})
		})
	}
}

// BenchmarkDurableChannel_WorkerCount 测试不同 Worker 数量的性能
func BenchmarkDurableChannel_WorkerCount(b *testing.B) {
	client := setupRedisClient(b)
	defer client.Close()

	workerCounts := []int{1, 2, 5, 10}

	for _, count := range workerCounts {
		b.Run(fmt.Sprintf("%d_workers", count), func(b *testing.B) {
			b.ReportAllocs()

			config := DefaultDurableChannelConfig("bench-workers")
			config.PersistenceMode = PersistHybrid
			config.WorkerCount = count
			config.RecoveryOnStart = false
			config.BufferSize = 1000

			dc := NewDurableChannel[string](client, config)
			defer dc.Close()

			ctx := context.Background()

			// 启动消费者
			go func() {
				for range dc.Receive() {
					// 消费消息
				}
			}()

			time.Sleep(100 * time.Millisecond) // 等待 worker 启动

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchError = dc.Send(ctx, "test message")
			}
		})
	}
}

// BenchmarkDurableChannel_BufferSize 测试不同缓冲区大小的性能
func BenchmarkDurableChannel_BufferSize(b *testing.B) {
	client := setupRedisClient(b)
	defer client.Close()

	bufferSizes := []int{10, 100, 1000, 10000}

	for _, size := range bufferSizes {
		b.Run(fmt.Sprintf("buffer_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			config := DefaultDurableChannelConfig("bench-buffer")
			config.PersistenceMode = PersistAsync
			config.BufferSize = size
			config.RecoveryOnStart = false

			dc := NewDurableChannel[string](client, config)
			defer dc.Close()

			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchError = dc.Send(ctx, "test message")
			}
		})
	}
}

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

			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchError = dc.Send(ctx, "test message")
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

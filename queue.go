/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-21 15:35:16
 * @FilePath: \engine-im-service\go-cachex\queue.go
 * @Description: Redis队列实现，支持FIFO、优先级队列等多种队列类型
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// QueueType 队列类型
type QueueType string

const (
	QueueTypeFIFO     QueueType = "fifo"     // 先进先出队列
	QueueTypeLIFO     QueueType = "lifo"     // 后进先出队列（栈）
	QueueTypePriority QueueType = "priority" // 优先级队列
	QueueTypeDelayed  QueueType = "delayed"  // 延时队列
)

// QueueItem 队列项
type QueueItem struct {
	ID         string      `json:"id"`          // 唯一标识
	Data       interface{} `json:"data"`        // 数据内容
	Priority   float64     `json:"priority"`    // 优先级（用于优先级队列）
	DelayTime  int64       `json:"delay_time"`  // 延时时间戳（用于延时队列）
	CreatedAt  int64       `json:"created_at"`  // 创建时间
	RetryCount int         `json:"retry_count"` // 重试次数
}

// QueueConfig 队列配置
type QueueConfig struct {
	MaxRetries            int           // 最大重试次数
	RetryDelay            time.Duration // 重试延迟
	BatchSize             int           // 批处理大小
	LockTimeout           time.Duration // 分布式锁超时时间
	CleanupInterval       time.Duration // 清理间隔
	EnableDistributedLock bool          // 是否启用分布式锁
}

// QueueHandler Redis队列处理器
type QueueHandler struct {
	client    *redis.Client
	config    QueueConfig
	namespace string // 命名空间前缀
}

// NewQueueHandler 创建队列处理器
func NewQueueHandler(client *redis.Client, namespace string, config QueueConfig) *QueueHandler {
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Second * 5
	}
	if config.BatchSize == 0 {
		config.BatchSize = 10
	}
	if config.LockTimeout == 0 {
		config.LockTimeout = time.Minute * 5
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = time.Minute * 10
	}

	return &QueueHandler{
		client:    client,
		config:    config,
		namespace: namespace,
	}
}

// getQueueKey 获取队列键名
func (q *QueueHandler) getQueueKey(queueName string, queueType QueueType) string {
	return fmt.Sprintf("%s:queue:%s:%s", q.namespace, string(queueType), queueName)
}

// getLockKey 获取锁键名
func (q *QueueHandler) getLockKey(queueName string) string {
	return fmt.Sprintf("%s:lock:queue:%s", q.namespace, queueName)
}

// getDelayKey 获取延时队列键名
func (q *QueueHandler) getDelayKey(queueName string) string {
	return fmt.Sprintf("%s:delay:%s", q.namespace, queueName)
}

// Enqueue 入队操作
func (q *QueueHandler) Enqueue(ctx context.Context, queueName string, queueType QueueType, item *QueueItem) error {
	if item.ID == "" {
		item.ID = fmt.Sprintf("%d_%d", time.Now().UnixNano(), time.Now().Unix())
	}
	if item.CreatedAt == 0 {
		item.CreatedAt = time.Now().Unix()
	}

	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal queue item: %w", err)
	}

	queueKey := q.getQueueKey(queueName, queueType)

	switch queueType {
	case QueueTypeFIFO:
		// FIFO: RPush + LPop, 右边入队，左边出队，先进先出
		return q.client.RPush(ctx, queueKey, data).Err()
	case QueueTypeLIFO:
		// LIFO: LPush + LPop, 左边入栈，左边出栈，后进先出
		return q.client.LPush(ctx, queueKey, data).Err()
	case QueueTypePriority:
		return q.client.ZAdd(ctx, queueKey, redis.Z{
			Score:  item.Priority,
			Member: data,
		}).Err()
	case QueueTypeDelayed:
		delayKey := q.getDelayKey(queueName)
		executeTime := time.Now().Unix() + item.DelayTime
		return q.client.ZAdd(ctx, delayKey, redis.Z{
			Score:  float64(executeTime),
			Member: data,
		}).Err()
	default:
		return fmt.Errorf("unsupported queue type: %s", queueType)
	}
}

// Dequeue 出队操作
func (q *QueueHandler) Dequeue(ctx context.Context, queueName string, queueType QueueType) (*QueueItem, error) {
	return q.dequeueWithTimeout(ctx, queueName, queueType, time.Second*5)
}

// DequeueNonBlocking 非阻塞出队操作
func (q *QueueHandler) DequeueNonBlocking(ctx context.Context, queueName string, queueType QueueType) (*QueueItem, error) {
	return q.dequeueWithTimeout(ctx, queueName, queueType, 0)
}

// dequeueWithTimeout 带超时的出队操作
func (q *QueueHandler) dequeueWithTimeout(ctx context.Context, queueName string, queueType QueueType, timeout time.Duration) (*QueueItem, error) {
	queueKey := q.getQueueKey(queueName, queueType)

	var result []string
	var err error

	switch queueType {
	case QueueTypeFIFO:
		// FIFO: RPush + LPop, 左边出队
		if timeout > 0 {
			result, err = q.client.BLPop(ctx, timeout, queueKey).Result()
		} else {
			data, popErr := q.client.LPop(ctx, queueKey).Result()
			if popErr != nil {
				if popErr == redis.Nil {
					return nil, nil
				}
				return nil, popErr
			}
			result = []string{"", data}
		}
	case QueueTypeLIFO:
		if timeout > 0 {
			result, err = q.client.BLPop(ctx, timeout, queueKey).Result()
		} else {
			data, popErr := q.client.LPop(ctx, queueKey).Result()
			if popErr != nil {
				if popErr == redis.Nil {
					return nil, nil
				}
				return nil, popErr
			}
			result = []string{"", data}
		}
	case QueueTypePriority:
		// 获取最高优先级的元素
		zResult, zErr := q.client.ZPopMax(ctx, queueKey).Result()
		if zErr != nil {
			if zErr == redis.Nil {
				return nil, nil
			}
			return nil, zErr
		}
		if len(zResult) == 0 {
			return nil, nil
		}
		result = []string{"", zResult[0].Member.(string)}
	case QueueTypeDelayed:
		// 处理延时队列
		return q.processDelayedQueue(ctx, queueName)
	default:
		return nil, fmt.Errorf("unsupported queue type: %s", queueType)
	}

	if err != nil {
		if err == redis.Nil {
			return nil, nil // 队列为空
		}
		return nil, err
	}

	if len(result) < 2 {
		return nil, nil
	}

	var item QueueItem
	if err := json.Unmarshal([]byte(result[1]), &item); err != nil {
		return nil, fmt.Errorf("failed to unmarshal queue item: %w", err)
	}

	return &item, nil
}

// processDelayedQueue 处理延时队列
func (q *QueueHandler) processDelayedQueue(ctx context.Context, queueName string) (*QueueItem, error) {
	delayKey := q.getDelayKey(queueName)
	now := float64(time.Now().Unix())

	// 获取到期的任务
	result, err := q.client.ZRangeByScoreWithScores(ctx, delayKey, &redis.ZRangeBy{
		Min:   "0",
		Max:   fmt.Sprintf("%.0f", now),
		Count: 1,
	}).Result()

	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, nil // 没有到期的任务
	}

	// 删除已处理的任务
	q.client.ZRem(ctx, delayKey, result[0].Member)

	var item QueueItem
	if err := json.Unmarshal([]byte(result[0].Member.(string)), &item); err != nil {
		return nil, fmt.Errorf("failed to unmarshal delayed queue item: %w", err)
	}

	return &item, nil
}

// BatchDequeue 批量出队
func (q *QueueHandler) BatchDequeue(ctx context.Context, queueName string, queueType QueueType, count int) ([]*QueueItem, error) {
	if count <= 0 {
		count = q.config.BatchSize
	}

	items := make([]*QueueItem, 0, count)

	// 使用非阻塞操作提高效率
	for i := 0; i < count; i++ {
		item, err := q.DequeueNonBlocking(ctx, queueName, queueType)
		if err != nil {
			return items, err
		}
		if item == nil {
			break // 队列为空
		}
		items = append(items, item)
	}

	return items, nil
}

// Length 获取队列长度
func (q *QueueHandler) Length(ctx context.Context, queueName string, queueType QueueType) (int64, error) {
	queueKey := q.getQueueKey(queueName, queueType)

	switch queueType {
	case QueueTypeFIFO, QueueTypeLIFO:
		return q.client.LLen(ctx, queueKey).Result()
	case QueueTypePriority:
		return q.client.ZCard(ctx, queueKey).Result()
	case QueueTypeDelayed:
		delayKey := q.getDelayKey(queueName)
		return q.client.ZCard(ctx, delayKey).Result()
	default:
		return 0, fmt.Errorf("unsupported queue type: %s", queueType)
	}
}

// Peek 查看队列头部元素（不移除）
func (q *QueueHandler) Peek(ctx context.Context, queueName string, queueType QueueType, count int) ([]*QueueItem, error) {
	if count <= 0 {
		count = 1
	}

	queueKey := q.getQueueKey(queueName, queueType)
	var result []string
	var err error

	switch queueType {
	case QueueTypeFIFO:
		// FIFO: RPush + LPop, 直接从左边peek，无需反转
		result, err = q.client.LRange(ctx, queueKey, 0, int64(count-1)).Result()
	case QueueTypeLIFO:
		// LIFO: LPush + LPop, 从左边peek
		result, err = q.client.LRange(ctx, queueKey, 0, int64(count-1)).Result()
	case QueueTypePriority:
		zResult, zErr := q.client.ZRevRangeWithScores(ctx, queueKey, 0, int64(count-1)).Result()
		if zErr != nil {
			return nil, zErr
		}
		result = make([]string, len(zResult))
		for i, z := range zResult {
			result[i] = z.Member.(string)
		}
	case QueueTypeDelayed:
		delayKey := q.getDelayKey(queueName)
		zResult, zErr := q.client.ZRangeWithScores(ctx, delayKey, 0, int64(count-1)).Result()
		if zErr != nil {
			return nil, zErr
		}
		result = make([]string, len(zResult))
		for i, z := range zResult {
			result[i] = z.Member.(string)
		}
	default:
		return nil, fmt.Errorf("unsupported queue type: %s", queueType)
	}

	if err != nil {
		return nil, err
	}

	items := make([]*QueueItem, 0, len(result))
	for _, data := range result {
		var item QueueItem
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			continue // 跳过无效数据
		}
		items = append(items, &item)
	}

	return items, nil
}

// Clear 清空队列
func (q *QueueHandler) Clear(ctx context.Context, queueName string, queueType QueueType) error {
	queueKey := q.getQueueKey(queueName, queueType)

	switch queueType {
	case QueueTypeDelayed:
		delayKey := q.getDelayKey(queueName)
		return q.client.Del(ctx, queueKey, delayKey).Err()
	default:
		return q.client.Del(ctx, queueKey).Err()
	}
}

// Contains 检查队列是否包含特定元素
func (q *QueueHandler) Contains(ctx context.Context, queueName string, queueType QueueType, itemID string) (bool, error) {
	// 获取队列长度来确定要检查的元素数量
	length, err := q.Length(ctx, queueName, queueType)
	if err != nil {
		return false, err
	}

	if length == 0 {
		return false, nil
	}

	// 批量检查元素
	count := int(length)
	if count > 1000 { // 限制最大检查数量
		count = 1000
	}

	items, err := q.Peek(ctx, queueName, queueType, count)
	if err != nil {
		return false, err
	}

	for _, item := range items {
		if item.ID == itemID {
			return true, nil
		}
	}

	return false, nil
}

// RetryFailed 重试失败的任务
func (q *QueueHandler) RetryFailed(ctx context.Context, queueName string, queueType QueueType, item *QueueItem) error {
	if item.RetryCount >= q.config.MaxRetries {
		// 达到最大重试次数，移到失败队列
		failedKey := fmt.Sprintf("%s:failed:%s", q.namespace, queueName)
		data, _ := json.Marshal(item)
		return q.client.LPush(ctx, failedKey, data).Err()
	}

	// 增加重试次数并重新入队
	item.RetryCount++
	item.DelayTime = int64(q.config.RetryDelay.Seconds())

	return q.Enqueue(ctx, queueName, QueueTypeDelayed, item)
}

// GetFailedItems 获取失败的任务
func (q *QueueHandler) GetFailedItems(ctx context.Context, queueName string, offset, limit int64) ([]*QueueItem, error) {
	failedKey := fmt.Sprintf("%s:failed:%s", q.namespace, queueName)

	result, err := q.client.LRange(ctx, failedKey, offset, offset+limit-1).Result()
	if err != nil {
		return nil, err
	}

	items := make([]*QueueItem, 0, len(result))
	for _, data := range result {
		var item QueueItem
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			continue
		}
		items = append(items, &item)
	}

	return items, nil
}

// AcquireLock 获取分布式锁
func (q *QueueHandler) AcquireLock(ctx context.Context, queueName string, workerID string) (bool, error) {
	// 如果未启用分布式锁，直接返回true
	if !q.config.EnableDistributedLock {
		return true, nil
	}

	lockKey := q.getLockKey(queueName)

	result, err := q.client.SetNX(ctx, lockKey, workerID, q.config.LockTimeout).Result()
	if err != nil {
		return false, err
	}

	return result, nil
}

// ReleaseLock 释放分布式锁
func (q *QueueHandler) ReleaseLock(ctx context.Context, queueName string, workerID string) error {
	// 如果未启用分布式锁，直接返回
	if !q.config.EnableDistributedLock {
		return nil
	}

	lockKey := q.getLockKey(queueName)

	// 使用Lua脚本确保只能释放自己持有的锁
	script := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`

	return q.client.Eval(ctx, script, []string{lockKey}, workerID).Err()
}

// Statistics 获取队列统计信息
type QueueStats struct {
	QueueName    string `json:"queue_name"`
	QueueType    string `json:"queue_type"`
	Length       int64  `json:"length"`
	DelayedCount int64  `json:"delayed_count,omitempty"`
	FailedCount  int64  `json:"failed_count"`
}

// GetStats 获取队列统计信息
func (q *QueueHandler) GetStats(ctx context.Context, queueName string, queueType QueueType) (*QueueStats, error) {
	stats := &QueueStats{
		QueueName: queueName,
		QueueType: string(queueType),
	}

	// 获取主队列长度
	length, err := q.Length(ctx, queueName, queueType)
	if err != nil {
		return nil, err
	}
	stats.Length = length

	// 获取延时队列长度（如果是延时队列）
	if queueType == QueueTypeDelayed {
		delayKey := q.getDelayKey(queueName)
		delayedCount, err := q.client.ZCard(ctx, delayKey).Result()
		if err == nil {
			stats.DelayedCount = delayedCount
		}
	}

	// 获取失败队列长度
	failedKey := fmt.Sprintf("%s:failed:%s", q.namespace, queueName)
	failedCount, err := q.client.LLen(ctx, failedKey).Result()
	if err == nil {
		stats.FailedCount = failedCount
	}

	return stats, nil
}

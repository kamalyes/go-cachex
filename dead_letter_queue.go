/**
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-03-06 15:15:17
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-03-06 15:55:17
 * @FilePath: \go-cachex\dead_letter_queue.go
 * @Description: 死信队列 - 泛型实现，用于存储和管理失败的数据
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/kamalyes/go-toolbox/pkg/idgen"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/osx"
)

// AlertLevel 预警级别
type AlertLevel int

const (
	AlertLevelInfo     AlertLevel = iota // 信息级别
	AlertLevelWarning                    // 警告级别
	AlertLevelError                      // 错误级别
	AlertLevelCritical                   // 严重级别
)

// String 返回预警级别的字符串表示
func (a AlertLevel) String() string {
	switch a {
	case AlertLevelInfo:
		return "INFO"
	case AlertLevelWarning:
		return "WARNING"
	case AlertLevelError:
		return "ERROR"
	case AlertLevelCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// AlertEvent 预警事件
type AlertEvent struct {
	Level     AlertLevel // 预警级别
	QueueKey  string     // 队列键
	Length    int64      // 当前队列长度
	MaxSize   int64      // 最大队列长度
	Threshold int64      // 触发阈值
	Message   string     // 预警消息
	Timestamp time.Time  // 触发时间
}

// AlertCallback 预警回调函数
// 当队列长度达到阈值时触发
type AlertCallback func(event AlertEvent)

// DeadLetterQueue 死信队列接口（泛型）
// T: 存储的数据类型
type DeadLetterQueue[T any] interface {
	// Push 将数据推入死信队列
	// queueKey: 队列键（如 "message:user_offline", "task:timeout" 等）
	// data: 要存储的数据
	Push(ctx context.Context, queueKey string, data T) error

	// GetItems 获取死信队列数据（不移除）
	// queueKey: 队列键
	// count: 获取的数据数量
	GetItems(ctx context.Context, queueKey string, count int64) ([]T, error)

	// GetLength 获取死信队列长度
	// queueKey: 队列键
	GetLength(ctx context.Context, queueKey string) (int64, error)

	// Remove 从死信队列中移除数据
	// queueKey: 队列键
	// count: 移除的数据数量（从队列头部开始）
	Remove(ctx context.Context, queueKey string, count int64) error

	// Clear 清空指定的死信队列
	// queueKey: 队列键
	Clear(ctx context.Context, queueKey string) error

	// SetAlertCallback 设置预警回调
	// callback: 预警回调函数
	SetAlertCallback(callback AlertCallback)

	// SetAlertThresholds 设置预警阈值
	// warning: 警告阈值（队列长度百分比，如 0.6 表示 60%）
	// error: 错误阈值（队列长度百分比，如 0.8 表示 80%）
	// critical: 严重阈值（队列长度百分比，如 0.95 表示 95%）
	SetAlertThresholds(warning, error, critical float64)
}

// DeadLetterQueueConfig 死信队列配置
type DeadLetterQueueConfig struct {
	MaxSize           int64         // 队列最大长度
	AlertCallback     AlertCallback // 预警回调（可选）
	WarningThreshold  float64       // 警告阈值（默认 0.6，即 60%）
	ErrorThreshold    float64       // 错误阈值（默认 0.8，即 80%）
	CriticalThreshold float64       // 严重阈值（默认 0.95，即 95%）
}

// deadLetterQueueImpl 死信队列实现
type deadLetterQueueImpl[T any] struct {
	queueHandler      *QueueHandler
	maxSize           int64
	ttl               time.Duration
	alertCallback     AlertCallback
	warningThreshold  int64                     // 警告阈值（绝对值）
	errorThreshold    int64                     // 错误阈值（绝对值）
	criticalThreshold int64                     // 严重阈值（绝对值）
	idGenerator       *idgen.SnowflakeGenerator // 雪花算法 ID 生成器
}

// NewDeadLetterQueue 创建死信队列
// 示例：
//
//	queueHandler := cachex.NewQueueHandler(client, "dlq:", queueConfig)
//	config := cachex.DeadLetterQueueConfig{
//	    MaxSize: 1000,
//	    WarningThreshold: 0.6,
//	    ErrorThreshold: 0.8,
//	    CriticalThreshold: 0.95,
//	}
//	dlq := cachex.NewDeadLetterQueue[*MyData](queueHandler, config)
func NewDeadLetterQueue[T any](queueHandler *QueueHandler, config DeadLetterQueueConfig) DeadLetterQueue[T] {
	maxSize := mathx.IfLeZero(config.MaxSize, 1000)

	// 设置默认阈值
	warningPct := mathx.IfLeZero(config.WarningThreshold, 0.6)
	errorPct := mathx.IfLeZero(config.ErrorThreshold, 0.8)
	criticalPct := mathx.IfLeZero(config.CriticalThreshold, 0.95)

	// 使用 go-toolbox 的 WorkerID 和 DatacenterID 创建雪花算法 ID 生成器
	workerID := osx.GetWorkerIdForSnowflake()
	datacenterID := osx.GetDatacenterId()
	idGenerator := idgen.NewSnowflakeGenerator(workerID, datacenterID)

	return &deadLetterQueueImpl[T]{
		queueHandler:      queueHandler,
		maxSize:           maxSize,
		ttl:               7 * 24 * time.Hour,
		alertCallback:     config.AlertCallback,
		warningThreshold:  int64(float64(maxSize) * warningPct),
		errorThreshold:    int64(float64(maxSize) * errorPct),
		criticalThreshold: int64(float64(maxSize) * criticalPct),
		idGenerator:       idGenerator,
	}
}

// Push 将数据推入死信队列
func (d *deadLetterQueueImpl[T]) Push(ctx context.Context, queueKey string, data T) error {
	// 使用反射检查数据是否为 nil（支持指针、接口、切片、map、channel、函数）
	v := reflect.ValueOf(data)
	if !v.IsValid() || (v.Kind() == reflect.Ptr && v.IsNil()) {
		return fmt.Errorf("data is nil")
	}

	// 使用雪花算法生成唯一 ID
	itemID := d.idGenerator.GenerateRequestID()

	// 将数据包装为 QueueItem
	item := &QueueItem{
		ID:        itemID,
		Data:      data,
		CreatedAt: time.Now().Unix(),
	}

	// 使用 FIFO 队列（先进先出）
	if err := d.queueHandler.Enqueue(ctx, queueKey, QueueTypeFIFO, item); err != nil {
		return fmt.Errorf("push to dead letter queue failed: %w", err)
	}

	// 检查队列长度，如果超过 maxSize 则移除最旧的数据
	length, err := d.queueHandler.Length(ctx, queueKey, QueueTypeFIFO)
	if err != nil {
		return fmt.Errorf("get queue length failed: %w", err)
	}

	if length > d.maxSize {
		// 移除超出的数据（从队列头部移除最旧的）
		removeCount := length - d.maxSize
		for i := int64(0); i < removeCount; i++ {
			_, _ = d.queueHandler.Dequeue(ctx, queueKey, QueueTypeFIFO)
		}
	}

	// 检查是否需要触发预警
	d.checkAndAlert(queueKey, length)

	return nil
}

// checkAndAlert 检查队列长度并触发预警
func (d *deadLetterQueueImpl[T]) checkAndAlert(queueKey string, length int64) {
	if d.alertCallback == nil {
		return
	}

	var level AlertLevel
	var threshold int64
	var message string

	switch {
	case length >= d.criticalThreshold:
		level = AlertLevelCritical
		threshold = d.criticalThreshold
		message = fmt.Sprintf("死信队列 '%s' 达到严重阈值！当前长度: %d, 最大容量: %d (%.1f%%)",
			queueKey, length, d.maxSize, float64(length)/float64(d.maxSize)*100)
	case length >= d.errorThreshold:
		level = AlertLevelError
		threshold = d.errorThreshold
		message = fmt.Sprintf("死信队列 '%s' 达到错误阈值！当前长度: %d, 最大容量: %d (%.1f%%)",
			queueKey, length, d.maxSize, float64(length)/float64(d.maxSize)*100)
	case length >= d.warningThreshold:
		level = AlertLevelWarning
		threshold = d.warningThreshold
		message = fmt.Sprintf("死信队列 '%s' 达到警告阈值！当前长度: %d, 最大容量: %d (%.1f%%)",
			queueKey, length, d.maxSize, float64(length)/float64(d.maxSize)*100)
	default:
		return // 未达到任何阈值，不触发预警
	}

	event := AlertEvent{
		Level:     level,
		QueueKey:  queueKey,
		Length:    length,
		MaxSize:   d.maxSize,
		Threshold: threshold,
		Message:   message,
		Timestamp: time.Now(),
	}

	// 异步触发回调，避免阻塞主流程
	go d.alertCallback(event)
}

// GetItems 获取死信队列数据（不移除）
func (d *deadLetterQueueImpl[T]) GetItems(ctx context.Context, queueKey string, count int64) ([]T, error) {
	count = mathx.IfLeZero(count, 10)

	// 使用 Peek 查看队列头部元素（不移除）
	items, err := d.queueHandler.Peek(ctx, queueKey, QueueTypeFIFO, int(count))
	if err != nil {
		return nil, fmt.Errorf("get dead letter items failed: %w", err)
	}

	results := make([]T, 0, len(items))
	for _, item := range items {
		// 尝试直接类型断言
		if data, ok := item.Data.(T); ok {
			results = append(results, data)
			continue
		}

		// 如果直接断言失败，尝试通过 JSON 重新序列化和反序列化
		// 这是因为从 Redis 读取后，interface{} 类型会丢失原始类型信息
		jsonData, err := json.Marshal(item.Data)
		if err != nil {
			continue // 跳过无法序列化的数据
		}

		var data T
		if err := json.Unmarshal(jsonData, &data); err != nil {
			continue // 跳过无法反序列化的数据
		}

		results = append(results, data)
	}

	return results, nil
}

// GetLength 获取死信队列长度
func (d *deadLetterQueueImpl[T]) GetLength(ctx context.Context, queueKey string) (int64, error) {
	length, err := d.queueHandler.Length(ctx, queueKey, QueueTypeFIFO)
	if err != nil {
		return 0, fmt.Errorf("get dead letter queue length failed: %w", err)
	}
	return length, nil
}

// Remove 从死信队列中移除数据（从队列头部开始）
func (d *deadLetterQueueImpl[T]) Remove(ctx context.Context, queueKey string, count int64) error {
	if count <= 0 {
		return nil
	}

	// 批量出队（移除）
	_, err := d.queueHandler.BatchDequeue(ctx, queueKey, QueueTypeFIFO, int(count))
	if err != nil {
		return fmt.Errorf("remove dead letter items failed: %w", err)
	}

	return nil
}

// Clear 清空指定的死信队列
func (d *deadLetterQueueImpl[T]) Clear(ctx context.Context, queueKey string) error {
	err := d.queueHandler.Clear(ctx, queueKey, QueueTypeFIFO)
	if err != nil {
		return fmt.Errorf("clear dead letter queue failed: %w", err)
	}
	return nil
}

// SetAlertCallback 设置预警回调
func (d *deadLetterQueueImpl[T]) SetAlertCallback(callback AlertCallback) {
	d.alertCallback = callback
}

// SetAlertThresholds 设置预警阈值
func (d *deadLetterQueueImpl[T]) SetAlertThresholds(warning, error, critical float64) {
	if warning > 0 && warning < 1 {
		d.warningThreshold = int64(float64(d.maxSize) * warning)
	}
	if error > 0 && error < 1 {
		d.errorThreshold = int64(float64(d.maxSize) * error)
	}
	if critical > 0 && critical < 1 {
		d.criticalThreshold = int64(float64(d.maxSize) * critical)
	}
}

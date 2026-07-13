package stats

// 统计持久化 (方案 A): 定期把内存计数落盘为 JSON, 启动时读回。
//
// 设计原则: 热路径 (RecordCheck) 只碰内存原子操作; 所有磁盘 IO 都在后台 goroutine
// 按固定间隔进行, 或在进程退出时刷一次。因此持久化不影响检测吞吐。
//
// 落盘用 "临时文件 + 原子 rename", 避免写到一半被读到半个文件。
// 崩溃时最多丢失最后一个 flush 间隔内的增量 (方案 A 的固有取舍)。

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// Persister 负责一个 Collector 的定期落盘与启动恢复。
type Persister struct {
	c        *Collector
	path     string
	interval time.Duration
}

// NewPersister 创建持久化器。interval <= 0 时会被调用方规避 (见 Run)。
func NewPersister(c *Collector, path string, interval time.Duration) *Persister {
	return &Persister{c: c, path: path, interval: interval}
}

// LoadInto 在启动时从磁盘读取统计并恢复到 Collector。
// 文件不存在视为首次启动 (无历史), 返回 nil。其他读取/解析错误如实返回。
func (p *Persister) LoadInto() error {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 首次启动, 没有历史文件
		}
		return fmt.Errorf("读取统计文件失败: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("解析统计文件失败: %w", err)
	}
	p.c.Restore(s)
	return nil
}

// Flush 立即将当前统计写盘 (临时文件 + 原子 rename)。并发安全。
func (p *Persister) Flush() error {
	data, err := json.MarshalIndent(p.c.Dump(), "", "  ")
	if err != nil {
		return fmt.Errorf("序列化统计失败: %w", err)
	}
	data = append(data, '\n')

	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时统计文件失败: %w", err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		return fmt.Errorf("替换统计文件失败: %w", err)
	}
	return nil
}

// Run 启动后台定期落盘循环, 阻塞直到 done 关闭; 退出前会做最后一次 Flush。
// 通常放在独立 goroutine 中: go p.Run(done)。
func (p *Persister) Run(done <-chan struct{}) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := p.Flush(); err != nil {
				log.Printf("[stats] 定期落盘失败: %v", err)
			}
		case <-done:
			// 退出前最后刷一次, 尽量减少丢失。
			if err := p.Flush(); err != nil {
				log.Printf("[stats] 退出前落盘失败: %v", err)
			} else {
				log.Printf("[stats] 退出前已落盘: %s", p.path)
			}
			return
		}
	}
}

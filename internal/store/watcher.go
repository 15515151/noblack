package store

import (
	"log"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch 使用 fsnotify 监听词库文件变化, 变化时自动触发 Store.Reload。
//
// 实现细节:
//   - 许多编辑器保存文件时会先删除再重建 (rename/create), 单纯监听 Write 事件可能漏掉,
//     因此我们监听文件所在“目录”, 并对目标文件的任意变更做出反应。
//   - 使用 debounce (去抖) 合并短时间内的多次事件, 避免一次保存触发多次重建。
//
// 该函数会阻塞运行, 通常放在独立 goroutine 中: go st.Watch()。
// 传入的 done 通道关闭时, 监听退出。
func (s *Store) Watch(done <-chan struct{}) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// 监听目录而非文件本身, 以兼容“先删后建”式保存。
	dir := filepath.Dir(s.path)
	if err := watcher.Add(dir); err != nil {
		return err
	}

	target := filepath.Clean(s.path)
	log.Printf("[watch] 开始监听词库目录: %s (目标文件: %s)", dir, target)

	var (
		debounce   *time.Timer
		debounceCh = make(chan struct{}, 1)
	)
	// debounce 触发后执行真正的 reload。
	go func() {
		for range debounceCh {
			if n, err := s.Reload(); err != nil {
				log.Printf("[watch] 热加载失败, 保留旧词库: %v", err)
			} else {
				log.Printf("[watch] 热加载成功, 当前词条数: %d", n)
			}
		}
	}()

	trigger := func() {
		// 300ms 去抖: 期间的多次事件只触发一次 reload。
		if debounce != nil {
			debounce.Stop()
		}
		debounce = time.AfterFunc(300*time.Millisecond, func() {
			select {
			case debounceCh <- struct{}{}:
			default: // 已有待处理的 reload, 丢弃本次
			}
		})
	}

	for {
		select {
		case <-done:
			log.Printf("[watch] 收到停止信号, 退出监听")
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// 只关心目标文件的变更。
			if filepath.Clean(event.Name) != target {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				log.Printf("[watch] 检测到文件变更: %s (%s)", event.Name, event.Op)
				trigger()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("[watch] 监听错误: %v", err)
		}
	}
}

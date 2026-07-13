package stats

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersist_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	// 第一段生命周期: 记录一些数据并落盘。
	c1 := New()
	c1.RecordCheck([]string{"挖矿", "废物"})
	c1.RecordCheck([]string{"挖矿"})
	c1.RecordReload()
	p1 := NewPersister(c1, path, time.Hour)
	if err := p1.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 第二段生命周期 (模拟重启): 新 Collector 从磁盘恢复。
	c2 := New()
	p2 := NewPersister(c2, path, time.Hour)
	if err := p2.LoadInto(); err != nil {
		t.Fatalf("LoadInto: %v", err)
	}

	s := c2.Snapshot(10)
	if s.CheckRequests != 2 || s.TotalMatches != 3 || s.ReloadCount != 1 {
		t.Errorf("恢复的汇总计数不符: %+v", s)
	}
	if s.DistinctWords != 2 {
		t.Errorf("恢复的 distinct 不符: %d", s.DistinctWords)
	}
	if len(s.TopWords) == 0 || s.TopWords[0].Word != "挖矿" || s.TopWords[0].Count != 2 {
		t.Errorf("恢复的词计数不符: %+v", s.TopWords)
	}

	// 恢复后继续累加应在原基础上叠加。
	c2.RecordCheck([]string{"挖矿"})
	if s2 := c2.Snapshot(1); s2.TopWords[0].Count != 3 {
		t.Errorf("恢复后累加错误: %+v", s2.TopWords)
	}
}

func TestPersist_MissingFileIsNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	c := New()
	p := NewPersister(c, path, time.Hour)
	if err := p.LoadInto(); err != nil {
		t.Errorf("文件不存在应视为首次启动 (nil), 实际: %v", err)
	}
}

func TestPersist_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New()
	p := NewPersister(c, path, time.Hour)
	if err := p.LoadInto(); err == nil {
		t.Error("损坏文件应返回解析错误")
	}
}

func TestPersistConcurrentFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	c := New()
	c.RecordCheck([]string{"a"})
	p := NewPersister(c, path, time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Flush(); err != nil {
				t.Errorf("并发落盘失败: %v", err)
			}
		}()
	}
	wg.Wait()

	restored := New()
	if err := NewPersister(restored, path, time.Hour).LoadInto(); err != nil {
		t.Fatalf("并发落盘后加载失败: %v", err)
	}
	if got := restored.Snapshot(0).CheckRequests; got != 1 {
		t.Fatalf("恢复后的检测请求数为 %d，期望 1", got)
	}
}

func TestPersisterRunWaitsForFinalFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	c := New()
	c.RecordCheck([]string{"a"})
	p := NewPersister(c, path, time.Hour)
	done := make(chan struct{})
	go p.Run(done)
	close(done)
	p.Wait()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("退出前最终落盘未完成: %v", err)
	}
}

func TestPersisterInvalidIntervalExits(t *testing.T) {
	p := NewPersister(New(), filepath.Join(t.TempDir(), "stats.json"), 0)
	done := make(chan struct{})
	go p.Run(done)
	p.Wait()
}

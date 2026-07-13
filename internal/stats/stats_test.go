package stats

import (
	"sync"
	"testing"
)

func TestRecordAndSnapshot(t *testing.T) {
	c := New()
	c.RecordCheck([]string{"挖矿", "废物"})
	c.RecordCheck([]string{"挖矿"})
	c.RecordCheck(nil) // 未命中

	s := c.Snapshot(10)
	if s.CheckRequests != 3 {
		t.Errorf("check_requests=%d, 期望 3", s.CheckRequests)
	}
	if s.HitRequests != 2 {
		t.Errorf("hit_requests=%d, 期望 2", s.HitRequests)
	}
	if s.TotalMatches != 3 {
		t.Errorf("total_matches=%d, 期望 3", s.TotalMatches)
	}
	if s.DistinctWords != 2 {
		t.Errorf("distinct_words=%d, 期望 2", s.DistinctWords)
	}
	// 挖矿 命中 2 次, 应排第一。
	if len(s.TopWords) == 0 || s.TopWords[0].Word != "挖矿" || s.TopWords[0].Count != 2 {
		t.Errorf("top_words 排序错误: %+v", s.TopWords)
	}
}

func TestTopN(t *testing.T) {
	c := New()
	for i, w := range []string{"a", "b", "c", "d"} {
		for j := 0; j <= i; j++ {
			c.RecordCheck([]string{w})
		}
	}
	s := c.Snapshot(2)
	if len(s.TopWords) != 2 {
		t.Fatalf("TopN 未限制数量: %d", len(s.TopWords))
	}
	if s.TopWords[0].Word != "d" || s.TopWords[1].Word != "c" {
		t.Errorf("TopN 排序错误: %+v", s.TopWords)
	}
	if s.DistinctWords != 4 {
		t.Errorf("distinct 应为全量 4, 实际 %d", s.DistinctWords)
	}
}

func TestReset(t *testing.T) {
	c := New()
	c.RecordCheck([]string{"x"})
	c.Reset()
	s := c.Snapshot(10)
	if s.CheckRequests != 0 || s.DistinctWords != 0 || len(s.TopWords) != 0 {
		t.Errorf("Reset 未清零: %+v", s)
	}
}

// 并发安全: 多 goroutine 同时记录不应 panic 或丢计数。
func TestConcurrent(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.RecordCheck([]string{"hot"})
			}
		}()
	}
	wg.Wait()
	s := c.Snapshot(1)
	if s.CheckRequests != 50000 || s.TopWords[0].Count != 50000 {
		t.Errorf("并发计数错误: req=%d word=%+v", s.CheckRequests, s.TopWords)
	}
}

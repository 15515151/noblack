package stats

// Collector 收集运行时统计, 全程无锁 (atomic + sync.Map)。
//
// 性能说明:
//   - 接口计数用 atomic.Int64, 单次自增 ~1-5 ns。
//   - 按词计数用 sync.Map[string]*atomic.Int64: 首次遇到某词时 LoadOrStore 建计数器,
//     之后都是纯原子自增, 无锁无竞争。
//   相比一次匹配 (~2500 ns), 统计开销占比 < 0.1%, 对吞吐无实质影响。
//
// 排序 (取 TopN) 只在读取统计快照时发生, 不在检测热路径上。

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Collector 是并发安全的统计收集器。
type Collector struct {
	checkRequests atomic.Int64 // /check 被调用次数
	totalMatches  atomic.Int64 // 累计命中的敏感词次数 (含重叠)
	hitRequests   atomic.Int64 // 至少命中一个词的 /check 次数
	reloadCount   atomic.Int64 // 词库热加载成功次数

	// wordHits: 词 -> *atomic.Int64。每个曾命中过的词一个计数器。
	wordHits sync.Map
}

// New 创建一个空的收集器。
func New() *Collector {
	return &Collector{}
}

// RecordCheck 记录一次 /check 调用及其命中情况。
// words 为本次命中的全部词 (可含重复, 代表命中次数)。
func (c *Collector) RecordCheck(words []string) {
	c.checkRequests.Add(1)
	if len(words) > 0 {
		c.hitRequests.Add(1)
		c.totalMatches.Add(int64(len(words)))
		for _, w := range words {
			c.incWord(w)
		}
	}
}

// incWord 对单个词的计数器 +1 (无锁)。
func (c *Collector) incWord(word string) {
	if v, ok := c.wordHits.Load(word); ok {
		v.(*atomic.Int64).Add(1)
		return
	}
	// 首次出现: 竞争地初始化计数器。LoadOrStore 保证只有一个胜出。
	cnt := new(atomic.Int64)
	actual, _ := c.wordHits.LoadOrStore(word, cnt)
	actual.(*atomic.Int64).Add(1)
}

// RecordReload 记录一次成功热加载。
func (c *Collector) RecordReload() { c.reloadCount.Add(1) }

// WordCount 是单个词的命中统计, 用于输出。
type WordCount struct {
	Word  string `json:"word"`
	Count int64  `json:"count"`
}

// Snapshot 是某一刻的统计快照。
type Snapshot struct {
	CheckRequests int64       `json:"check_requests"`
	HitRequests   int64       `json:"hit_requests"`
	TotalMatches  int64       `json:"total_matches"`
	ReloadCount   int64       `json:"reload_count"`
	DistinctWords int         `json:"distinct_words"` // 曾命中过的不同词数量
	TopWords      []WordCount `json:"top_words"`      // 命中最多的词 (降序)
}

// Snapshot 生成当前统计快照。topN <= 0 时返回全部词。
func (c *Collector) Snapshot(topN int) Snapshot {
	var words []WordCount
	c.wordHits.Range(func(k, v any) bool {
		words = append(words, WordCount{Word: k.(string), Count: v.(*atomic.Int64).Load()})
		return true
	})

	// 按命中次数降序; 次数相同按词字典序, 保证结果稳定。
	sort.Slice(words, func(i, j int) bool {
		if words[i].Count != words[j].Count {
			return words[i].Count > words[j].Count
		}
		return words[i].Word < words[j].Word
	})

	distinct := len(words)
	if topN > 0 && len(words) > topN {
		words = words[:topN]
	}

	return Snapshot{
		CheckRequests: c.checkRequests.Load(),
		HitRequests:   c.hitRequests.Load(),
		TotalMatches:  c.totalMatches.Load(),
		ReloadCount:   c.reloadCount.Load(),
		DistinctWords: distinct,
		TopWords:      words,
	}
}

// Reset 清空所有统计 (供 /stats/reset 使用)。
func (c *Collector) Reset() {
	c.checkRequests.Store(0)
	c.hitRequests.Store(0)
	c.totalMatches.Store(0)
	c.reloadCount.Store(0)
	c.wordHits.Range(func(k, _ any) bool {
		c.wordHits.Delete(k)
		return true
	})
}

// ---------- 持久化: 全量 dump / restore ----------

// State 是可持久化的完整统计状态 (含全部词计数, 不截断)。
// 与 Snapshot 的区别: Snapshot 面向展示 (TopN 截断、含 distinct_words 派生字段);
// State 面向存盘, 保留每个词的精确计数以便原样恢复。
type State struct {
	CheckRequests int64            `json:"check_requests"`
	HitRequests   int64            `json:"hit_requests"`
	TotalMatches  int64            `json:"total_matches"`
	ReloadCount   int64            `json:"reload_count"`
	WordHits      map[string]int64 `json:"word_hits"`
}

// Dump 导出当前完整状态 (全量, 用于落盘)。并发安全。
func (c *Collector) Dump() State {
	wh := make(map[string]int64)
	c.wordHits.Range(func(k, v any) bool {
		wh[k.(string)] = v.(*atomic.Int64).Load()
		return true
	})
	return State{
		CheckRequests: c.checkRequests.Load(),
		HitRequests:   c.hitRequests.Load(),
		TotalMatches:  c.totalMatches.Load(),
		ReloadCount:   c.reloadCount.Load(),
		WordHits:      wh,
	}
}

// Restore 用给定状态覆盖当前计数 (用于启动时从磁盘恢复)。
// 应在服务开始处理请求之前调用, 此时无并发。
func (c *Collector) Restore(s State) {
	c.checkRequests.Store(s.CheckRequests)
	c.hitRequests.Store(s.HitRequests)
	c.totalMatches.Store(s.TotalMatches)
	c.reloadCount.Store(s.ReloadCount)
	// 清掉旧词计数再灌入。
	c.wordHits.Range(func(k, _ any) bool { c.wordHits.Delete(k); return true })
	for w, n := range s.WordHits {
		cnt := new(atomic.Int64)
		cnt.Store(n)
		c.wordHits.Store(w, cnt)
	}
}

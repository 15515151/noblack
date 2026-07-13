package store

// Store 负责持有“当前生效”的自动机 + 词条列表, 提供无锁读取、原子热替换、
// 以及线程安全的词条 CRUD。
//
// 并发模型:
//   - 读路径 (Current): atomic.Value.Load() 获取当前自动机指针, 无锁、无阻塞。
//   - 写路径 (Reload / CRUD): 在 mu 保护下修改词条内存副本 -> 构建全新自动机 ->
//     atomic.Store 原子替换 -> 落盘 JSON。写操作彼此串行, 但绝不阻塞读路径。
//
// 关键点: 热加载/编辑期间, 旧自动机始终对读请求可用; 新树构建完毕的瞬间才被“发布”。

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"noblack/internal/matcher"
)

// Store 保存自动机原子引用 + 词条内存副本。
type Store struct {
	v atomic.Value // *matcher.Automaton, 读路径无锁访问

	// mu 保护 entries 及写路径 (Reload/CRUD) 的串行化; 不参与读路径。
	mu      sync.Mutex
	entries []matcher.Entry

	path string
	opts matcher.Options
}

// New 用初始词条创建 Store。
func New(path string, entries []matcher.Entry, opts matcher.Options) *Store {
	s := &Store{path: path, opts: opts, entries: entries}
	s.v.Store(matcher.BuildFromEntries(entries, opts))
	return s
}

// Current 返回当前生效的自动机。热路径, 无锁。
func (s *Store) Current() *matcher.Automaton {
	a, _ := s.v.Load().(*matcher.Automaton)
	return a
}

// Path 返回词库文件路径。
func (s *Store) Path() string { return s.path }

// ---------- 写路径 ----------

// rebuildAndPublishLocked 用当前 s.entries 构建新自动机并原子发布。调用者须持有 mu。
func (s *Store) rebuildAndPublishLocked() {
	s.v.Store(matcher.BuildFromEntries(s.entries, s.opts))
}

// Reload 从磁盘重新读取词库, 覆盖内存副本并重建自动机。
// 用于 fsnotify 监听到外部文件改动、或手动 POST /reload。
func (s *Store) Reload() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := matcher.LoadEntries(s.path, s.opts)
	if err != nil {
		return 0, err // 失败: 保留旧词条与旧树
	}
	s.entries = entries
	s.rebuildAndPublishLocked()
	return len(entries), nil
}

// ListEntries 返回当前词条的一个副本 (按词排序), 供前端展示。
func (s *Store) ListEntries() []matcher.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]matcher.Entry, len(s.entries))
	copy(out, s.entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Word < out[j].Word })
	return out
}

// findLocked 返回 word 在 s.entries 中的下标, 不存在返回 -1。调用者须持有 mu。
func (s *Store) findLocked(word string) int {
	for i := range s.entries {
		if s.entries[i].Word == word {
			return i
		}
	}
	return -1
}

// AddEntry 新增一个词条。若词已存在返回错误 (改用 UpdateEntry)。
// 成功后重建自动机并落盘。
func (s *Store) AddEntry(e matcher.Entry) error {
	e = matcher.NormalizeEntry(e) // 清洗 word 空段/空白 + levels/remarks 空项
	if e.Word == "" {
		return fmt.Errorf("word 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.findLocked(e.Word) >= 0 {
		return fmt.Errorf("词条 %q 已存在", e.Word)
	}
	s.entries = append(s.entries, e)
	return s.commitLocked()
}

// UpdateEntry 更新一个已存在词条的等级与备注。词不存在返回错误。
func (s *Store) UpdateEntry(e matcher.Entry) error {
	e = matcher.NormalizeEntry(e)
	if e.Word == "" {
		return fmt.Errorf("word 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.findLocked(e.Word)
	if idx < 0 {
		return fmt.Errorf("词条 %q 不存在", e.Word)
	}
	s.entries[idx] = e
	return s.commitLocked()
}

// UpsertEntry 存在则更新, 不存在则新增。返回 created=true 表示是新增。
func (s *Store) UpsertEntry(e matcher.Entry) (created bool, err error) {
	e = matcher.NormalizeEntry(e)
	if e.Word == "" {
		return false, fmt.Errorf("word 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.findLocked(e.Word)
	if idx < 0 {
		s.entries = append(s.entries, e)
		created = true
	} else {
		s.entries[idx] = e
	}
	return created, s.commitLocked()
}

// DeleteEntry 删除一个词条。词不存在返回错误。
func (s *Store) DeleteEntry(word string) error {
	word = matcher.NormalizeWord(word)
	if word == "" {
		return fmt.Errorf("word 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.findLocked(word)
	if idx < 0 {
		return fmt.Errorf("词条 %q 不存在", word)
	}
	s.entries = append(s.entries[:idx], s.entries[idx+1:]...)
	return s.commitLocked()
}

// commitLocked 重建并发布自动机, 然后落盘。调用者须持有 mu。
// 落盘失败时回滚内存副本, 保证内存与文件一致。
func (s *Store) commitLocked() error {
	// 先落盘: 若失败则不改变已发布的自动机 (但内存 entries 已改, 需回滚)。
	if err := matcher.SaveEntries(s.path, s.entries); err != nil {
		// 从磁盘恢复内存副本, 避免内存与文件不一致。
		if entries, e2 := matcher.LoadEntries(s.path, s.opts); e2 == nil {
			s.entries = entries
		}
		return err
	}
	s.rebuildAndPublishLocked()
	return nil
}

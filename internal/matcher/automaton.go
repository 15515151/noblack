package matcher

// Aho-Corasick 自动机实现。
//
// 设计要点:
//  1. 以 rune (Unicode 码点) 为单位构建, 而非 byte, 从而正确处理中文/英文/emoji 等字符,
//     英文单词 (如 bilibili) 与中文一视同仁, 位置下标以 rune 计。
//  2. 子节点用 map[rune]*node 存储, 对大字符集 (中文) 更省内存。
//  3. 构建完成后自动机是只读的 (immutable), 匹配阶段不修改任何字段, 天然并发安全,
//     配合上层 atomic.Value 热替换, 实现无锁读取。
//  4. 等级 (Level) 不再是硬编码枚举, 而是任意字符串 —— 词库里写什么等级就是什么等级,
//     构建时自动收集全部出现过的等级 (见 Automaton.Levels)。
//  5. 备注 (Remarks) 支持多个, 以字符串切片保存。

import (
	"sort"
	"strings"
	"unicode"
)

// Level 现在只是一个普通字符串别名, 不限定取值。
// 词库可自由使用 High / Medium / Low / 挖矿 / 色情 / 任意自定义等级。
type Level = string

// WordMeta 保存一个词条的附加信息, 命中时随结果返回。
type WordMeta struct {
	Word    string   // 原始词条 (保留原始大小写), 如 "挖矿" / "Bilibili"
	Levels  []Level  // 敏感度等级, 支持多个 (如 ["bilibili","引流"]); 至少一个
	Remarks []string // 备注列表, 如 ["大奶子", "大奶"]; 无备注则为空切片
}

// node 是自动机中的一个状态节点。
type node struct {
	children map[rune]*node // 转移边: 字符 -> 子节点
	fail     *node          // 失配指针 (fail link)
	depth    int            // 从根到该节点的深度 (rune 数), 命中时反推起始位置
	meta     *WordMeta      // 非 nil 表示以该节点结尾构成一个完整敏感词
}

func newNode() *node {
	return &node{children: make(map[rune]*node)}
}

// cleanStrings 去空白、去空项, 返回非 nil 切片 (便于 JSON 序列化为 [] 而非 null)。
func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Automaton 是对外暴露的、可用于匹配的只读自动机。
type Automaton struct {
	root            *node
	size            int      // 词条总数
	levels          []string // 词库中出现过的全部等级 (已排序去重)
	caseInsensitive bool     // 是否大小写不敏感 (主要惠及英文)
}

// Match 描述一次命中结果。
type Match struct {
	Word    string
	Levels  []Level
	Remarks []string
	Start   int // 命中词在文本中的起始 rune 下标 (含)
	End     int // 结束 rune 下标 (不含), 即 [Start, End)
}

// Size 返回词条数量。
func (a *Automaton) Size() int {
	if a == nil {
		return 0
	}
	return a.size
}

// Levels 返回词库中出现过的全部等级 (已排序去重)。
// 词库新增/删除等级后, 热加载重建即可通过此方法感知到最新等级集合。
func (a *Automaton) Levels() []string {
	if a == nil {
		return nil
	}
	return a.levels
}

// fold 按大小写敏感设置折叠单个 rune。
// unicode.ToLower 是 rune->rune 的 1:1 映射, 不改变 rune 数量, 因此位置下标保持精确。
func fold(r rune, ci bool) rune {
	if ci {
		return unicode.ToLower(r)
	}
	return r
}

// Builder 用于收集词条并最终构建 Automaton。
type Builder struct {
	root            *node
	count           int
	levelSet        map[string]struct{}
	caseInsensitive bool
}

// NewBuilder 创建构建器。caseInsensitive=true 时匹配大小写不敏感。
func NewBuilder(caseInsensitive bool) *Builder {
	return &Builder{
		root:            newNode(),
		levelSet:        make(map[string]struct{}),
		caseInsensitive: caseInsensitive,
	}
}

// Add 插入一个词条及其元信息。word 为空则忽略; 重复词条以后插入者为准。
// levels 支持一个词条挂多个等级; 传空时该词条无等级。
func (b *Builder) Add(word string, levels []Level, remarks []string) {
	if word == "" {
		return
	}
	cur := b.root
	depth := 0
	for _, r := range word { // range string 天然按 rune 迭代
		depth++
		rr := fold(r, b.caseInsensitive)
		next, ok := cur.children[rr]
		if !ok {
			next = newNode()
			next.depth = depth
			cur.children[rr] = next
		}
		cur = next
	}
	if cur.meta == nil {
		b.count++
	}
	// 归一化: 去空白去空项, 保证非 nil 切片。
	levels = cleanStrings(levels)
	remarks = cleanStrings(remarks)
	cur.meta = &WordMeta{Word: word, Levels: levels, Remarks: remarks}
	for _, lv := range levels {
		b.levelSet[lv] = struct{}{}
	}
}

// Build 通过 BFS 构建失配指针, 生成只读 Automaton。仅应在构建期调用一次。
func (b *Builder) Build() *Automaton {
	root := b.root
	root.fail = root

	queue := make([]*node, 0, len(root.children))
	for _, child := range root.children {
		child.fail = root
		queue = append(queue, child)
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for r, child := range cur.children {
			f := cur.fail
			for f != root && f.children[r] == nil {
				f = f.fail
			}
			if fc, ok := f.children[r]; ok && fc != child {
				child.fail = fc
			} else {
				child.fail = root
			}
			queue = append(queue, child)
		}
	}

	// 收集并排序等级集合。
	effectiveLevels := make(map[string]struct{})
	var collectLevels func(*node)
	collectLevels = func(n *node) {
		if n.meta != nil {
			for _, lv := range n.meta.Levels {
				effectiveLevels[lv] = struct{}{}
			}
		}
		for _, child := range n.children {
			collectLevels(child)
		}
	}
	collectLevels(root)
	levels := make([]string, 0, len(effectiveLevels))
	for lv := range effectiveLevels {
		levels = append(levels, lv)
	}
	sort.Strings(levels)

	return &Automaton{
		root:            root,
		size:            b.count,
		levels:          levels,
		caseInsensitive: b.caseInsensitive,
	}
}

// FindAll 查找 text 中所有命中 (含重叠)。时间复杂度 O(n + z), 与词库规模无关。
// 不修改自动机状态, 并发安全。
func (a *Automaton) FindAll(text string) []Match {
	if a == nil || a.root == nil {
		return nil
	}

	runes := []rune(text)
	var matches []Match

	cur := a.root
	for i, r := range runes {
		rr := fold(r, a.caseInsensitive) // 折叠输入以匹配 (位置下标不变)
		for cur != a.root && cur.children[rr] == nil {
			cur = cur.fail
		}
		if next, ok := cur.children[rr]; ok {
			cur = next
		} else {
			cur = a.root
		}

		for t := cur; t != a.root; t = t.fail {
			if t.meta != nil {
				end := i + 1
				matches = append(matches, Match{
					Word:    t.meta.Word,
					Levels:  t.meta.Levels,
					Remarks: t.meta.Remarks,
					Start:   end - t.depth,
					End:     end,
				})
			}
		}
	}
	return matches
}

package matcher

import "testing"

// buildTestAutomaton 构造覆盖多种情形的测试自动机 (多备注/多等级/自定义等级/英文词)。
func buildTestAutomaton(ci bool) *Automaton {
	b := NewBuilder(ci)
	b.Add("大雷", []string{"High"}, []string{"大奶子", "大奶"})     // 多备注
	b.Add("废物", []string{"Medium"}, nil)
	b.Add("笨蛋", []string{"Low"}, []string{"轻微侮辱"})
	b.Add("雷", []string{"Low"}, nil)                          // 与 "大雷" 重叠
	b.Add("挖矿", []string{"bilibili", "引流"}, []string{"引流站点"}) // 一词多等级
	b.Add("pornhub", []string{"色情"}, []string{"黄色平台"})       // 英文词
	return b.Build()
}

func TestFindAll_MultiLevel(t *testing.T) {
	auto := buildTestAutomaton(false)
	matches := auto.FindAll("有人在挖矿")
	if len(matches) == 0 || matches[0].Word != "挖矿" {
		t.Fatalf("未命中 挖矿: %+v", matches)
	}
	lv := matches[0].Levels
	if len(lv) != 2 || lv[0] != "bilibili" || lv[1] != "引流" {
		t.Errorf("多等级解析错误: %+v", lv)
	}
}

func TestFindAll_MultiRemark(t *testing.T) {
	auto := buildTestAutomaton(false)
	for _, m := range auto.FindAll("你真是个大雷") {
		if m.Word == "大雷" {
			if len(m.Remarks) != 2 || m.Remarks[0] != "大奶子" || m.Remarks[1] != "大奶" {
				t.Errorf("多备注解析错误: %+v", m.Remarks)
			}
			if len(m.Levels) != 1 || m.Levels[0] != "High" {
				t.Errorf("等级错误: %+v", m.Levels)
			}
		}
	}
}

func TestLevels_Discovery(t *testing.T) {
	auto := buildTestAutomaton(false)
	want := map[string]bool{"High": true, "Low": true, "Medium": true, "bilibili": true, "引流": true, "色情": true}
	levels := auto.Levels()
	if len(levels) != len(want) {
		t.Fatalf("等级集合数量错误: %v", levels)
	}
	for _, lv := range levels {
		if !want[lv] {
			t.Errorf("出现未预期等级: %s", lv)
		}
	}
}

func TestFindAll_CaseInsensitive(t *testing.T) {
	auto := buildTestAutomaton(true)
	got := map[string]bool{}
	for _, m := range auto.FindAll("看 PORNHUB") {
		got[m.Word] = true
	}
	if !got["pornhub"] {
		t.Errorf("大小写不敏感匹配失败: %v", got)
	}
}

func TestFindAll_Overlap(t *testing.T) {
	auto := buildTestAutomaton(false)
	words := map[string]bool{}
	for _, m := range auto.FindAll("大雷") {
		words[m.Word] = true
	}
	if !words["大雷"] || !words["雷"] {
		t.Errorf("重叠匹配失败, 命中: %v", words)
	}
}

func TestFindAll_NoMatch(t *testing.T) {
	auto := buildTestAutomaton(false)
	if m := auto.FindAll("今天天气不错"); len(m) != 0 {
		t.Errorf("期望无命中, 实际: %+v", m)
	}
}

func TestFindAll_Position_Unicode(t *testing.T) {
	auto := buildTestAutomaton(false)
	for _, m := range auto.FindAll("🎉hi大雷") { // 🎉(0) h(1) i(2) 大(3) 雷(4)
		if m.Word == "大雷" && (m.Start != 3 || m.End != 5) {
			t.Errorf("emoji 场景位置错误 [%d,%d)", m.Start, m.End)
		}
	}
}

func TestParseRemarks_JSON(t *testing.T) {
	// JSON 里 remarks 既可数组也可逗号串, 均归一化为切片。
	cases := []struct {
		in   string
		want []string
	}{
		{`["大奶子","大奶"]`, []string{"大奶子", "大奶"}},
		{`"大奶子,大奶"`, []string{"大奶子", "大奶"}},
		{`"大奶子，大奶"`, []string{"大奶子", "大奶"}}, // 中文逗号
		{`null`, nil},
		{`[]`, []string{}},
	}
	for _, c := range cases {
		var s stringOrList
		if err := s.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", c.in, err)
		}
		if !eqSlice(s, c.want) {
			t.Errorf("UnmarshalJSON(%s) = %v, 期望 %v", c.in, []string(s), c.want)
		}
	}
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func BenchmarkFindAll(b *testing.B) {
	auto := buildTestAutomaton(false)
	text := "你真是个大雷加废物还是个笨蛋有人在挖矿还看pornhub这段文本用来压测匹配性能"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = auto.FindAll(text)
	}
}

func TestBuildFromEntries_MultiWord(t *testing.T) {
	// 一个词条含逗号分隔的多个敏感词, 共享 levels/remarks。
	entries := []Entry{
		{Word: "大雷,小雷", Levels: []string{"High"}, Remarks: []string{"女性胸部"}},
		{Word: "挖矿", Levels: []string{"bilibili"}, Remarks: nil},
	}
	auto := BuildFromEntries(entries, Options{})

	// "大雷" 和 "小雷" 应各自独立命中, 且都带 High + 女性胸部。
	for _, word := range []string{"大雷", "小雷"} {
		m := auto.FindAll("看那个" + word)
		if len(m) == 0 || m[0].Word != word {
			t.Fatalf("%q 未独立命中: %+v", word, m)
		}
		if len(m[0].Levels) != 1 || m[0].Levels[0] != "High" {
			t.Errorf("%q 等级错误: %+v", word, m[0].Levels)
		}
		if len(m[0].Remarks) != 1 || m[0].Remarks[0] != "女性胸部" {
			t.Errorf("%q 备注错误: %+v", word, m[0].Remarks)
		}
	}
	// 中文逗号也支持。
	auto2 := BuildFromEntries([]Entry{{Word: "甲，乙", Levels: []string{"L"}}}, Options{})
	if m := auto2.FindAll("甲乙"); len(m) != 2 {
		t.Errorf("中文逗号拆分失败, 命中数: %d", len(m))
	}
}

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"大雷,小雷", []string{"大雷", "小雷"}},
		{"甲，乙，丙", []string{"甲", "乙", "丙"}},
		{"挖矿", []string{"挖矿"}},
		{"a, b ,c", []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		if got := SplitWords(c.in); !eqSlice(got, c.want) {
			t.Errorf("SplitWords(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeWord(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  ,  ,TS,  ", "TS"},                    // 用户实际发的脏数据
		{"扶她, 伪娘 ,TS,男同", "扶她,伪娘,TS,男同"}, // 去段内空白
		{"挖矿", "挖矿"},
		{"  挖矿  ", "挖矿"},
		{" , , ", ""},   // 全空 -> 空串
		{"", ""},
		{"a，，b", "a,b"}, // 中文逗号 + 空段
	}
	for _, c := range cases {
		if got := NormalizeWord(c.in); got != c.want {
			t.Errorf("NormalizeWord(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeEntry(t *testing.T) {
	// 模拟用户那条脏请求。
	e := NormalizeEntry(Entry{
		Word:    "  ,  ,TS,  ",
		Levels:  []string{"bilibili", " Medium "},
		Remarks: []string{"     "}, // 纯空格备注应被丢弃
	})
	if e.Word != "TS" {
		t.Errorf("word 未清洗: %q", e.Word)
	}
	if !eqSlice(e.Levels, []string{"bilibili", "Medium"}) {
		t.Errorf("levels 未清洗: %v", e.Levels)
	}
	if len(e.Remarks) != 0 {
		t.Errorf("空白备注应被丢弃, 实际: %v", e.Remarks)
	}
}

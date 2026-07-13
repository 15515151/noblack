package api

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"noblack/internal/matcher"
	"noblack/internal/stats"
	"noblack/internal/store"
)

// Handler 聚合所有 HTTP 处理逻辑, 持有 Store 与统计收集器。
type Handler struct {
	store   *store.Store
	metrics *stats.Collector
	token   string // 词条写操作的鉴权令牌; 为空表示不鉴权
}

// NewHandler 创建 Handler。token 为空时词条写操作不鉴权 (向后兼容)。
func NewHandler(s *store.Store, m *stats.Collector, token string) *Handler {
	return &Handler{store: s, metrics: m, token: token}
}

// ---------- 鉴权 ----------

// authEnabled 报告是否启用了写操作鉴权。
func (h *Handler) authEnabled() bool { return h.token != "" }

// tokenFromRequest 从请求中提取令牌, 支持两种写法:
//   - X-Auth-Token: <token>
//   - Authorization: Bearer <token>
func tokenFromRequest(r *http.Request) string {
	if t := r.Header.Get("X-Auth-Token"); t != "" {
		return t
	}
	if a := r.Header.Get("Authorization"); a != "" {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	return ""
}

// checkAuth 校验请求令牌。未启用鉴权时恒通过。
// 用 subtle.ConstantTimeCompare 做定长比较, 避免计时侧信道。
func (h *Handler) checkAuth(r *http.Request) bool {
	if !h.authEnabled() {
		return true
	}
	got := tokenFromRequest(r)
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

// requireAuth 校验失败时写 401 并返回 false。
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.checkAuth(r) {
		return true
	}
	writeErr(w, http.StatusUnauthorized, "令牌无效或缺失")
	return false
}

// Register 注册所有路由。
func (h *Handler) Register(mux *http.ServeMux) {
	// 检测与运维
	mux.HandleFunc("/check", h.handleCheck)
	mux.HandleFunc("/reload", h.handleReload)
	mux.HandleFunc("/levels", h.handleLevels)
	mux.HandleFunc("/health", h.handleHealth)

	// 词库 CRUD
	mux.HandleFunc("/words", h.handleWords)       // GET 列表 / POST 新增
	mux.HandleFunc("/words/", h.handleWordByID)   // PUT 更新 / DELETE 删除 (/words/{word})

	// 统计
	mux.HandleFunc("/stats", h.handleStats)
	mux.HandleFunc("/stats/reset", h.handleStatsReset)

	// 鉴权状态 (前端据此决定是否显示令牌框、校验令牌)
	mux.HandleFunc("/auth/status", h.handleAuthStatus)
	mux.HandleFunc("/auth/verify", h.handleAuthVerify)

	// 前端页面
	mux.HandleFunc("/", h.handleIndex)
}

// ---------- 统一响应 ----------

type apiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, httpStatus int, resp apiResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeErr(w http.ResponseWriter, httpStatus int, msg string) {
	writeJSON(w, httpStatus, apiResponse{Code: httpStatus, Message: msg})
}

// ---------- /check ----------

type checkRequest struct {
	Text string `json:"text"`
}

type position struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type matchItem struct {
	Word     string   `json:"word"`
	Levels   []string `json:"levels"`
	Remarks  []string `json:"remarks"`
	Position position `json:"position"`
}

type checkData struct {
	HasSensitiveWord bool        `json:"has_sensitive_word"`
	Matches          []matchItem `json:"matches"`
}

func (h *Handler) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	var req checkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	// 无锁获取当前自动机并匹配。
	rawMatches := h.store.Current().FindAll(req.Text)

	items := make([]matchItem, 0, len(rawMatches))
	hitWords := make([]string, 0, len(rawMatches))
	for _, m := range rawMatches {
		levels := m.Levels
		if levels == nil {
			levels = []string{}
		}
		remarks := m.Remarks
		if remarks == nil {
			remarks = []string{}
		}
		items = append(items, matchItem{
			Word:     m.Word,
			Levels:   levels,
			Remarks:  remarks,
			Position: position{Start: m.Start, End: m.End},
		})
		hitWords = append(hitWords, m.Word)
	}

	// 记录统计 (原子操作, ~纳秒级, 不影响吞吐)。
	h.metrics.RecordCheck(hitWords)

	writeJSON(w, http.StatusOK, apiResponse{
		Code:    200,
		Message: "success",
		Data:    checkData{HasSensitiveWord: len(items) > 0, Matches: items},
	})
}

// ---------- /reload ----------

func (h *Handler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	n, err := h.store.Reload()
	if err != nil {
		log.Printf("[reload] 失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "热加载失败: "+err.Error())
		return
	}
	h.metrics.RecordReload()
	log.Printf("[reload] 成功, 词条数: %d", n)
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "reloaded", Data: map[string]int{"word_count": n}})
}

// ---------- /levels ----------

func (h *Handler) handleLevels(w http.ResponseWriter, r *http.Request) {
	levels := h.store.Current().Levels()
	if levels == nil {
		levels = []string{}
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "success",
		Data: map[string]interface{}{"levels": levels, "count": len(levels)},
	})
}

// ---------- /health ----------

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	auto := h.store.Current()
	levels := auto.Levels()
	if levels == nil {
		levels = []string{}
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "ok",
		Data: map[string]interface{}{"word_count": auto.Size(), "levels": levels},
	})
}

// ---------- /words (GET 列表 / POST 新增) ----------

func (h *Handler) handleWords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries := h.store.ListEntries()
		writeJSON(w, http.StatusOK, apiResponse{
			Code: 200, Message: "success",
			Data: map[string]interface{}{"words": entries, "count": len(entries)},
		})
	case http.MethodPost:
		if !h.requireAuth(w, r) { // 写操作: 需令牌
			return
		}
		var e matcher.Entry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		e = matcher.NormalizeEntry(e) // 清洗后再存, 保证响应与落盘一致
		if err := h.store.AddEntry(e); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("[words] 新增词条: %s", e.Word)
		writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "created", Data: e})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET / POST")
	}
}

// ---------- /words/{word} (PUT 更新 / DELETE 删除) ----------

func (h *Handler) handleWordByID(w http.ResponseWriter, r *http.Request) {
	// 路径形如 /words/挖矿, 取 /words/ 之后的部分。
	// r.URL.Path 已由 net/http 完成 URL 解码, 中文可直接使用。
	word := trimPrefixPath(r.URL.Path, "/words/")
	if word == "" {
		writeErr(w, http.StatusBadRequest, "缺少词条, 路径应为 /words/{word}")
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !h.requireAuth(w, r) { // 写操作: 需令牌
			return
		}
		var e matcher.Entry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if e.Word == "" {
			e.Word = word // 允许 body 省略 word, 用路径里的
		}
		e = matcher.NormalizeEntry(e) // 清洗后再存, 保证响应与落盘一致
		if err := h.store.UpdateEntry(e); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		log.Printf("[words] 更新词条: %s", e.Word)
		writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "updated", Data: e})
	case http.MethodDelete:
		if !h.requireAuth(w, r) { // 写操作: 需令牌
			return
		}
		if err := h.store.DeleteEntry(word); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		log.Printf("[words] 删除词条: %s", word)
		writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "deleted", Data: map[string]string{"word": word}})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 PUT / DELETE")
	}
}

// ---------- /stats ----------

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	// 支持 ?top=N 限制返回的高频词数量, 默认 20。
	topN := 20
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			topN = n
		}
	}
	snap := h.metrics.Snapshot(topN)
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "success", Data: snap})
}

func (h *Handler) handleStatsReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	h.metrics.Reset()
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "reset"})
}

// ---------- 鉴权状态 / 校验 ----------

// handleAuthStatus GET /auth/status: 告诉前端是否需要令牌 (是否启用了写鉴权)。
func (h *Handler) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "success",
		Data: map[string]bool{"required": h.authEnabled()},
	})
}

// handleAuthVerify POST /auth/verify: 校验请求携带的令牌是否正确, 供前端"验证"按钮使用。
// 未启用鉴权时恒返回成功。
func (h *Handler) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !h.checkAuth(r) {
		writeErr(w, http.StatusUnauthorized, "令牌无效或缺失")
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "ok"})
}

// ---------- 前端页面 ----------

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

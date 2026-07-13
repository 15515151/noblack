package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"noblack/internal/matcher"
	"noblack/internal/stats"
	"noblack/internal/store"
)

func newTestHandler(t *testing.T, token string) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "挖矿", Levels: []string{"L"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	st := store.New(path, entries, matcher.Options{})
	return NewHandler(st, stats.New(), token)
}

func do(h *Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.Register(mux)
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAuth_WritesBlockedWithoutToken(t *testing.T) {
	h := newTestHandler(t, "s3cret")

	// 无令牌新增 -> 401
	if rec := do(h, "POST", "/words", `{"word":"x","levels":["A"]}`, nil); rec.Code != 401 {
		t.Errorf("无令牌 POST 应 401, 实际 %d", rec.Code)
	}
	// 错令牌 -> 401
	if rec := do(h, "POST", "/words", `{"word":"x","levels":["A"]}`, map[string]string{"X-Auth-Token": "bad"}); rec.Code != 401 {
		t.Errorf("错令牌 POST 应 401, 实际 %d", rec.Code)
	}
	// 正确令牌 (X-Auth-Token) -> 200
	if rec := do(h, "POST", "/words", `{"word":"x","levels":["A"]}`, map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != 200 {
		t.Errorf("正确令牌 POST 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	// 正确令牌 (Bearer) -> 200
	if rec := do(h, "PUT", "/words/x", `{"levels":["B"]}`, map[string]string{"Authorization": "Bearer s3cret"}); rec.Code != 200 {
		t.Errorf("Bearer 令牌 PUT 应 200, 实际 %d", rec.Code)
	}
	// 无令牌删除 -> 401
	if rec := do(h, "DELETE", "/words/x", "", nil); rec.Code != 401 {
		t.Errorf("无令牌 DELETE 应 401, 实际 %d", rec.Code)
	}
}

func TestAuth_ReadsAlwaysOpen(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	// GET /words 不需令牌
	if rec := do(h, "GET", "/words", "", nil); rec.Code != 200 {
		t.Errorf("GET /words 应 200, 实际 %d", rec.Code)
	}
	// /check 不需令牌
	if rec := do(h, "POST", "/check", `{"text":"挖矿"}`, nil); rec.Code != 200 {
		t.Errorf("/check 应 200, 实际 %d", rec.Code)
	}
	// /stats 不需令牌
	if rec := do(h, "GET", "/stats", "", nil); rec.Code != 200 {
		t.Errorf("/stats 应 200, 实际 %d", rec.Code)
	}
}

func TestAuth_DisabledWhenNoToken(t *testing.T) {
	h := newTestHandler(t, "") // 未设令牌 = 不鉴权
	if rec := do(h, "POST", "/words", `{"word":"y","levels":["A"]}`, nil); rec.Code != 200 {
		t.Errorf("未启用鉴权时 POST 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	// /auth/status 应报告 required:false
	rec := do(h, "GET", "/auth/status", "", nil)
	if !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Errorf("未启用鉴权 status 应 required:false, 实际 %s", rec.Body)
	}
}

func TestAuth_VerifyEndpoint(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	if rec := do(h, "POST", "/auth/verify", "", map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != 200 {
		t.Errorf("正确令牌 verify 应 200, 实际 %d", rec.Code)
	}
	if rec := do(h, "POST", "/auth/verify", "", map[string]string{"X-Auth-Token": "nope"}); rec.Code != 401 {
		t.Errorf("错令牌 verify 应 401, 实际 %d", rec.Code)
	}
}

func TestAuth_ManagementWritesRequireToken(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	for _, path := range []string{"/reload", "/stats/reset"} {
		if rec := do(h, http.MethodPost, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 未携带令牌，实际状态码 %d，期望 401", path, rec.Code)
		}
		if rec := do(h, http.MethodPost, path, "", map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusOK {
			t.Errorf("%s 携带正确令牌，实际状态码 %d，期望 200: %s", path, rec.Code, rec.Body)
		}
	}
}

func TestRequestBodyPolicy(t *testing.T) {
	payload := func(size int) string {
		const prefix = `{"text":"`
		const suffix = `"}`
		return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	}

	h := newTestHandler(t, "s3cret")
	if rec := do(h, http.MethodPost, "/check", payload(normalRequestBodyLimit), nil); rec.Code != http.StatusOK {
		t.Fatalf("3 MiB 请求实际状态码 %d，期望 200: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/check", payload(normalRequestBodyLimit+1), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("超过 3 MiB 且未携带令牌，实际状态码 %d，期望 401", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/check", payload(normalRequestBodyLimit+1), map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusOK {
		t.Fatalf("超过 3 MiB 且携带正确令牌，实际状态码 %d，期望 200: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/check", payload(maximumRequestBodyLimit+1), map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过 10 MiB，实际状态码 %d，期望 413", rec.Code)
	}

	withoutConfiguredToken := newTestHandler(t, "")
	if rec := do(withoutConfiguredToken, http.MethodPost, "/check", payload(normalRequestBodyLimit+1), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("服务未配置令牌时发送超过 3 MiB 请求，实际状态码 %d，期望 401", rec.Code)
	}
}

func TestRequestBodyPolicyChunkedUnknownLength(t *testing.T) {
	payload := func(size int) string {
		const prefix = `{"text":"`
		const suffix = `"}`
		return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	}
	callUnknownLength := func(h *Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(body))
		req.ContentLength = -1
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux := http.NewServeMux()
		h.Register(mux)
		mux.ServeHTTP(rec, req)
		return rec
	}

	h := newTestHandler(t, "s3cret")
	if rec := callUnknownLength(h, payload(normalRequestBodyLimit+1), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知长度请求超过 3 MiB 且未携带令牌，实际状态码 %d，期望 401", rec.Code)
	}
	if rec := callUnknownLength(h, payload(maximumRequestBodyLimit+1), map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("未知长度请求超过 10 MiB，实际状态码 %d，期望 413", rec.Code)
	}
}

func TestUpdateRejectsBodyPathMismatch(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	rec := do(h, http.MethodPut, "/words/%E6%8C%96%E7%9F%BF", `{"word":"其他词","levels":["B"]}`, map[string]string{"X-Auth-Token": "s3cret"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("请求体词条与路径不一致，实际状态码 %d，期望 400: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/words", "", nil); !strings.Contains(rec.Body.String(), `"levels":["L"]`) {
		t.Fatalf("拒绝更新后原词条发生了变化: %s", rec.Body)
	}
}

func TestIndexDoesNotEmbedWordsInInlineHandlers(t *testing.T) {
	h := newTestHandler(t, "")
	rec := do(h, http.MethodGet, "/", "", nil)
	body := rec.Body.String()
	if strings.Contains(body, "onclick='editWord(") || strings.Contains(body, "onclick='delWord(") {
		t.Fatalf("词条操作仍在使用内联事件处理器")
	}
	if !strings.Contains(body, `data-word-action="edit"`) || !strings.Contains(body, `addEventListener('click'`) {
		t.Fatalf("未找到安全的事件委托实现")
	}
}

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

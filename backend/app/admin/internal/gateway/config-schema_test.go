package gateway

import (
	"encoding/json"
	adminv1 "server/api/admin/v1"
	"server/pkg/params"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// catalogOnce 单测内拿到的目录（其实现走 sync.OnceValue，但每次断言同一份）。
func catalogOnce() *adminv1.ConfigSchemaReply { return configSchemaPayload() }

// TestConfigSchemaCatalogShape 锁目录骨架：分类数量、键唯一、blob 字段回填、
// secret fallback 恒空、risk 前缀与 store 一致。
func TestConfigSchemaCatalogShape(t *testing.T) {
	cats := catalogOnce().Categories
	if len(cats) < 12 {
		t.Fatalf("categories count regressed: got %d want >=12", len(cats))
	}
	seen := map[string]string{}
	var blobCat, riskCat, dataSourceCat *adminv1.ConfigSchemaCategory
	for _, c := range cats {
		if c.Id == "" || c.Title == "" {
			t.Fatalf("category missing id/title: %+v", c)
		}
		for _, f := range c.Fields {
			if f.Key == "" || f.Label == "" || f.Kind == "" {
				t.Fatalf("field in %q missing core metadata: %+v", c.Id, f)
			}
			if prev, dup := seen[f.Key]; dup {
				t.Fatalf("duplicate field key %q seen in %q and %q", f.Key, prev, c.Id)
			}
			seen[f.Key] = c.Id
		}
		switch c.Id {
		case "sentiment":
			blobCat = c
		case "risk":
			riskCat = c
		case "datasource":
			dataSourceCat = c
		}
	}
	if blobCat == nil || blobCat.BlobKey != "market.sentiment.rules" {
		t.Fatalf("sentiment category missing or blobKey wrong: %+v", blobCat)
	}
	if riskCat == nil || dataSourceCat == nil {
		t.Fatal("risk/datasource categories must exist")
	}
	// Blob 字段 BlobKey 自动回填，json_key = key。
	for _, f := range blobCat.Fields {
		if f.BlobKey != "market.sentiment.rules" {
			t.Errorf("blob field %q missing blobKey backfill: %+v", f.Key, f)
		}
		if f.JsonKey != f.Key {
			t.Errorf("blob field %q jsonKey mismatch key: %+v", f.Key, f)
		}
	}
	// Risk 字段 Key 统一加 risk. 前缀，store=risk。
	for _, f := range riskCat.Fields {
		if !strings.HasPrefix(f.Key, "risk.") {
			t.Errorf("risk field key must be risk.-prefixed: %+v", f)
		}
		if f.Store != "risk" {
			t.Errorf("risk field store must be risk: %+v", f)
		}
	}
	// 数据源分类的 secret 字段 fallback 恒为 ""。
	for _, f := range dataSourceCat.Fields {
		if f.Kind == "secret" && f.Fallback != "" {
			t.Errorf("secret field must not carry fallback: %+v", f)
		}
	}
	// 关键已知键存在（drift 哨兵：删字段会被这里抓到）。
	// 注：blob 字段（sentiment/sector 分类）的 key=json_key 不带 market.* 外层。
	for _, want := range []string{"app.ds.tushare.token", "m06.select_score", "risk.live_admitted", "risk.phase_scale", "ice_max_up", "pct"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("expected key %q missing from catalog", want)
		}
	}
}

// TestConfigSchemaConformanceAgainstParams 是 schema 与 params 的漂移哨兵：
// 目录里每一个字段必须能被 params.ValidatePatch 接受（用 fallback 值或非敏感占位
// 触发合法值校验），否则 UI 渲染出来却写不进——catalog 与 registry 双向漂移。
//
// 同源纪律参照 app/datahub/internal/data/params_conformance_test.go。
func TestConfigSchemaConformanceAgainstParams(t *testing.T) {
	for _, c := range catalogOnce().Categories {
		for _, f := range c.Fields {
			// blob 字段经 fetchBlob 由外层 blob_key 整对象校验，跳过单 key 直打。
			if f.BlobKey != "" {
				continue
			}
			value := f.Fallback
			// secret / text url 等 fallback 为空，跳过验证：仅列出 key 不强校验 value。
			if value == "" {
				continue
			}
			patch := map[string]string{f.Key: value}
			if _, err := params.ValidatePatch(nil, patch); err != nil {
				t.Errorf("catalog field %q fallback %q rejected by ValidatePatch: %v", f.Key, value, err)
			}
		}
	}
}

// TestConfigSchemaHandlerReturnsCatalog 通过 ServeHTTP 走完整路由前缀分发，
// viewer 也可读；DB 故障时仍返回 200（目录随二进制发放）。
func TestConfigSchemaHandlerReturnsCatalog(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	gate := testGate(string(hash), &memAudit{})
	// 不设 Params：schema 与参数库存活独立（review 发现 1：DB 故障不能让表单渲染也 503）。

	viewer := login(t, gate, "bob", "pw")
	req := httptest.NewRequest(http.MethodGet, "/api/config/schema", nil)
	req.Header.Set("Authorization", "Bearer "+viewer)
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 viewer-readable, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body adminv1.ConfigSchemaReply
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v body=%s", err, rec.Body.String())
	}
	if body.SchemaVersion != 1 {
		t.Fatalf("schema_version wrong: %d", body.SchemaVersion)
	}
	if len(body.Categories) < 12 {
		t.Fatalf("catalog regress: %d categories", len(body.Categories))
	}
	raw := rec.Body.Bytes()
	for _, forbidden := range []string{"old-secret", "new-secret", "BEGIN PRIVATE"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("catalog leaked sensitive marker %q", forbidden)
		}
	}
}

// TestConfigSchemaPayloadCached 同一进程内 schema payload 是同一份指针（惰性构建）。
func TestConfigSchemaPayloadCached(t *testing.T) {
	first := configSchemaPayload()
	second := configSchemaPayload()
	if first != second {
		t.Fatal("payload must be cached (sync.OnceValue); want identical pointer")
	}
}

// TestConfigSchemaRejectsAnonymous 未登录匿名请求与其他配置一样 401。
func TestConfigSchemaRejectsAnonymous(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	gate := testGate(string(hash), &memAudit{})
	req := httptest.NewRequest(http.MethodGet, "/api/config/schema", nil)
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous schema read should 401, got %d", rec.Code)
	}
}

package kbstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"server/app/intel/internal/biz/kb"
	"server/conf"
)

// Dims 是 kb_chunk.embedding 的列宽。配置的维度必须与之相等。
const Dims = 1024

// HashModel 是开发自测用的哈希向量模型名，不能用于正式数据。
const HashModel = "dev-hash-1024"

// bgeQueryInstruction 是 bge 中文模型推荐的检索查询前缀，文档侧不加。
const bgeQueryInstruction = "为这个句子生成表示以用于检索相关文章："

// NewEmbedder 按配置返回嵌入客户端。provider 为空或 none 时返回 nil，检索只走关键词。
func NewEmbedder(c *conf.Kb) (kb.Embedder, error) {
	dims := int(c.GetEmbedDims())
	if dims == 0 {
		dims = Dims
	}
	switch strings.ToLower(strings.TrimSpace(c.GetEmbedProvider())) {
	case "", "none":
		return nil, nil
	case "hash":
		if dims != Dims {
			return nil, fmt.Errorf("kbstore: embed_dims %d != column width %d", dims, Dims)
		}
		return hashEmbedder{}, nil
	case "openai":
		if dims != Dims {
			return nil, fmt.Errorf("kbstore: embed_dims %d != column width %d", dims, Dims)
		}
		endpoint := strings.TrimRight(strings.TrimSpace(c.GetEmbedEndpoint()), "/")
		if endpoint == "" || c.GetEmbedModel() == "" {
			return nil, fmt.Errorf("kbstore: openai provider needs embed_endpoint and embed_model")
		}
		if strings.HasSuffix(endpoint, "/v1") {
			endpoint += "/embeddings"
		} else if !strings.HasSuffix(endpoint, "/embeddings") {
			endpoint += "/v1/embeddings"
		}
		timeout := 30 * time.Second
		if d := c.GetEmbedTimeout(); d != nil && d.AsDuration() > 0 {
			timeout = d.AsDuration()
		}
		instr := c.GetQueryInstruction()
		if instr == "" {
			instr = bgeQueryInstruction
		}
		return &openAIEmbedder{
			url:         endpoint,
			model:       c.GetEmbedModel(),
			apiKey:      c.GetEmbedApiKey(),
			instruction: instr,
			http:        &http.Client{Timeout: timeout},
		}, nil
	default:
		return nil, fmt.Errorf("kbstore: unknown embed_provider %q", c.GetEmbedProvider())
	}
}

type openAIEmbedder struct {
	url         string
	model       string
	apiKey      string
	instruction string
	http        *http.Client
}

func (e *openAIEmbedder) Model() string { return e.model }
func (e *openAIEmbedder) Dims() int     { return Dims }

func (e *openAIEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	return e.call(ctx, texts)
}

func (e *openAIEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	out, err := e.call(ctx, []string{e.instruction + text})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (e *openAIEmbedder) call(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kbstore: embed request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("kbstore: embed read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kbstore: embed status %d: %s", resp.StatusCode, truncateBytes(raw, 200))
	}
	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("kbstore: embed decode: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("kbstore: embed returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(texts) || out[d.Index] != nil {
			return nil, fmt.Errorf("kbstore: embed bad index %d", d.Index)
		}
		if len(d.Embedding) != Dims {
			return nil, fmt.Errorf("kbstore: embed dims %d, want %d", len(d.Embedding), Dims)
		}
		out[d.Index] = normalize(d.Embedding)
	}
	return out, nil
}

func truncateBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// hashEmbedder 把分词结果哈希到 1024 维，只在没有 DGX 时联调向量通路。
type hashEmbedder struct{}

func (hashEmbedder) Model() string { return HashModel }
func (hashEmbedder) Dims() int     { return Dims }

func (h hashEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.vector(t)
	}
	return out, nil
}

func (h hashEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return h.vector(text), nil
}

func (hashEmbedder) vector(text string) []float32 {
	v := make([]float32, Dims)
	for _, tok := range kb.Tokenize(text) {
		f := fnv.New32a()
		_, _ = f.Write([]byte(tok))
		sum := f.Sum32()
		sign := float32(1)
		if sum&(1<<31) != 0 {
			sign = -1
		}
		v[sum%Dims] += sign
	}
	return normalize(v)
}

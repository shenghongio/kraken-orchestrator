package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// Client 是Agent 调用 Schedule HTTP API 的客户端
type Client struct {
	baseURL    string       // Scheduler 的基础URL ,例如 http://scheduler:8080
	httpClient *http.Client // 共享的 HTTP client,带超时控制
}

// ClaimRequest 领取任务接口
type ClaimRequest struct {
	AgentID string `json:"agent_id"`
}

// NewClient 创建 Scheduler 客户端
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second, // 防止慢请求卡住 agent
		},
	}
}

// RegisterRequest Agent 注册请求
type RegisterRequest struct {
	AgentID        string   `json:"agent_id"`
	Version        string   `json:"version"`
	Capabilities   []string `json:"capabilities"`
	MaxConcurrency int      `json:"max_concurrency"`
}

// Register 向 Scheduler 注册或更新 Agent 信息
func (c *Client) Register(ctx context.Context, req *RegisterAgentRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal register request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/agent/register", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
}

// Heartbeat 向 Scheduler 发送心跳 ,上报当前 busy_slots
func (c *Client) Heartbeat(ctx context.Context, req *HeartbeatRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal heartbeat request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/agent/heartbeat", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return domain.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
}

// Claim 向 Scheduler 申领一个任务，无任务时返回 (nil, nil)
func (c *Client) Claim(ctx context.Context, req *ClaimTaskRequest) (*ClaimTaskResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal claim request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/agent/tasks/claim", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 204 No Content: 没有可申领的任务
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var claimResp ClaimTaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&claimResp); err != nil {
		return nil, fmt.Errorf("decode claim response: %w", err)
	}
	return &claimResp, nil
}

// SendCallback 向 Scheduler 上报任务执行结果
func (c *Client) SendCallback(ctx context.Context, req *CallbackRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal callback request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/callback", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusConflict:
		return domain.ErrStaleCallback
	case http.StatusNotFound:
		return domain.ErrNotFound
	default:
		return readError(resp)
	}
}

// do 发送HTTP 请求
func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	withContext, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	withContext.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(withContext)
}

// readError 读取错误响应
func readError(resp *http.Response) error {
	readAll, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("scheduler returned %d: %s", resp.StatusCode, string(readAll))
}

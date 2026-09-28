package tencentdocs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	sitePersonal = "personal"
	siteSaaS     = "saas"

	personalEndpoint = "https://docs.qq.com/openapi/mcp"
	saasEndpoint     = "https://saas.docs.qq.com/api/v6/open/agent/mcp"
)

type mcpClient struct {
	http     *http.Client
	token    string
	endpoint string
}

func newMCPClient(site, token string) (*mcpClient, error) {
	endpoint := ""
	switch site {
	case sitePersonal:
		endpoint = personalEndpoint
	case siteSaaS:
		endpoint = saasEndpoint
	default:
		return nil, fmt.Errorf("unsupported Tencent Docs site")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Tencent Docs token is required")
	}
	return &mcpClient{
		http:  &http.Client{Timeout: 45 * time.Second},
		token: strings.TrimSpace(token), endpoint: endpoint,
	}, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	Result struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
		Content           []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *mcpClient) call(ctx context.Context, tool string, arguments any, output any) error {
	payload, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: map[string]any{"name": tool, "arguments": arguments},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Tencent Docs request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Tencent Docs returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var envelope rpcResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("invalid Tencent Docs response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("Tencent Docs tool %s failed (code %d): %s",
			tool, envelope.Error.Code, envelope.Error.Message)
	}
	data := envelope.Result.StructuredContent
	if len(data) == 0 && len(envelope.Result.Content) > 0 {
		data = []byte(envelope.Result.Content[0].Text)
	}
	if envelope.Result.IsError || len(data) == 0 {
		return fmt.Errorf("Tencent Docs tool %s returned no usable result", tool)
	}
	var status struct {
		ErrorCode int    `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return fmt.Errorf("invalid Tencent Docs tool result: %w", err)
	}
	if status.ErrorCode != 0 || status.Error != "" {
		return fmt.Errorf("Tencent Docs tool %s failed (code %d): %s",
			tool, status.ErrorCode, strings.TrimSpace(status.ErrorMsg+" "+status.Error))
	}
	return json.Unmarshal(data, output)
}

package mockplus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const defaultBaseURL = "https://ued.gree.com:6060"
const defaultTeamID = "ahywvxjgmy"
const maxImageBytes = 100 << 20

type client struct {
	base           *url.URL
	team           string
	user, password string
	http           *http.Client
	mu             sync.Mutex
	cookies        []*http.Cookie
}

// Session cookies never leave this connector's memory. Non-secret settings can
// also arrive in credentials from the existing validate-credentials endpoint.
func (c *Connector) client(config *types.DataSourceConfig) (*client, error) {
	return c.clientWithTeam(config, true)
}

func (c *Connector) clientWithTeam(config *types.DataSourceConfig, requireTeam bool) (*client, error) {
	if config == nil {
		return nil, errors.New("摹客配置缺失")
	}
	setting := func(key, fallback string) string {
		if v, ok := config.Settings[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if v, ok := config.Credentials[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return fallback
	}
	user, _ := config.Credentials["username"].(string)
	password, _ := config.Credentials["password"].(string)
	user = strings.TrimSpace(user)
	pageCredentials := user != "" || password != "" || setting("auth_mode", "") == "password"
	baseDefault := ""
	teamDefault := ""
	if !pageCredentials {
		baseDefault = envOr("MOCKPLUS_BASE_URL", defaultBaseURL)
		teamDefault = envOr("MOCKPLUS_TEAM_ID", defaultTeamID)
	}
	baseText := setting("base_url", baseDefault)
	base, err := url.Parse(strings.TrimRight(baseText, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("摹客服务地址必须是 HTTP 或 HTTPS 服务根地址")
	}
	if !pageCredentials {
		// Legacy environment credentials remain bound to their configured host.
		allowed, err := url.Parse(strings.TrimRight(envOr("MOCKPLUS_BASE_URL", defaultBaseURL), "/"))
		if err != nil || !sameOrigin(base, allowed) {
			return nil, errors.New("旧版环境凭证仅用于本地配置的服务地址，请在页面配置账号密码")
		}
		user, password = types.MockplusEnvironmentCredentials()
	}
	if user == "" || password == "" {
		return nil, errors.New("请填写摹客账号和密码")
	}
	insecure, _ := config.Settings["insecure_tls"].(bool)
	if len(config.Settings) == 0 {
		insecure, _ = config.Credentials["insecure_tls"].(bool)
	}
	team := setting("team_id", teamDefault)
	if requireTeam && team == "" {
		return nil, errors.New("请填写摹客团队 ID")
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(base.String()+"\x00"+team+"\x00"+user+"\x00"+password+fmt.Sprint(insecure))))
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.clients[key]; existing != nil {
		return existing, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} // Explicit per-connector private deployment setting.
	cl := &client{base: base, team: team, user: user, password: password}
	cl.http = &http.Client{Transport: transport, Timeout: 120 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("摹客请求重定向过多")
			}
			if !sameOrigin(req.URL, base) {
				req.Header.Del("Cookie")
				req.Header.Del("Authorization")
			}
			if len(via) > 0 && via[0].URL.Path == "/api/v1/user/signin" {
				return errors.New("摹客登录接口发生重定向")
			}
			return nil
		},
	}
	// Bound cached clients after credential rotation.
	if len(c.clients) >= 8 {
		c.clients = make(map[string]*client)
	}
	c.clients[key] = cl
	return cl, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (c *client) loginLocked(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"email": c.user, "password": c.password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/api/v1/user/signin", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法连接摹客登录接口，请检查网络和证书设置")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("摹客登录失败（HTTP %d）", resp.StatusCode)
	}
	var result struct {
		Code *int `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil || result.Code == nil {
		return errors.New("摹客登录响应无效")
	}
	if *result.Code != 0 {
		return fmt.Errorf("摹客登录失败，请检查账号密码（业务码 %d）", *result.Code)
	}
	cookies := []*http.Cookie{}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "ds.sid" || cookie.Name == "ds.sid.sig" {
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) != 2 {
		return errors.New("摹客登录未返回完整会话")
	}
	c.cookies = cookies
	return nil
}

func (c *client) request(ctx context.Context, method, path string, conditional http.Header, refresh bool) (*http.Response, error) {
	u, err := c.base.Parse(path)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("摹客图片地址无效")
	}
	c.mu.Lock()
	if refresh || len(c.cookies) == 0 {
		if err := c.loginLocked(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	cookies := append([]*http.Cookie(nil), c.cookies...)
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, errors.New("摹客请求无效")
	}
	if sameOrigin(u, c.base) {
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
	}
	for k, values := range conditional {
		req.Header[k] = values
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("摹客请求失败，请检查网络或证书设置")
	}
	return resp, nil
}

func (c *client) api(ctx context.Context, path string, target any) error {
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.request(ctx, http.MethodGet, path, nil, attempt > 0)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			return errors.New("读取摹客响应失败")
		}
		var envelope struct {
			Code    *int            `json:"code"`
			Payload json.RawMessage `json:"payload"`
			Data    json.RawMessage `json:"data"`
		}
		decodeErr := json.Unmarshal(data, &envelope)
		if resp.StatusCode == http.StatusUnauthorized || (decodeErr == nil && envelope.Code != nil && *envelope.Code == 401) {
			if attempt == 0 {
				continue
			}
			return errors.New("摹客会话失效，请检查本地账号配置")
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("摹客接口请求失败（HTTP %d）", resp.StatusCode)
		}
		if decodeErr != nil {
			return errors.New("摹客接口返回了无效数据")
		}
		if envelope.Code != nil && *envelope.Code != 0 {
			return fmt.Errorf("摹客接口请求失败（业务码 %d）", *envelope.Code)
		}
		body := envelope.Payload
		if len(body) == 0 || string(body) == "null" {
			body = envelope.Data
		}
		if len(body) == 0 || string(body) == "null" {
			body = data
		}
		if json.Unmarshal(body, target) != nil {
			return errors.New("摹客接口数据结构不受支持")
		}
		return nil
	}
	return errors.New("摹客会话失效")
}

func (c *client) image(ctx context.Context, path string, old boardState) ([]byte, string, string, bool, error) {
	headers := http.Header{}
	if old.Hash != "" {
		if old.ETag != "" {
			headers.Set("If-None-Match", old.ETag)
		} else if old.LastModified != "" {
			headers.Set("If-Modified-Since", old.LastModified)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.request(ctx, http.MethodGet, path, headers, attempt > 0)
		if err != nil {
			return nil, "", "", false, err
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			if attempt == 0 {
				continue
			}
			return nil, "", "", false, errors.New("摹客图片读取无权限")
		}
		if resp.StatusCode == http.StatusNotModified && old.Hash != "" {
			resp.Body.Close()
			return nil, old.ETag, old.LastModified, true, nil
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, "", "", false, fmt.Errorf("摹客图片下载失败（HTTP %d）", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, "", "", false, errors.New("摹客图片下载未完成")
		}
		if len(data) > maxImageBytes {
			return nil, "", "", false, errors.New("摹客单张图片超过 100 MiB")
		}
		if len(data) < 8 || !bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
			var auth struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(data, &auth) == nil && auth.Code == 401 && attempt == 0 {
				continue
			}
			return nil, "", "", false, errors.New("摹客图片响应不是 PNG，未入库")
		}
		return data, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), false, nil
	}
	return nil, "", "", false, errors.New("摹客图片下载失败")
}

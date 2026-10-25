// Package weread contains the volatile adapter for Tencent's official skill gateway.
package weread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const GatewayURL = "https://i.weread.qq.com/api/agent/gateway"
const SkillVersion = "1.0.5"

var allowed = map[string]bool{
	"/_list": true, "/store/search": true, "/shelf/sync": true, "/user/notebooks": true,
	"/book/info": true, "/book/chapterinfo": true, "/book/getprogress": true,
	"/book/bookmarklist": true, "/review/list/mine": true, "/book/underlines": true,
	"/book/bestbookmarks": true, "/book/readreviews": true, "/review/single": true,
	"/review/list": true, "/book/recommend": true, "/book/similar": true, "/readdata/detail": true,
}

type Gateway interface {
	Call(context.Context, string, map[string]any) (json.RawMessage, error)
}

type Client struct {
	endpoint, key string
	http          *http.Client
	mu            sync.Mutex
	last          time.Time
	Interval      time.Duration
}

type UpgradeError struct{ Message string }

// AuthenticationError distinguishes rejected keys from network/service failures.
type AuthenticationError struct {
	API        string
	StatusCode int
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("%s: authentication rejected (HTTP %d); run weread-cli login to replace your API key", e.API, e.StatusCode)
}

func (e *UpgradeError) Error() string {
	return "official skill upgrade required; update weread-cli before syncing: " + e.Message
}

func New(endpoint, key string) (*Client, error) {
	if endpoint == "" {
		endpoint = GatewayURL
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid gateway URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return nil, errors.New("gateway must use HTTPS (HTTP allowed only on loopback for tests)")
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("run weread-cli login to get and save an API key (https://weread.qq.com/r/weread-skills), or set WEREAD_API_KEY")
	}
	return &Client{endpoint: endpoint, key: key, Interval: 250 * time.Millisecond, http: &http.Client{
		Timeout:       45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) Call(ctx context.Context, api string, params map[string]any) (json.RawMessage, error) {
	if !allowed[api] {
		return nil, fmt.Errorf("unsupported read API %q; use weread-cli api /_list to inspect the official gateway", api)
	}
	body := make(map[string]any, len(params)+2)
	for k, v := range params {
		if k == "api_name" || k == "skill_version" || k == "params" {
			return nil, fmt.Errorf("reserved parameter %q; parameters must be flat", k)
		}
		body[k] = v
	}
	body["api_name"], body["skill_version"] = api, SkillVersion
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		if err := wait(ctx, time.Until(c.last.Add(c.Interval))); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "weread-cli (official-skill/"+SkillVersion+")")
		c.last = time.Now()
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%s: gateway connection failed", api)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if attempt < 2 {
				delay := time.Duration(1<<attempt) * time.Second
				if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 {
					delay = time.Duration(n) * time.Second
				}
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
				if e := wait(ctx, delay); e != nil {
					return nil, e
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				return nil, &AuthenticationError{API: api, StatusCode: resp.StatusCode}
			}
			return nil, fmt.Errorf("%s: gateway HTTP %d", api, resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("%s: incomplete response", api)
		}
		if len(data) > 32<<20 {
			return nil, fmt.Errorf("%s: response exceeds 32 MiB", api)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(data, &envelope); err != nil || envelope == nil {
			return nil, fmt.Errorf("%s: invalid gateway JSON object", api)
		}
		if upgrade, ok := envelope["upgrade_info"]; ok && string(upgrade) != "null" {
			var u struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(upgrade, &u)
			return nil, &UpgradeError{Message: c.clean(u.Message)}
		}
		if code, ok := envelope["errcode"]; ok && string(code) != "0" && string(code) != `"0"` && string(code) != "null" {
			var message string
			_ = json.Unmarshal(envelope["errmsg"], &message)
			return nil, fmt.Errorf("%s: gateway error %s: %s", api, c.clean(string(code)), c.clean(message))
		}
		return json.RawMessage(data), nil
	}
	return nil, errors.New("gateway retry limit exceeded")
}

func (c *Client) clean(s string) string {
	s = strings.ReplaceAll(s, c.key, "[REDACTED]")
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

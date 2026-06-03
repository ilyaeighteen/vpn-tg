package xui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL       string
	APIToken      string
	InboundID     int
	HTTPTimeout   time.Duration
	DefaultClient ClientDefaults
}

type ClientDefaults struct {
	Flow       string
	LimitIP    int
	TotalGB    int64
	ExpireDays int
	Enable     bool
}

type Client struct {
	cfg  Config
	http *http.Client
}

type AddClientResult struct {
	Email string
	UUID  string
}

type apiResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

type inboundClient struct {
	ID         string `json:"id"`
	Flow       string `json:"flow"`
	Email      string `json:"email"`
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	Enable     bool   `json:"enable"`
	TgID       string `json:"tgId"`
	SubID      string `json:"subId"`
	Reset      int    `json:"reset"`
}

func NewClient(cfg Config) *Client {
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) AddClient(ctx context.Context, inboundID int, email string) (AddClientResult, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return AddClientResult{}, errors.New("email is required")
	}
	if inboundID <= 0 {
		inboundID = c.cfg.InboundID
	}

	uuid, err := newUUID()
	if err != nil {
		return AddClientResult{}, err
	}

	client := inboundClient{
		ID:         uuid,
		Flow:       c.cfg.DefaultClient.Flow,
		Email:      email,
		LimitIP:    c.cfg.DefaultClient.LimitIP,
		TotalGB:    c.cfg.DefaultClient.TotalGB,
		ExpiryTime: expiryMillis(c.cfg.DefaultClient.ExpireDays),
		Enable:     c.cfg.DefaultClient.Enable,
		TgID:       "",
		SubID:      randomHex(8),
		Reset:      0,
	}

	settings, err := json.Marshal(map[string][]inboundClient{"clients": []inboundClient{client}})
	if err != nil {
		return AddClientResult{}, err
	}

	form := url.Values{}
	form.Set("id", strconv.Itoa(inboundID))
	form.Set("settings", string(settings))

	resp, err := c.postForm(ctx, "/panel/api/inbounds/addClient", form)
	if err != nil {
		return AddClientResult{}, err
	}
	if !resp.Success {
		return AddClientResult{}, fmt.Errorf("3x-ui add client failed: %s", resp.Msg)
	}

	return AddClientResult{Email: email, UUID: uuid}, nil
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) (apiResponse, error) {
	endpoint := c.cfg.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return apiResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIToken)

	res, err := c.http.Do(req)
	if err != nil {
		return apiResponse{}, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
	if err != nil {
		return apiResponse{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return apiResponse{}, fmt.Errorf("3x-ui returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	contentType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if contentType != "" && contentType != "application/json" && !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return apiResponse{}, fmt.Errorf("3x-ui returned non-json response from %s", path)
	}

	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return apiResponse{}, fmt.Errorf("decode 3x-ui response: %w", err)
	}
	return parsed, nil
}

func expiryMillis(days int) int64 {
	if days <= 0 {
		return 0
	}
	return time.Now().AddDate(0, 0, days).UnixMilli()
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func randomHex(bytesCount int) string {
	data := make([]byte, bytesCount)
	if _, err := rand.Read(data); err != nil {
		return ""
	}
	return hex.EncodeToString(data)
}

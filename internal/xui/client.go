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
	BaseURL             string
	APIToken            string
	InboundID           int
	HTTPTimeout         time.Duration
	SubscriptionBaseURL string
	SubscriptionPath    string
	DefaultClient       ClientDefaults
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

type ClientLinksResult struct {
	Email           string
	SubscriptionURL string
	Links           []string
}

type PanelClient struct {
	Email string
	ID    string
	SubID string
}

type apiResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

type inbound struct {
	ID       int    `json:"id"`
	Settings string `json:"settings"`
}

type inboundSettings struct {
	Clients []inboundClient `json:"clients"`
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

func (c *Client) ListClients(ctx context.Context, inboundID int) ([]PanelClient, error) {
	if inboundID <= 0 {
		inboundID = c.cfg.InboundID
	}

	resp, err := c.get(ctx, "/panel/api/inbounds/get/"+strconv.Itoa(inboundID))
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("3x-ui get inbound failed: %s", resp.Msg)
	}

	var item inbound
	if err := json.Unmarshal(resp.Obj, &item); err != nil {
		return nil, fmt.Errorf("decode inbound: %w", err)
	}

	var settings inboundSettings
	if strings.TrimSpace(item.Settings) != "" {
		if err := json.Unmarshal([]byte(item.Settings), &settings); err != nil {
			return nil, fmt.Errorf("decode inbound settings: %w", err)
		}
	}

	clients := make([]PanelClient, 0, len(settings.Clients))
	for _, client := range settings.Clients {
		clients = append(clients, PanelClient{
			Email: client.Email,
			ID:    client.ID,
			SubID: client.SubID,
		})
	}
	return clients, nil
}

func (c *Client) FindClientByID(ctx context.Context, inboundID int, clientID string) (PanelClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return PanelClient{}, errors.New("client id is required")
	}

	clients, err := c.ListClients(ctx, inboundID)
	if err != nil {
		return PanelClient{}, err
	}
	for _, client := range clients {
		if client.ID == clientID {
			return client, nil
		}
	}
	return PanelClient{}, fmt.Errorf("client not found: %s", clientID)
}

func (c *Client) DeleteClientByEmail(ctx context.Context, inboundID int, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("email is required")
	}
	if inboundID <= 0 {
		inboundID = c.cfg.InboundID
	}

	path := "/panel/api/inbounds/" + strconv.Itoa(inboundID) + "/delClientByEmail/" + url.PathEscape(email)
	resp, err := c.postForm(ctx, path, url.Values{})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("3x-ui delete client failed: %s", resp.Msg)
	}
	return nil
}

func (c *Client) GetClientLinks(ctx context.Context, inboundID int, email string) (ClientLinksResult, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return ClientLinksResult{}, errors.New("email is required")
	}
	if inboundID <= 0 {
		inboundID = c.cfg.InboundID
	}

	path := "/panel/api/inbounds/getClientLinks/" + strconv.Itoa(inboundID) + "/" + url.PathEscape(email)
	resp, err := c.get(ctx, path)
	if err != nil {
		return ClientLinksResult{}, err
	}
	if !resp.Success {
		return ClientLinksResult{}, fmt.Errorf("3x-ui get client links failed: %s", resp.Msg)
	}

	var links []string
	if err := json.Unmarshal(resp.Obj, &links); err != nil {
		return ClientLinksResult{}, fmt.Errorf("decode client links: %w", err)
	}

	subscriptionURL := ""
	if client, err := c.findClientByEmail(ctx, inboundID, email); err == nil {
		subscriptionURL = c.subscriptionURL(client.SubID)
	}

	return ClientLinksResult{Email: email, SubscriptionURL: subscriptionURL, Links: links}, nil
}

func (c *Client) findClientByEmail(ctx context.Context, inboundID int, email string) (PanelClient, error) {
	clients, err := c.ListClients(ctx, inboundID)
	if err != nil {
		return PanelClient{}, err
	}
	for _, client := range clients {
		if client.Email == email {
			return client, nil
		}
	}
	return PanelClient{}, fmt.Errorf("client not found: %s", email)
}

func (c *Client) subscriptionURL(subID string) string {
	subID = strings.TrimSpace(subID)
	if subID == "" || c.cfg.SubscriptionBaseURL == "" {
		return ""
	}

	baseURL := strings.TrimRight(c.cfg.SubscriptionBaseURL, "/")
	path := strings.TrimSpace(c.cfg.SubscriptionPath)
	if path == "" {
		path = "/sub/:subid"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	escapedSubID := url.PathEscape(subID)
	if strings.Contains(path, ":subid") {
		return baseURL + strings.ReplaceAll(path, ":subid", escapedSubID)
	}
	return baseURL + strings.TrimRight(path, "/") + "/" + escapedSubID
}

func (c *Client) get(ctx context.Context, path string) (apiResponse, error) {
	endpoint := c.cfg.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return apiResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIToken)

	return c.do(req, path)
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

	return c.do(req, path)
}

func (c *Client) do(req *http.Request, path string) (apiResponse, error) {
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

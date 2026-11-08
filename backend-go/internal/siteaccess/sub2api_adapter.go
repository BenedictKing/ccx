package siteaccess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 契约来自 Wei-Shaw/sub2api 6b4b37db：用户 /api/v1 接口使用 Bearer JWT。
// 不接管密码、验证码或二次认证；用户可更新 JWT，或仅导入推理 Key。
type sub2APIAdapter struct {
	base       *NewApiAdapter
	groupNames map[string]string
}

func (a *sub2APIAdapter) request(ctx context.Context, method, baseURL, path, jwt string, body any, result any) error {
	var reader io.Reader
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+"/api/v1"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost {
		digest := sha256.Sum256(append([]byte(baseURL+"\x00"+jwt+"\x00"+path), payload...))
		req.Header.Set("Idempotency-Key", fmt.Sprintf("ccx-%x", digest))
	}
	resp, err := a.base.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("sub2api 管理接口连接失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sub2api 管理接口 HTTP %d；请检查登录令牌或使用已有 Key 导入", resp.StatusCode)
	}
	var envelope struct {
		Code    *int            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("sub2api 响应格式不兼容")
	}
	if envelope.Code == nil || *envelope.Code != 0 {
		return fmt.Errorf("sub2api 管理操作未成功，请检查权限或套餐")
	}
	if result != nil {
		return json.Unmarshal(envelope.Data, result)
	}
	return nil
}

func (a *sub2APIAdapter) VerifyWithFallback(ctx context.Context, baseURL, jwt, userID, mode string) (*NewApiUserSelf, string, error) {
	var user struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Status   string `json:"status"`
	}
	if err := a.request(ctx, http.MethodGet, baseURL, "/auth/me", jwt, nil, &user); err != nil {
		return nil, "", err
	}
	if user.ID <= 0 || user.Status != "active" {
		return nil, "", fmt.Errorf("sub2api 用户身份无效或已停用")
	}
	if userID != "" && userID != strconv.Itoa(user.ID) {
		return nil, "", fmt.Errorf("管理令牌属于其他用户，请另建账号")
	}
	return &NewApiUserSelf{ID: user.ID, Username: user.Username}, strconv.Itoa(user.ID), nil
}

func (a *sub2APIAdapter) FetchBalance(ctx context.Context, baseURL, jwt, userID, mode string) (float64, string, error) {
	var user struct {
		Balance float64 `json:"balance"`
	}
	err := a.request(ctx, http.MethodGet, baseURL, "/auth/me", jwt, nil, &user)
	return user.Balance, "USD", err
}

func (a *sub2APIAdapter) FetchGroups(ctx context.Context, baseURL, jwt, userID, mode string) (map[string]float64, error) {
	var groups []struct {
		ID     int     `json:"id"`
		Name   string  `json:"name"`
		Rate   float64 `json:"rate_multiplier"`
		Status string  `json:"status"`
	}
	if err := a.request(ctx, http.MethodGet, baseURL, "/groups/available", jwt, nil, &groups); err != nil {
		return nil, err
	}
	var overrides map[string]float64
	if err := a.request(ctx, http.MethodGet, baseURL, "/groups/rates", jwt, nil, &overrides); err != nil {
		return nil, err
	}
	ratios := map[string]float64{}
	a.groupNames = map[string]string{}
	for _, group := range groups {
		if group.Status != "active" {
			continue
		}
		id := strconv.Itoa(group.ID)
		ratio := group.Rate
		if value, ok := overrides[id]; ok {
			ratio = value
		}
		if !validNewApiGroupRatio(ratio) {
			return nil, fmt.Errorf("sub2api 分组倍率无效")
		}
		ratios[id] = ratio
		a.groupNames[id] = group.Name
	}
	return ratios, nil
}

type sub2APIKey struct {
	ID        int        `json:"id"`
	Key       string     `json:"key"`
	Name      string     `json:"name"`
	GroupID   *int       `json:"group_id"`
	Status    string     `json:"status"`
	Quota     float64    `json:"quota"`
	QuotaUsed float64    `json:"quota_used"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (k sub2APIKey) token() NewApiToken {
	t := NewApiToken{ID: k.ID, Name: k.Name, Key: k.Key, Status: 2}
	if k.Status == "active" {
		t.Status = 1
	}
	if k.GroupID != nil {
		t.Group = strconv.Itoa(*k.GroupID)
	}
	if k.ExpiresAt != nil {
		t.ExpiredTime = k.ExpiresAt.Unix()
	}
	if k.Quota > 0 && k.QuotaUsed >= k.Quota {
		t.Status = 2
	}
	return t
}
func (a *sub2APIAdapter) ListTokens(ctx context.Context, baseURL, jwt, userID, mode string, page, size int) ([]NewApiToken, error) {
	var data struct {
		Items []sub2APIKey `json:"items"`
	}
	err := a.request(ctx, http.MethodGet, baseURL, fmt.Sprintf("/keys?page=%d&page_size=%d", page, size), jwt, nil, &data)
	if err != nil {
		return nil, err
	}
	out := make([]NewApiToken, 0, len(data.Items))
	for _, key := range data.Items {
		out = append(out, key.token())
	}
	return out, nil
}
func (a *sub2APIAdapter) ListAllTokens(ctx context.Context, account SiteAccount, jwt string) ([]NewApiToken, error) {
	return listAllTokens(ctx, a, account, jwt)
}
func (a *sub2APIAdapter) GetTokenKey(ctx context.Context, baseURL, jwt, userID, mode string, id int) (string, error) {
	var key sub2APIKey
	err := a.request(ctx, http.MethodGet, baseURL, fmt.Sprintf("/keys/%d", id), jwt, nil, &key)
	if err != nil {
		return "", err
	}
	if key.Key == "" || IsMaskedNewApiKey(key.Key) {
		return "", fmt.Errorf("sub2api 未返回完整 Key")
	}
	return key.Key, nil
}
func (a *sub2APIAdapter) DeleteToken(ctx context.Context, baseURL, jwt, userID, mode string, id int) error {
	return a.request(ctx, http.MethodDelete, baseURL, fmt.Sprintf("/keys/%d", id), jwt, nil, nil)
}
func (a *sub2APIAdapter) ProvisionKey(ctx context.Context, baseURL, jwt, userID, mode string, opts NewApiProvisionOptions) (int, string, bool, string, error) {
	account := SiteAccount{BaseURL: baseURL, UserID: userID, AuthTokenMode: mode}
	items, err := a.ListAllTokens(ctx, account, jwt)
	if err != nil {
		return 0, "", false, "", err
	}
	for _, key := range items {
		if key.Name == opts.Name && key.Group == opts.Group {
			if key.Status != 1 {
				return 0, "", true, key.Name, fmt.Errorf("同名 Key 已停用，请检查远端状态")
			}
			if key.Key == "" || IsMaskedNewApiKey(key.Key) {
				key.Key, err = a.GetTokenKey(ctx, baseURL, jwt, userID, mode, key.ID)
				if err != nil {
					return 0, "", true, key.Name, err
				}
			}
			return key.ID, key.Key, true, key.Name, nil
		}
	}
	groupID, err := strconv.Atoi(opts.Group)
	if err != nil || groupID <= 0 {
		return 0, "", false, "", fmt.Errorf("sub2api 分组 ID 无效")
	}
	var key sub2APIKey
	err = a.request(ctx, http.MethodPost, baseURL, "/keys", jwt, map[string]any{"name": opts.Name, "group_id": groupID}, &key)
	if err != nil {
		return 0, "", false, "", err
	}
	if key.ID <= 0 || key.Key == "" || key.GroupID == nil || *key.GroupID != groupID {
		return key.ID, "", false, opts.Name, fmt.Errorf("创建后无法确认 Key 归属，请核对站点后台")
	}
	return key.ID, key.Key, false, key.Name, nil
}
func (a *sub2APIAdapter) FetchGroupModels(ctx context.Context, baseURL, jwt, userID, mode, group string) ([]string, error) {
	var plaza struct {
		Groups []struct {
			ID     int `json:"id"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		} `json:"groups"`
	}
	if a.request(ctx, http.MethodGet, baseURL, "/model-plaza", jwt, nil, &plaza) == nil {
		for _, g := range plaza.Groups {
			if strconv.Itoa(g.ID) == group && len(g.Models) > 0 {
				models := []string{}
				for _, m := range g.Models {
					models = append(models, m.Name)
				}
				return models, nil
			}
		}
	}
	keys, err := a.ListAllTokens(ctx, SiteAccount{BaseURL: baseURL, UserID: userID, AuthTokenMode: mode}, jwt)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key.Group != group || key.Status != 1 || key.Key == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key.Key)
		resp, err := a.base.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, ErrGroupModelsUnknown
		}
		var data struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&data); err != nil {
			return nil, ErrGroupModelsUnknown
		}
		models := []string{}
		for _, m := range data.Data {
			models = append(models, m.ID)
		}
		return models, nil
	}
	return nil, ErrGroupModelsUnknown
}

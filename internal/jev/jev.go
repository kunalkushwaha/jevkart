// Package jev is a small client for the TypeSafe System One endpoint.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	Endpoint     = "https://api.typesafe.ai/v1/systemone"
	DefaultModel = "jev-latest"
)

// Question is one typed question. Criteria is a map for `choice` but MUST be a
// slice for `score` — the published schema is wrong about that.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type Request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]string  `json:"legend"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

type Client struct {
	key  string
	http *http.Client
}

func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 30 * time.Second}}
}

// Ask posts the request and reports how long the round trip took.
func (c *Client) Ask(ctx context.Context, req Request) (*Response, time.Duration, error) {
	if req.Model == "" {
		req.Model = DefaultModel
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	hr.Header.Set("Authorization", "Bearer "+c.key)
	hr.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, time.Since(start), err
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, time.Since(start), err
	}
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return nil, elapsed, fmt.Errorf("http %d: %s", resp.StatusCode, clip(buf.String(), 160))
	}
	var out Response
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return nil, elapsed, err
	}
	return &out, elapsed, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

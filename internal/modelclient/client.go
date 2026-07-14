package modelclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ModelResult is one model's independent classification result.
type ModelResult struct {
	Model                 string   `json:"model"`
	ID                    string   `json:"id"`
	SexualHarmProbability float64  `json:"sexual_harm_probability"`
	Action                string   `json:"action"`
	SemanticGate          float64  `json:"semantic_gate"`
	RuleHits              []string `json:"rule_hits"`
	PassThreshold         float64  `json:"pass_threshold"`
	BlockThreshold        float64  `json:"block_threshold"`
	LatencyMilliseconds   float64  `json:"latency_ms"`
}

// Prediction is the combined response returned by the local Python service.
type Prediction struct {
	OK                  bool          `json:"ok"`
	RequestID           string        `json:"request_id"`
	Device              string        `json:"device"`
	Parallel            bool          `json:"parallel"`
	Models              []ModelResult `json:"models"`
	CombinedAction      string        `json:"combined_action"`
	LatencyMilliseconds float64       `json:"latency_ms"`
}

type healthResponse struct {
	OK       bool     `json:"ok"`
	Device   string   `json:"device"`
	Models   []string `json:"models"`
	Parallel bool     `json:"parallel"`
}

// Client communicates only with the loopback model service. It never logs text.
type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	transport := &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

func (c *Client) Check(ctx context.Context, text string) (*Prediction, error) {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/predict", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model service request failed: %w", err)
	}
	defer resp.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read model service response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model service returned %s", resp.Status)
	}
	var result Prediction
	if err := json.Unmarshal(limited, &result); err != nil {
		return nil, fmt.Errorf("decode model service response: %w", err)
	}
	if !result.OK || len(result.Models) != 2 {
		return nil, fmt.Errorf("model service returned incomplete prediction")
	}
	return &result, nil
}

func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("model service health returned %s", resp.Status)
	}
	var health healthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&health); err != nil {
		return err
	}
	if !health.OK || len(health.Models) != 2 || !health.Parallel {
		return fmt.Errorf("model service is not fully ready")
	}
	return nil
}

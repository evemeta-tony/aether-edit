// services/tenancy/quotaclient/quotaclient.go

// Package quotaclient implements the frozen contracts.QuotaChecker
// interface over the tenancy service's internal HTTP quota API. FT-2
// and FT-3 construct it at deploy time (in place of ConfigQuota) so
// quota admission consults the metered rollups. The client is
// fail-closed by construction: transport failures and non-200
// responses surface as errors, and the frozen V-5 posture at the call
// sites denies on error.
package quotaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// Client calls the tenancy internal quota endpoints.
type Client struct {
	baseURL       string
	internalToken string
	hc            *http.Client
}

var _ contracts.QuotaChecker = (*Client)(nil)

// New builds a Client. baseURL is the tenancy service root (for
// example http://127.0.0.1:5401); internalToken must match the
// service's TENANCY_INTERNAL_TOKEN.
func New(baseURL, internalToken string, timeout time.Duration) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("quotaclient: baseURL is required")
	}
	if internalToken == "" {
		return nil, fmt.Errorf("quotaclient: internalToken is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		internalToken: internalToken,
		hc:            &http.Client{Timeout: timeout},
	}, nil
}

type decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// post sends one quota check and decodes the decision.
func (c *Client) post(ctx context.Context, path string, body any) (contracts.QuotaDecision, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return contracts.QuotaDecision{}, fmt.Errorf("quotaclient: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return contracts.QuotaDecision{}, fmt.Errorf("quotaclient: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", c.internalToken)
	resp, err := c.hc.Do(req)
	if err != nil {
		return contracts.QuotaDecision{}, fmt.Errorf("quotaclient: %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return contracts.QuotaDecision{}, fmt.Errorf("quotaclient: %s: read: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return contracts.QuotaDecision{}, fmt.Errorf("quotaclient: %s returned %d", path, resp.StatusCode)
	}
	var d decision
	if err := json.Unmarshal(raw, &d); err != nil {
		return contracts.QuotaDecision{}, fmt.Errorf("quotaclient: %s: decode: %w", path, err)
	}
	return contracts.QuotaDecision{Allowed: d.Allowed, Reason: d.Reason}, nil
}

// CheckUploadSession implements contracts.QuotaChecker.
func (c *Client) CheckUploadSession(ctx context.Context, workspaceID string, sizeHintBytes int64) (contracts.QuotaDecision, error) {
	return c.post(ctx, "/internal/v1/quota/check-upload-session", map[string]any{
		"workspaceId":   workspaceID,
		"sizeHintBytes": sizeHintBytes,
	})
}

// CheckJobAdmission implements contracts.QuotaChecker.
func (c *Client) CheckJobAdmission(ctx context.Context, workspaceID string) (contracts.QuotaDecision, error) {
	return c.post(ctx, "/internal/v1/quota/check-job-admission", map[string]any{
		"workspaceId": workspaceID,
	})
}

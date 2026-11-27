package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// NovuBridgeTimeout is how long a novu_message node waits for the bridge, as long as a webhook
// node waits for its endpoint.
const NovuBridgeTimeout = 30 * time.Second

// novuBridgeListTimeout bounds the workflow list call the console makes while an author edits a
// flow: the bridge answers from its own cache or from one Novu call.
const novuBridgeListTimeout = 10 * time.Second

// novuBridgeListLimit caps the workflow list read from the bridge.
const novuBridgeListLimit = 1 << 20

// NovuBridge is the client of the CRM bridge that novu_message nodes send through: the address
// and the secret are server settings (BRG_NOVU_BRIDGE_URL, BRG_NOVU_BRIDGE_SECRET), never read
// from a flow.
//
// Its HTTP client is not the SSRF-guarded one of webhook nodes: a webhook node posts wherever a
// member typed, while this address is set by the operator, and is usually on the private network
// of the deployment (http://bridge:8090). The app wires a client that does not follow redirects,
// so the bridge cannot send the secret elsewhere.
type NovuBridge struct {
	baseURL    string
	secret     string
	httpClient *http.Client
}

// NewNovuBridge creates a client for the bridge at baseURL. A nil httpClient gets a plain client
// with NovuBridgeTimeout that does not follow redirects.
func NewNovuBridge(baseURL, secret string, httpClient *http.Client) *NovuBridge {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: NovuBridgeTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &NovuBridge{
		baseURL:    strings.TrimSpace(baseURL),
		secret:     secret,
		httpClient: httpClient,
	}
}

// Configured reports whether both the address and the secret are set. Safe on a nil bridge.
func (b *NovuBridge) Configured() bool {
	return b != nil && b.baseURL != "" && b.secret != ""
}

// endpoint returns the bridge address with path appended as separate segments.
func (b *NovuBridge) endpoint(path ...string) (*url.URL, error) {
	u, err := url.Parse(b.baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("BRG_NOVU_BRIDGE_URL must be an http:// or https:// address")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.JoinPath(path...), nil
}

// messageURL builds the address of a send through workflowID. The title and the body travel as
// query parameters, escaped; the one that is empty is left out.
func (b *NovuBridge) messageURL(workflowID, title, body string) (string, error) {
	u, err := b.endpoint("notifuse", "novu", workflowID)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	if title != "" {
		query.Set("title", title)
	}
	if body != "" {
		query.Set("body", body)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// novuBridgeResponse is what the bridge answered: the status and the first 10 KB of the body.
type novuBridgeResponse struct {
	StatusCode int
	Body       []byte
}

// send posts payload to the bridge's endpoint for workflowID and returns whatever it answered,
// whatever the status: the caller decides what each one means.
func (b *NovuBridge) send(ctx context.Context, workflowID, title, body string, payload []byte) (*novuBridgeResponse, error) {
	target, err := b.messageURL(workflowID, title, body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create novu bridge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.secret)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("novu bridge request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read novu bridge response: %w", err)
	}
	return &novuBridgeResponse{StatusCode: resp.StatusCode, Body: respBody}, nil
}

// ListWorkflows asks the bridge for the active Novu workflows (GET /novu/workflows). Every
// failure is a *domain.NovuBridgeUnavailableError whose message is safe to show.
func (b *NovuBridge) ListWorkflows(ctx context.Context) ([]domain.NovuWorkflow, error) {
	if !b.Configured() {
		return nil, &domain.NovuBridgeUnavailableError{Message: "The Novu bridge is not set up on this server: BRG_NOVU_BRIDGE_URL and BRG_NOVU_BRIDGE_SECRET are required"}
	}
	target, err := b.endpoint("novu", "workflows")
	if err != nil {
		return nil, &domain.NovuBridgeUnavailableError{Message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, novuBridgeListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, &domain.NovuBridgeUnavailableError{Message: "The Novu bridge address is not valid"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.secret)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, &domain.NovuBridgeUnavailableError{Message: "Could not reach the Novu bridge"}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &domain.NovuBridgeUnavailableError{Message: "The Novu bridge refused the configured secret (BRG_NOVU_BRIDGE_SECRET)"}
	case resp.StatusCode != http.StatusOK:
		return nil, &domain.NovuBridgeUnavailableError{Message: fmt.Sprintf("The Novu bridge answered HTTP %d", resp.StatusCode)}
	}

	var decoded struct {
		Workflows []domain.NovuWorkflow `json:"workflows"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, novuBridgeListLimit)).Decode(&decoded); err != nil {
		return nil, &domain.NovuBridgeUnavailableError{Message: "The Novu bridge answered something that is not a workflow list"}
	}

	// A workflow whose id a node could not carry is of no use to the picker.
	workflows := make([]domain.NovuWorkflow, 0, len(decoded.Workflows))
	for _, w := range decoded.Workflows {
		if !domain.IsValidNovuWorkflowID(w.ID) {
			continue
		}
		if w.Name == "" {
			w.Name = w.ID
		}
		workflows = append(workflows, w)
	}
	return workflows, nil
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Outcomes a novu_message node records on its node execution.
const (
	novuOutcomeSent       = "sent"
	novuOutcomeNotOptedIn = "not_opted_in"
)

// errNovuBridgeNotConfigured fails a novu_message node on a server without the bridge settings.
var errNovuBridgeNotConfigured = errors.New("novu_message node cannot run: BRG_NOVU_BRIDGE_URL and BRG_NOVU_BRIDGE_SECRET are not set on this server")

// NovuMessageNodeExecutor executes novu_message nodes: an in-app inbox message and web push,
// sent by the CRM bridge through one of its Novu workflows.
//
// The request carries the same JSON body a webhook node sends (buildWebhookPayload), where the
// bridge reads the contact's external_id, and the node's title and body as query parameters.
// The bridge answers, and the node takes each answer as a webhook node takes it, except 409:
//
//   - 2xx: sent, on to the next node.
//   - 409: the player has not opted in and nothing was sent. That is the consent decision
//     working, not a failure, so the contact goes on to the next node and the outcome is
//     recorded.
//   - 5xx (the consent lookup or Novu is down): an error, retried on the executor's backoff.
//   - 4xx (bad secret, bad input) and anything else: an error.
type NovuMessageNodeExecutor struct {
	bridge *NovuBridge
	logger logger.Logger
	// timeout bounds each call; zero means NovuBridgeTimeout. Tests set it shorter.
	timeout time.Duration
}

// NewNovuMessageNodeExecutor creates a novu_message node executor that sends through bridge.
// A nil or unconfigured bridge is accepted: the node then fails with a clear error when it runs,
// so a deployment that does not use the bridge starts normally.
func NewNovuMessageNodeExecutor(log logger.Logger, bridge *NovuBridge) *NovuMessageNodeExecutor {
	return &NovuMessageNodeExecutor{
		bridge: bridge,
		logger: log,
	}
}

// NodeType returns the node type this executor handles
func (e *NovuMessageNodeExecutor) NodeType() domain.NodeType {
	return domain.NodeTypeNovuMessage
}

// Execute processes a novu_message node
func (e *NovuMessageNodeExecutor) Execute(ctx context.Context, params NodeExecutionParams) (*NodeExecutionResult, error) {
	// 1. Parse config
	config, err := parseNovuMessageNodeConfig(params.Node.Config)
	if err != nil {
		return nil, fmt.Errorf("invalid novu_message node config: %w", err)
	}

	// 2. The bridge is a server setting: without it nothing can be sent.
	if !e.bridge.Configured() {
		return nil, errNovuBridgeNotConfigured
	}

	// 3. Same body as a webhook node
	payloadBytes, err := json.Marshal(buildWebhookPayload(params.ContactData, params.Automation, params.Node.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal novu_message payload: %w", err)
	}

	// 4. Send, bounded by the node's own timeout whatever the client's
	timeout := e.timeout
	if timeout <= 0 {
		timeout = NovuBridgeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := e.bridge.send(ctx, config.WorkflowID, config.Title, config.Body, payloadBytes)
	if err != nil {
		return nil, err
	}

	// 5. Handle response status
	logFields := map[string]interface{}{
		"workspace_id":  params.WorkspaceID,
		"automation_id": params.Automation.ID,
		"workflow_id":   config.WorkflowID,
		"status_code":   resp.StatusCode,
	}
	var outcome string
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		outcome = novuOutcomeSent
		e.logger.WithFields(logFields).Info("Novu message node executed successfully")
	case resp.StatusCode == http.StatusConflict:
		outcome = novuOutcomeNotOptedIn
		e.logger.WithFields(logFields).Info("Novu message node sent nothing: the player has not opted in")
	case resp.StatusCode >= 500:
		// 5xx - consent lookup or Novu down, return error to trigger retry via existing backoff
		return nil, fmt.Errorf("novu bridge returned server error: %d %s", resp.StatusCode, string(resp.Body))
	case resp.StatusCode >= 400:
		// 4xx - bad secret or bad input. Retried on the executor's backoff like any other
		// node error until the journey's retries run out.
		return nil, fmt.Errorf("novu bridge returned client error: %d %s", resp.StatusCode, string(resp.Body))
	default:
		// 1xx, and a 3xx the client did not follow
		return nil, fmt.Errorf("novu bridge returned unexpected status: %d %s", resp.StatusCode, string(resp.Body))
	}

	// 6. Parse JSON response for the node execution log
	var responseData map[string]interface{}
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &responseData); err != nil {
			responseData = map[string]interface{}{
				"raw": string(resp.Body),
			}
		}
	}

	return &NodeExecutionResult{
		NextNodeID: params.Node.NextNodeID,
		Status:     domain.ContactAutomationStatusActive,
		Output: buildNodeOutput(domain.NodeTypeNovuMessage, map[string]interface{}{
			"workflow_id": config.WorkflowID,
			"outcome":     outcome,
			"status_code": resp.StatusCode,
			"response":    responseData,
		}),
	}, nil
}

// parseNovuMessageNodeConfig parses novu_message node configuration from map
func parseNovuMessageNodeConfig(config map[string]interface{}) (*domain.NovuMessageNodeConfig, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	var c domain.NovuMessageNodeConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return &c, nil
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/safehttpclient"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testNovuSecret = "bridge-secret-value-1234"

// capturedNovuRequest is what the stand-in bridge received.
type capturedNovuRequest struct {
	method        string
	path          string
	rawQuery      string
	query         map[string][]string
	authorization string
	contentType   string
	body          []byte
}

// novuBridgeServer starts a stand-in for the CRM bridge that answers status with body and
// records what it receives. It stands in for the bridge in unit tests only.
func novuBridgeServer(t *testing.T, status int, body string) (*httptest.Server, *capturedNovuRequest, *atomic.Int32) {
	t.Helper()
	captured := &capturedNovuRequest{}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		*captured = capturedNovuRequest{
			method:        r.Method,
			path:          r.URL.Path,
			rawQuery:      r.URL.RawQuery,
			query:         r.URL.Query(),
			authorization: r.Header.Get("Authorization"),
			contentType:   r.Header.Get("Content-Type"),
			body:          raw,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, captured, &calls
}

func novuNodeParams(config map[string]interface{}) NodeExecutionParams {
	return NodeExecutionParams{
		WorkspaceID: "ws1",
		Node: &domain.AutomationNode{
			ID:         "novu-node-1",
			Type:       domain.NodeTypeNovuMessage,
			NextNodeID: strPtr("next_node"),
			Config:     config,
		},
		Contact: &domain.ContactAutomation{ID: "ca1", ContactEmail: "player@example.com"},
		ContactData: &domain.Contact{
			Email:      "player@example.com",
			ExternalID: &domain.NullableString{String: "casino-player-4711", IsNull: false},
		},
		Automation: &domain.Automation{ID: "auto1", Name: "Welcome flow"},
	}
}

func newNovuExecutor(t *testing.T, serverURL string) *NovuMessageNodeExecutor {
	t.Helper()
	ctrl := gomock.NewController(t)
	return NewNovuMessageNodeExecutor(setupMockLoggerForNodeExecutor(ctrl), NewNovuBridge(serverURL, testNovuSecret, nil))
}

func TestNovuMessageNodeExecutor_NodeType(t *testing.T) {
	assert.Equal(t, domain.NodeTypeNovuMessage, newNovuExecutor(t, "http://bridge:8090").NodeType())
}

func TestNovuMessageNodeExecutor_Execute_Sent(t *testing.T) {
	server, got, calls := novuBridgeServer(t, http.StatusOK, `{"ok":true,"transaction_id":"tx-1"}`)
	executor := newNovuExecutor(t, server.URL)

	result, err := executor.Execute(context.Background(), novuNodeParams(map[string]interface{}{
		"workflow_id": "crm-message",
		"title":       "Welcome",
		"body":        "Your bonus is ready",
	}))
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/notifuse/novu/crm-message", got.path)
	assert.Equal(t, "Bearer "+testNovuSecret, got.authorization)
	assert.Equal(t, "application/json", got.contentType)
	assert.Equal(t, []string{"Welcome"}, got.query["title"])
	assert.Equal(t, []string{"Your bonus is ready"}, got.query["body"])

	require.NotNil(t, result.NextNodeID)
	assert.Equal(t, "next_node", *result.NextNodeID)
	assert.Equal(t, domain.ContactAutomationStatusActive, result.Status)
	assert.Equal(t, "novu_message", result.Output["node_type"])
	assert.Equal(t, "crm-message", result.Output["workflow_id"])
	assert.Equal(t, "sent", result.Output["outcome"])
	assert.Equal(t, 200, result.Output["status_code"])
	assert.Equal(t, map[string]interface{}{"ok": true, "transaction_id": "tx-1"}, result.Output["response"])
}

func TestNovuMessageNodeExecutor_Execute_LastNodeCompletes(t *testing.T) {
	server, _, _ := novuBridgeServer(t, http.StatusOK, `{}`)
	params := novuNodeParams(map[string]interface{}{"workflow_id": "crm-welcome-inapp"})
	params.Node.NextNodeID = nil

	result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), params)
	require.NoError(t, err)
	assert.Nil(t, result.NextNodeID)
}

// The bridge reads the contact's external_id from the body, so the body has to stay exactly what a
// webhook node posts.
func TestNovuMessageNodeExecutor_Execute_BodyEqualsWebhookPayload(t *testing.T) {
	novuServer, novuGot, _ := novuBridgeServer(t, http.StatusOK, `{}`)
	hookServer, hookGot, _ := novuBridgeServer(t, http.StatusOK, `{}`)

	novuParams := novuNodeParams(map[string]interface{}{"workflow_id": "crm-message", "title": "t", "body": "b"})
	_, err := newNovuExecutor(t, novuServer.URL).Execute(context.Background(), novuParams)
	require.NoError(t, err)

	hookParams := novuNodeParams(map[string]interface{}{"url": hookServer.URL})
	hookParams.Node.Type = domain.NodeTypeWebhook
	ctrl := gomock.NewController(t)
	_, err = NewWebhookNodeExecutor(setupMockLoggerForNodeExecutor(ctrl), permissiveWebhookClient()).Execute(context.Background(), hookParams)
	require.NoError(t, err)

	decode := func(raw []byte) map[string]interface{} {
		var payload map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &payload))
		// The one field that moves: the time the body was built.
		ts, ok := payload["timestamp"].(string)
		require.True(t, ok, "timestamp missing")
		_, err := time.Parse(time.RFC3339, ts)
		require.NoError(t, err)
		delete(payload, "timestamp")
		return payload
	}
	novuBody, hookBody := decode(novuGot.body), decode(hookGot.body)

	assert.Equal(t, hookBody, novuBody)
	assert.Equal(t, "novu-node-1", novuBody["node_id"])
	assert.Equal(t, "player@example.com", novuBody["email"])
	assert.Equal(t, "auto1", novuBody["automation_id"])
	contact, ok := novuBody["contact"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "casino-player-4711", contact["external_id"])
}

// 409 is the consent decision working: nothing was sent, and the contact carries on.
func TestNovuMessageNodeExecutor_Execute_NotOptedInContinues(t *testing.T) {
	server, _, calls := novuBridgeServer(t, http.StatusConflict, `{"detail":"player is not opted in; nothing sent"}`)

	result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{
		"workflow_id": "crm-message", "title": "Hi",
	}))

	require.NoError(t, err, "a refused consent is not a failed node")
	require.NotNil(t, result)
	assert.Equal(t, int32(1), calls.Load(), "no retry")
	require.NotNil(t, result.NextNodeID)
	assert.Equal(t, "next_node", *result.NextNodeID)
	assert.Equal(t, domain.ContactAutomationStatusActive, result.Status)
	assert.Equal(t, "not_opted_in", result.Output["outcome"])
	assert.Equal(t, 409, result.Output["status_code"])
	assert.Equal(t, map[string]interface{}{"detail": "player is not opted in; nothing sent"}, result.Output["response"])
}

func TestNovuMessageNodeExecutor_Execute_NotOptedInWithPlainBody(t *testing.T) {
	server, _, _ := novuBridgeServer(t, http.StatusConflict, `player is not opted in; nothing sent`)

	result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

	require.NoError(t, err)
	assert.Equal(t, "not_opted_in", result.Output["outcome"])
	assert.Equal(t, map[string]interface{}{"raw": "player is not opted in; nothing sent"}, result.Output["response"])
}

// 503 (consent lookup or Novu down) is transient: an error, so the executor retries it on its
// backoff, as it does a webhook's 5xx.
func TestNovuMessageNodeExecutor_Execute_ServerErrorFails(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		server, _, _ := novuBridgeServer(t, status, `{"detail":"consent lookup failed"}`)

		result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

		require.Error(t, err, "status %d", status)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "novu bridge returned server error")
		assert.Contains(t, err.Error(), "consent lookup failed")
	}
}

func TestNovuMessageNodeExecutor_Execute_ClientErrorFails(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound} {
		server, _, _ := novuBridgeServer(t, status, `{"detail":"nope"}`)

		result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

		require.Error(t, err, "status %d", status)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "novu bridge returned client error")
		assert.NotContains(t, err.Error(), testNovuSecret)
	}
}

// The bridge is not followed anywhere else: the secret stays with the address the operator named.
func TestNovuMessageNodeExecutor_Execute_DoesNotFollowRedirects(t *testing.T) {
	elsewhere, elsewhereGot, elsewhereCalls := novuBridgeServer(t, http.StatusOK, `{}`)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)

	result, err := newNovuExecutor(t, redirecting.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unexpected status: 307")
	assert.Equal(t, int32(0), elsewhereCalls.Load())
	assert.Empty(t, elsewhereGot.authorization)
}

func TestNovuMessageNodeExecutor_Execute_NotConfigured(t *testing.T) {
	cases := []struct {
		name   string
		bridge *NovuBridge
	}{
		{"no bridge", nil},
		{"both unset", NewNovuBridge("", "", nil)},
		{"url unset", NewNovuBridge("", testNovuSecret, nil)},
		{"secret unset", NewNovuBridge("http://bridge:8090", "", nil)},
		{"url blank", NewNovuBridge("   ", testNovuSecret, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			executor := NewNovuMessageNodeExecutor(setupMockLoggerForNodeExecutor(ctrl), tc.bridge)

			result, err := executor.Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, errNovuBridgeNotConfigured)
			assert.Contains(t, err.Error(), "BRG_NOVU_BRIDGE_URL")
			assert.Contains(t, err.Error(), "BRG_NOVU_BRIDGE_SECRET")
		})
	}
}

func TestNovuMessageNodeExecutor_Execute_BridgeURLMustBeHTTP(t *testing.T) {
	for _, base := range []string{"bridge:8090", "ftp://bridge/", "file:///etc/passwd", "http://"} {
		ctrl := gomock.NewController(t)
		executor := NewNovuMessageNodeExecutor(setupMockLoggerForNodeExecutor(ctrl), NewNovuBridge(base, testNovuSecret, nil))

		_, err := executor.Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

		require.Error(t, err, base)
		assert.Contains(t, err.Error(), "BRG_NOVU_BRIDGE_URL must be an http:// or https:// address", base)
	}
}

func TestNovuMessageNodeExecutor_Execute_InvalidConfigSendsNothing(t *testing.T) {
	server, _, calls := novuBridgeServer(t, http.StatusOK, `{}`)
	configs := map[string]map[string]interface{}{
		"no workflow":      {},
		"bad workflow":     {"workflow_id": "CRM Message"},
		"path traversal":   {"workflow_id": "../admin"},
		"title too long":   {"workflow_id": "crm-message", "title": strings.Repeat("t", 201)},
		"body too long":    {"workflow_id": "crm-message", "body": strings.Repeat("b", 1001)},
		"workflow not str": {"workflow_id": 42},
	}
	for name, config := range configs {
		t.Run(name, func(t *testing.T) {
			result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(config))
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), "invalid novu_message node config")
		})
	}
	assert.Equal(t, int32(0), calls.Load())
}

func TestNovuMessageNodeExecutor_Execute_QueryEscaping(t *testing.T) {
	title := `Big win & "free" spins = 100% #1? ünï 🎰`
	body := "Line one\nLine two + three; ?x=1&y=2#frag /slash\\ %41"
	server, got, _ := novuBridgeServer(t, http.StatusOK, `{}`)

	_, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{
		"workflow_id": "crm-message", "title": title, "body": body,
	}))
	require.NoError(t, err)

	// Whatever the text holds, the bridge reads back exactly what was typed, and nothing of it
	// became a parameter, a fragment or a path of its own.
	assert.Equal(t, "/notifuse/novu/crm-message", got.path)
	assert.Equal(t, []string{title}, got.query["title"])
	assert.Equal(t, []string{body}, got.query["body"])
	assert.Len(t, got.query, 2)
	assert.NotContains(t, got.rawQuery, " ")
	assert.NotContains(t, got.rawQuery, "#")
	assert.NotContains(t, got.rawQuery, "\n")
	assert.Equal(t, 2, strings.Count(got.rawQuery, "&")+1, "exactly two parameters")
}

func TestNovuMessageNodeExecutor_Execute_EmptyTextLeavesQueryOut(t *testing.T) {
	server, got, _ := novuBridgeServer(t, http.StatusOK, `{}`)

	_, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-welcome-inapp"}))
	require.NoError(t, err)

	assert.Equal(t, "/notifuse/novu/crm-welcome-inapp", got.path)
	assert.Empty(t, got.rawQuery)

	// Only the one that is set is sent.
	_, err = newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message", "body": "Only a body"}))
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"body": {"Only a body"}}, got.query)
}

func TestNovuMessageNodeExecutor_Execute_BaseURLWithPath(t *testing.T) {
	server, got, _ := novuBridgeServer(t, http.StatusOK, `{}`)

	for _, base := range []string{server.URL + "/api/", server.URL + "/api"} {
		_, err := newNovuExecutor(t, base).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))
		require.NoError(t, err)
		assert.Equal(t, "/api/notifuse/novu/crm-message", got.path, base)
	}

	// A query or fragment in the setting does not leak into the request.
	_, err := newNovuExecutor(t, server.URL+"/?token=x#frag").Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))
	require.NoError(t, err)
	assert.Empty(t, got.rawQuery)
}

// The bridge address is an operator setting on the private network, which the SSRF-guarded client
// of webhook nodes refuses: the node must still reach it.
func TestNovuMessageNodeExecutor_Execute_ReachesLoopbackBridge(t *testing.T) {
	server, _, calls := novuBridgeServer(t, http.StatusOK, `{}`)
	params := novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"})

	// The webhook client with its guard on cannot connect to this address.
	guarded := safehttpclient.New()
	ctrl := gomock.NewController(t)
	hookParams := novuNodeParams(map[string]interface{}{"url": server.URL})
	_, hookErr := NewWebhookNodeExecutor(setupMockLoggerForNodeExecutor(ctrl), guarded).Execute(context.Background(), hookParams)
	require.Error(t, hookErr)
	require.True(t, errors.Is(hookErr, safehttpclient.ErrPrivateIP))
	require.Equal(t, int32(0), calls.Load())

	// The novu_message node can.
	_, err := newNovuExecutor(t, server.URL).Execute(context.Background(), params)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestNovuMessageNodeExecutor_Execute_ConnectionRefused(t *testing.T) {
	server, _, _ := novuBridgeServer(t, http.StatusOK, `{}`)
	closedURL := server.URL
	server.Close()

	result, err := newNovuExecutor(t, closedURL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "novu bridge request failed")
	assert.NotContains(t, err.Error(), testNovuSecret)
}

func TestNovuMessageNodeExecutor_Execute_Timeout(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })

	executor := newNovuExecutor(t, slow.URL)
	executor.timeout = 50 * time.Millisecond

	start := time.Now()
	_, err := executor.Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "novu bridge request failed")
	assert.Less(t, time.Since(start), 5*time.Second)
}

// Nothing the node writes to its execution output or its errors carries the bridge secret.
func TestNovuMessageNodeExecutor_Execute_SecretStaysOutOfOutput(t *testing.T) {
	server, _, _ := novuBridgeServer(t, http.StatusOK, `{"ok":true}`)

	result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{
		"workflow_id": "crm-message", "title": "Hi",
	}))
	require.NoError(t, err)

	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), testNovuSecret)
	assert.NotContains(t, string(encoded), server.URL)
}

func TestParseNovuMessageNodeConfig(t *testing.T) {
	c, err := parseNovuMessageNodeConfig(map[string]interface{}{
		"description": "Day 1", "workflow_id": "crm-message", "title": "Hi", "body": "There",
	})
	require.NoError(t, err)
	assert.Equal(t, "crm-message", c.WorkflowID)
	assert.Equal(t, "Hi", c.Title)
	assert.Equal(t, "There", c.Body)
	assert.Equal(t, "Day 1", c.Description)

	_, err = parseNovuMessageNodeConfig(map[string]interface{}{"title": "Hi"})
	assert.EqualError(t, err, "workflow_id is required")
}

// The executor is registered for the node type, wired with the bridge it was given.
func TestNewAutomationExecutor_RegistersNovuMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	bridge := NewNovuBridge("http://bridge:8090", testNovuSecret, nil)

	executor := NewAutomationExecutor(nil, nil, nil, nil, nil, nil, nil, nil,
		setupMockLoggerForNodeExecutor(ctrl), "https://api.example.com", permissiveWebhookClient(), bridge)

	registered, ok := executor.nodeExecutors[domain.NodeTypeNovuMessage].(*NovuMessageNodeExecutor)
	require.True(t, ok, "novu_message has no executor")
	assert.Same(t, bridge, registered.bridge)
}

// The bridge secret never travels on in what the bridge sends back: not in a retry error, not in
// the stored node output, not in the workflow names shown to the browser.
func TestNovuMessageNodeExecutor_Execute_SecretEchoedByBridgeIsRedacted(t *testing.T) {
	echo := `{"detail":"bad auth header Bearer ` + testNovuSecret + `"}`

	server, _, _ := novuBridgeServer(t, http.StatusInternalServerError, echo)
	_, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testNovuSecret)
	assert.Contains(t, err.Error(), "[redacted]")

	for _, status := range []int{http.StatusOK, http.StatusConflict} {
		server, _, _ := novuBridgeServer(t, status, echo)
		result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))
		require.NoError(t, err)
		out, _ := json.Marshal(result.Output)
		assert.NotContains(t, string(out), testNovuSecret, "status %d", status)
	}

	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflows":[{"id":"crm-message","name":"leak ` + testNovuSecret + `","uses_text":true}]}`))
	}))
	defer list.Close()
	flows, err := NewNovuBridge(list.URL, testNovuSecret, nil).ListWorkflows(context.Background())
	require.NoError(t, err)
	require.Len(t, flows, 1)
	assert.NotContains(t, flows[0].Name, testNovuSecret)
}

// A 409 whose body is cut short is still the consent decision: the contact moves on, no retry.
func TestNovuMessageNodeExecutor_Execute_NotOptedInWithTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "500")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"player is`))
	}))
	defer server.Close()

	result, err := newNovuExecutor(t, server.URL).Execute(context.Background(), novuNodeParams(map[string]interface{}{"workflow_id": "crm-message"}))

	require.NoError(t, err)
	assert.Equal(t, "not_opted_in", result.Output["outcome"])
	require.NotNil(t, result.NextNodeID)
}

// A proxy from the environment never sees the bridge secret.
func TestNovuBridgeTransport_IgnoresEnvironmentProxy(t *testing.T) {
	assert.Nil(t, NovuBridgeTransport().Proxy)
	bridge := NewNovuBridge("http://bridge:8090", testNovuSecret, nil)
	tr, ok := bridge.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "the default client uses NovuBridgeTransport")
	assert.Nil(t, tr.Proxy)
}

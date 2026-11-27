package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeType_IsValid_NovuMessage(t *testing.T) {
	assert.True(t, NodeTypeNovuMessage.IsValid())
	assert.Equal(t, "novu_message", string(NodeTypeNovuMessage))
}

func TestIsValidNovuWorkflowID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"crm-message", true},
		{"a", true},
		{"0", true},
		{"crm-welcome-inapp", true},
		{"a" + strings.Repeat("b", 63), true},  // 64 characters
		{"a" + strings.Repeat("b", 64), false}, // 65 characters
		{"", false},
		{"-crm", false},
		{"CRM-message", false},
		{"crm_message", false},
		{"crm message", false},
		{"crm/message", false},
		{"crm..message", false},
		{"crm-message\n", false},
		{"crm-méssage", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.id), func(t *testing.T) {
			assert.Equal(t, tt.want, IsValidNovuWorkflowID(tt.id))
		})
	}
}

func TestNovuMessageNodeConfig_Validate(t *testing.T) {
	t.Run("workflow id alone is enough", func(t *testing.T) {
		require.NoError(t, NovuMessageNodeConfig{WorkflowID: "crm-welcome-inapp"}.Validate())
	})

	t.Run("workflow id is required", func(t *testing.T) {
		assert.EqualError(t, NovuMessageNodeConfig{Title: "Hi", Body: "There"}.Validate(), "workflow_id is required")
	})

	t.Run("workflow id format", func(t *testing.T) {
		for _, id := range []string{"CRM", "-crm", "crm message", "crm/../x", strings.Repeat("a", 65)} {
			err := NovuMessageNodeConfig{WorkflowID: id}.Validate()
			require.Error(t, err, id)
			assert.Contains(t, err.Error(), "invalid workflow_id", id)
		}
	})

	t.Run("title and body limits are inclusive", func(t *testing.T) {
		require.NoError(t, NovuMessageNodeConfig{
			WorkflowID: "crm-message",
			Title:      strings.Repeat("t", MaxNovuMessageTitleLength),
			Body:       strings.Repeat("b", MaxNovuMessageBodyLength),
		}.Validate())

		assert.EqualError(t, NovuMessageNodeConfig{
			WorkflowID: "crm-message",
			Title:      strings.Repeat("t", MaxNovuMessageTitleLength+1),
		}.Validate(), "title must be at most 200 characters")
		assert.EqualError(t, NovuMessageNodeConfig{
			WorkflowID: "crm-message",
			Body:       strings.Repeat("b", MaxNovuMessageBodyLength+1),
		}.Validate(), "body must be at most 1000 characters")
	})

	t.Run("limits count characters, not bytes", func(t *testing.T) {
		// 200 four-byte characters are 800 bytes: within the limit the bridge applies.
		require.NoError(t, NovuMessageNodeConfig{
			WorkflowID: "crm-message",
			Title:      strings.Repeat("🎰", MaxNovuMessageTitleLength),
		}.Validate())
		assert.Error(t, NovuMessageNodeConfig{
			WorkflowID: "crm-message",
			Title:      strings.Repeat("🎰", MaxNovuMessageTitleLength+1),
		}.Validate())
	})

	t.Run("limits are pinned to what the bridge accepts", func(t *testing.T) {
		assert.Equal(t, 200, MaxNovuMessageTitleLength)
		assert.Equal(t, 1000, MaxNovuMessageBodyLength)
	})
}

func TestNovuMessageNodeConfig_JSON(t *testing.T) {
	t.Run("decodes the stored shape", func(t *testing.T) {
		var c NovuMessageNodeConfig
		require.NoError(t, json.Unmarshal(
			[]byte(`{"description":"Welcome push","workflow_id":"crm-message","title":"Welcome","body":"Your bonus is ready"}`), &c))
		assert.Equal(t, "Welcome push", c.Description)
		assert.Equal(t, "crm-message", c.WorkflowID)
		assert.Equal(t, "Welcome", c.Title)
		assert.Equal(t, "Your bonus is ready", c.Body)
	})

	t.Run("empty text is left out", func(t *testing.T) {
		encoded, err := json.Marshal(NovuMessageNodeConfig{WorkflowID: "crm-welcome-inapp"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"workflow_id":"crm-welcome-inapp"}`, string(encoded))
	})

	t.Run("the config holds no address and no secret", func(t *testing.T) {
		// The bridge address and secret are server settings. A field for either would let
		// them end up in the stored flow.
		encoded, err := json.Marshal(NovuMessageNodeConfig{
			NodeConfigDescription: NodeConfigDescription{Description: "d"},
			WorkflowID:            "crm-message", Title: "t", Body: "b",
		})
		require.NoError(t, err)
		var keys map[string]interface{}
		require.NoError(t, json.Unmarshal(encoded, &keys))
		assert.ElementsMatch(t, []string{"description", "workflow_id", "title", "body"}, mapKeys(keys))
	})
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestAutomationNode_Validate_NovuMessage(t *testing.T) {
	node := func(config map[string]interface{}) *AutomationNode {
		return &AutomationNode{ID: "node-1", AutomationID: "auto-1", Type: NodeTypeNovuMessage, Config: config}
	}

	t.Run("a node still being set up is savable", func(t *testing.T) {
		require.NoError(t, node(map[string]interface{}{}).Validate())
		require.NoError(t, node(map[string]interface{}{"workflow_id": ""}).Validate())
		require.NoError(t, node(map[string]interface{}{"title": "Hi"}).Validate())
	})

	t.Run("a complete node is savable", func(t *testing.T) {
		require.NoError(t, node(map[string]interface{}{
			"workflow_id": "crm-message", "title": "Hi", "body": "There",
		}).Validate())
	})

	t.Run("a workflow id that can never be sent is refused", func(t *testing.T) {
		err := node(map[string]interface{}{"workflow_id": "Not A Workflow"}).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid workflow_id")
	})

	t.Run("text beyond the bridge limits is refused", func(t *testing.T) {
		err := node(map[string]interface{}{
			"workflow_id": "crm-message", "title": strings.Repeat("t", MaxNovuMessageTitleLength+1),
		}).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "title must be at most")

		err = node(map[string]interface{}{
			"workflow_id": "crm-message", "body": strings.Repeat("b", MaxNovuMessageBodyLength+1),
		}).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "body must be at most")
	})

	t.Run("other node types are not held to it", func(t *testing.T) {
		other := &AutomationNode{
			ID: "node-1", AutomationID: "auto-1", Type: NodeTypeWebhook,
			Config: map[string]interface{}{"workflow_id": "NOT VALID", "url": "https://example.com"},
		}
		require.NoError(t, other.Validate())
	})
}

func TestNovuMessageNodeConfig_DescriptionRoundTrip(t *testing.T) {
	var c NovuMessageNodeConfig
	require.NoError(t, json.Unmarshal([]byte(`{"description":"Day 1 push","workflow_id":"crm-message"}`), &c))
	assert.Equal(t, "Day 1 push", c.Description)

	encoded, err := json.Marshal(NovuMessageNodeConfig{})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"description"`)
}

func TestNovuWorkflowsRequest(t *testing.T) {
	t.Run("workspace_id is read from the query", func(t *testing.T) {
		var req NovuWorkflowsRequest
		require.NoError(t, req.FromURLParams(map[string][]string{"workspace_id": {"ws-1"}}))
		assert.Equal(t, "ws-1", req.WorkspaceID)
	})

	t.Run("workspace_id is required", func(t *testing.T) {
		var req NovuWorkflowsRequest
		assert.EqualError(t, req.FromURLParams(map[string][]string{}), "workspace_id is required")
		assert.EqualError(t, req.FromURLParams(map[string][]string{"workspace_id": {""}}), "workspace_id is required")
	})
}

func TestNovuWorkflow_JSON(t *testing.T) {
	var decoded struct {
		Workflows []NovuWorkflow `json:"workflows"`
	}
	require.NoError(t, json.Unmarshal([]byte(
		`{"workflows":[{"id":"crm-message","name":"CRM · Message","uses_text":true},{"id":"crm-welcome-inapp","name":"Welcome","uses_text":false}]}`),
		&decoded))
	require.Len(t, decoded.Workflows, 2)
	assert.Equal(t, NovuWorkflow{ID: "crm-message", Name: "CRM · Message", UsesText: true}, decoded.Workflows[0])
	assert.False(t, decoded.Workflows[1].UsesText)

	encoded, err := json.Marshal(decoded.Workflows[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"crm-message","name":"CRM · Message","uses_text":true}`, string(encoded))
}

func TestNovuBridgeUnavailableError(t *testing.T) {
	var err error = &NovuBridgeUnavailableError{Message: "Could not reach the Novu bridge"}
	assert.EqualError(t, err, "Could not reach the Novu bridge")

	var target *NovuBridgeUnavailableError
	require.True(t, errors.As(fmt.Errorf("wrapped: %w", err), &target))
	assert.Equal(t, "Could not reach the Novu bridge", target.Message)
}

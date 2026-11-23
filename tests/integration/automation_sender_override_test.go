package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/app"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/tests/testutil"
	"github.com/google/uuid"
	shortuuid "github.com/lithammer/shortuuid/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// senderOverrideAutomationIDs are the IDs of a trigger -> email automation. Updating the
// automation takes the same node IDs the create used.
type senderOverrideAutomationIDs struct {
	automation string
	trigger    string
	email      string
}

func newSenderOverrideAutomationIDs() senderOverrideAutomationIDs {
	return senderOverrideAutomationIDs{
		automation: shortuuid.New(),
		trigger:    shortuuid.New(),
		email:      shortuuid.New(),
	}
}

// senderOverrideAutomationRequest is the body of automations.create and automations.update
// for a draft that starts when a contact subscribes to the list and then sends one email
// node, configured by emailConfig.
func senderOverrideAutomationRequest(workspaceID, listID string, ids senderOverrideAutomationIDs, emailConfig map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"workspace_id": workspaceID,
		"automation": map[string]interface{}{
			"id":           ids.automation,
			"workspace_id": workspaceID,
			"name":         "Sender override " + ids.automation,
			"status":       "draft",
			"list_id":      listID,
			"trigger": map[string]interface{}{
				"event_kind": "list.subscribed",
				"list_id":    listID,
				"frequency":  "once",
			},
			"root_node_id": ids.trigger,
			"nodes": []map[string]interface{}{
				{
					"id":            ids.trigger,
					"automation_id": ids.automation,
					"type":          "trigger",
					"config":        map[string]interface{}{},
					"next_node_id":  ids.email,
					"position":      map[string]interface{}{"x": 0, "y": 0},
				},
				{
					"id":            ids.email,
					"automation_id": ids.automation,
					"type":          "email",
					"config":        emailConfig,
					"position":      map[string]interface{}{"x": 0, "y": 100},
				},
			},
			"stats": map[string]interface{}{"enrolled": 0, "completed": 0, "exited": 0, "failed": 0},
		},
	}
}

// sendFromSenderOverrideAutomation creates and activates an automation whose email node is
// configured by emailConfig, subscribes a contact to its list, waits for the automation to
// run for that contact and returns the contact's email.
func sendFromSenderOverrideAutomation(t *testing.T, factory *testutil.TestDataFactory, client *testutil.APIClient, workspaceID string, emailConfig map[string]interface{}) string {
	t.Helper()
	list, err := factory.CreateList(workspaceID)
	require.NoError(t, err)

	ids := newSenderOverrideAutomationIDs()
	createResp, err := client.CreateAutomation(senderOverrideAutomationRequest(workspaceID, list.ID, ids, emailConfig))
	require.NoError(t, err)
	createBody, err := client.ReadBody(createResp)
	_ = createResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createResp.StatusCode, "create automation: %s", createBody)

	activateResp, err := client.ActivateAutomation(map[string]interface{}{
		"workspace_id":  workspaceID,
		"automation_id": ids.automation,
	})
	require.NoError(t, err)
	activateBody, err := client.ReadBody(activateResp)
	_ = activateResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, activateResp.StatusCode, "activate automation: %s", activateBody)

	email := "automation-sender-" + uuid.New().String()[:8] + "@example.com"
	subscribeSenderOverrideContact(t, factory, workspaceID, list.ID, email)

	waitForEnrollmentViaAPI(t, client, ids.automation, email, 5*time.Second)
	completed := waitForAutomationComplete(t, factory, workspaceID, ids.automation, email, 15*time.Second)
	require.Equal(t, domain.ContactAutomationStatusCompleted, completed.Status)
	return email
}

// TestAutomationSenderOverride is the full-stack test of the sender an automation's email
// node can choose in place of its template's: automations.create and automations.update, the
// automation scheduler, the email queue worker, the SMTP provider, and the From the message
// delivered to Mailpit carries.
func TestAutomationSenderOverride(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()

	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface {
		return app.NewApp(cfg)
	})
	defer suite.Cleanup()

	factory := suite.DataFactory
	client := suite.APIClient

	user, err := factory.CreateUser()
	require.NoError(t, err)
	workspace, err := factory.CreateWorkspace()
	require.NoError(t, err)
	require.NoError(t, factory.AddUserToWorkspace(user.ID, workspace.ID, "owner"))

	senders := setupSenderOverrideProvider(t, factory, workspace.ID)

	// The automation scheduler runs the nodes, and the email queue worker sends what the
	// email node enqueues
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	require.NoError(t, suite.ServerManager.StartBackgroundWorkers(workerCtx))

	require.NoError(t, client.Login(user.Email, "password"))
	client.SetWorkspaceID(workspace.ID)

	template, err := factory.CreateTemplate(workspace.ID,
		testutil.WithTemplateName("Automation sender override template"),
		testutil.WithTemplateSubject("Automation sender override"),
		withTemplateSender(senders.template))
	require.NoError(t, err)

	t.Run("an email node sends from its own sender, not from the template's", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))

		email := sendFromSenderOverrideAutomation(t, factory, client, workspace.ID, map[string]interface{}{
			"template_id": template.ID,
			"sender_id":   senders.first.ID,
		})

		msg, err := waitForEmailByRecipientAddr(t, email, 20*time.Second)
		require.NoError(t, err, "the automation email should reach Mailpit")
		assertSenderOverrideFrom(t, msg, senders.first, "email node with a sender")
		assert.NotEqual(t, senders.template.Email, msg.From.Address, "the template's sender must not be used")
	})

	t.Run("an email node without a sender sends from the template's", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))

		email := sendFromSenderOverrideAutomation(t, factory, client, workspace.ID, map[string]interface{}{
			"template_id": template.ID,
		})

		msg, err := waitForEmailByRecipientAddr(t, email, 20*time.Second)
		require.NoError(t, err, "the automation email should reach Mailpit")
		assertSenderOverrideFrom(t, msg, senders.template, "email node without a sender")
	})

	t.Run("saving an automation whose email node names an unknown sender is refused", func(t *testing.T) {
		list, err := factory.CreateList(workspace.ID)
		require.NoError(t, err)
		unknownSender := uuid.New().String()
		withSender := func(senderID string) map[string]interface{} {
			return map[string]interface{}{"template_id": template.ID, "sender_id": senderID}
		}

		// Create
		refused := newSenderOverrideAutomationIDs()
		resp, err := client.CreateAutomation(senderOverrideAutomationRequest(workspace.ID, list.ID, refused, withSender(unknownSender)))
		require.NoError(t, err)
		body, err := client.ReadBody(resp)
		_ = resp.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %s", body)
		assert.Contains(t, body, unknownSender, "the refusal should name the sender it cannot find")

		listResp, err := client.ListAutomations(map[string]string{"workspace_id": workspace.ID, "list_id": list.ID})
		require.NoError(t, err)
		defer func() { _ = listResp.Body.Close() }()
		require.Equal(t, http.StatusOK, listResp.StatusCode)
		var listed struct {
			Automations []struct {
				ID string `json:"id"`
			} `json:"automations"`
		}
		require.NoError(t, json.NewDecoder(listResp.Body).Decode(&listed))
		for _, automation := range listed.Automations {
			assert.NotEqual(t, refused.automation, automation.ID, "a refused automation must not be stored")
		}

		// Update: an automation saved with a sender the provider has is refused when it is
		// saved again with one it does not have, and keeps the sender it had
		saved := newSenderOverrideAutomationIDs()
		resp, err = client.CreateAutomation(senderOverrideAutomationRequest(workspace.ID, list.ID, saved, withSender(senders.first.ID)))
		require.NoError(t, err)
		body, err = client.ReadBody(resp)
		_ = resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", body)

		resp, err = client.UpdateAutomation(senderOverrideAutomationRequest(workspace.ID, list.ID, saved, withSender(unknownSender)))
		require.NoError(t, err)
		body, err = client.ReadBody(resp)
		_ = resp.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %s", body)
		assert.Contains(t, body, unknownSender)

		getResp, err := client.GetAutomation(saved.automation)
		require.NoError(t, err)
		defer func() { _ = getResp.Body.Close() }()
		require.Equal(t, http.StatusOK, getResp.StatusCode)
		var stored struct {
			Automation struct {
				Nodes []struct {
					ID     string                 `json:"id"`
					Config map[string]interface{} `json:"config"`
				} `json:"nodes"`
			} `json:"automation"`
		}
		require.NoError(t, json.NewDecoder(getResp.Body).Decode(&stored))
		var emailNodeSender interface{}
		for _, node := range stored.Automation.Nodes {
			if node.ID == saved.email {
				emailNodeSender = node.Config["sender_id"]
			}
		}
		assert.Equal(t, senders.first.ID, emailNodeSender, "a refused update must not be applied")
	})
}

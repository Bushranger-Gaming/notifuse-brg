package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/app"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/tests/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// senderOverrideSenders are the senders of the marketing provider the sender override tests
// send through. Every template the tests create names the template sender, so an email that
// comes out from the default sender, or from any sender a test did not choose on purpose,
// shows up as a wrong From. The first and second senders are the ones a variation or an
// automation step overrides the template's with; the retired one is for a test that takes a
// sender off the provider after something was saved with it.
type senderOverrideSenders struct {
	fallback domain.EmailSender
	template domain.EmailSender
	first    domain.EmailSender
	second   domain.EmailSender
	retired  domain.EmailSender
}

func newSenderOverrideSenders() senderOverrideSenders {
	extra := func(email, name string) domain.EmailSender {
		return domain.EmailSender{ID: uuid.New().String(), Email: email, Name: name}
	}
	return senderOverrideSenders{
		// NewEmailSender marks its sender as the provider's default
		fallback: domain.NewEmailSender("default-sender@notifuse.test", "Default Sender"),
		template: extra("template-sender@notifuse.test", "Template Sender"),
		first:    extra("first-override@notifuse.test", "First Override"),
		second:   extra("second-override@notifuse.test", "Second Override"),
		retired:  extra("retired-sender@notifuse.test", "Retired Sender"),
	}
}

// setupSenderOverrideProvider makes an SMTP provider (Mailpit) with the senders above the
// workspace's marketing and transactional provider.
func setupSenderOverrideProvider(t *testing.T, factory *testutil.TestDataFactory, workspaceID string) senderOverrideSenders {
	t.Helper()
	senders := newSenderOverrideSenders()
	_, err := factory.SetupWorkspaceWithSMTPProvider(workspaceID,
		testutil.WithIntegrationEmailProvider(domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{
				senders.fallback, senders.template, senders.first, senders.second, senders.retired,
			},
			SMTP: &domain.SMTPSettings{
				Host:     "localhost",
				Port:     1025,
				Username: "",
				Password: "",
				UseTLS:   false,
			},
			RateLimitPerMinute: 2000,
		}))
	require.NoError(t, err)
	return senders
}

// withTemplateSender makes a template name the sender it goes out from.
func withTemplateSender(sender domain.EmailSender) testutil.TemplateOption {
	return func(template *domain.Template) {
		if template.Email != nil {
			template.Email.SenderID = sender.ID
		}
	}
}

// assertSenderOverrideFrom checks the From of a message Mailpit received, which is what the
// recipient's mail client shows.
func assertSenderOverrideFrom(t *testing.T, msg *testutil.MailpitMessage, want domain.EmailSender, what string) {
	t.Helper()
	assert.Equal(t, want.Email, msg.From.Address, "%s: From address", what)
	assert.Equal(t, want.Name, msg.From.Name, "%s: From name", what)
}

// subscribeSenderOverrideContact creates a contact actively subscribed to the list.
func subscribeSenderOverrideContact(t *testing.T, factory *testutil.TestDataFactory, workspaceID, listID, email string) {
	t.Helper()
	contact, err := factory.CreateContact(workspaceID, testutil.WithContactEmail(email))
	require.NoError(t, err)
	_, err = factory.CreateContactList(workspaceID,
		testutil.WithContactListEmail(contact.Email),
		testutil.WithContactListListID(listID),
		testutil.WithContactListStatus(domain.ContactListStatusActive))
	require.NoError(t, err)
}

// senderOverrideAudience creates a list with size subscribed contacts and returns their emails.
func senderOverrideAudience(t *testing.T, factory *testutil.TestDataFactory, workspaceID, prefix string, size int) (listID string, emails []string) {
	t.Helper()
	list, err := factory.CreateList(workspaceID)
	require.NoError(t, err)
	for i := 0; i < size; i++ {
		email := fmt.Sprintf("%s-%d-%s@example.com", prefix, i, uuid.New().String()[:8])
		subscribeSenderOverrideContact(t, factory, workspaceID, list.ID, email)
		emails = append(emails, email)
	}
	return list.ID, emails
}

// senderOverrideVariation is a variation as the API takes it; senderID is left out of the
// body when empty, as the console does for a variation that keeps its template's sender.
func senderOverrideVariation(name, templateID, senderID string) map[string]interface{} {
	variation := map[string]interface{}{
		"variation_name": name,
		"template_id":    templateID,
	}
	if senderID != "" {
		variation["sender_id"] = senderID
	}
	return variation
}

// singleVariationSettings are the test settings of a broadcast that is not an A/B test.
func singleVariationSettings(templateID, senderID string) map[string]interface{} {
	return map[string]interface{}{
		"enabled":           false,
		"sample_percentage": 100,
		"variations":        []map[string]interface{}{senderOverrideVariation("Default", templateID, senderID)},
	}
}

// createSenderOverrideBroadcastRequest is the body of broadcasts.create.
func createSenderOverrideBroadcastRequest(workspaceID, name, listID string, testSettings map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"workspace_id": workspaceID,
		"name":         name,
		"audience": map[string]interface{}{
			"list":                 listID,
			"exclude_unsubscribed": true,
		},
		"test_settings": testSettings,
	}
}

// createSenderOverrideBroadcast creates a broadcast through the HTTP API and returns its ID.
func createSenderOverrideBroadcast(t *testing.T, client *testutil.APIClient, workspaceID, name, listID string, testSettings map[string]interface{}) string {
	t.Helper()
	resp, err := client.CreateBroadcast(createSenderOverrideBroadcastRequest(workspaceID, name, listID, testSettings))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := client.ReadBody(resp)
		t.Fatalf("create broadcast: status %d: %s", resp.StatusCode, body)
	}

	var created struct {
		Broadcast struct {
			ID string `json:"id"`
		} `json:"broadcast"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEmpty(t, created.Broadcast.ID)
	return created.Broadcast.ID
}

// senderOverrideStoredBroadcast is the part of a stored broadcast these tests read back.
type senderOverrideStoredBroadcast struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	PauseReason  string `json:"pause_reason"`
	TestSettings struct {
		Variations []struct {
			TemplateID string `json:"template_id"`
			SenderID   string `json:"sender_id"`
		} `json:"variations"`
	} `json:"test_settings"`
}

func getSenderOverrideBroadcast(t *testing.T, client *testutil.APIClient, broadcastID string) senderOverrideStoredBroadcast {
	t.Helper()
	resp, err := client.GetBroadcast(broadcastID)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Broadcast senderOverrideStoredBroadcast `json:"broadcast"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got.Broadcast
}

// sendSenderOverrideBroadcast sends the broadcast now and drives its task until the broadcast
// reaches one of the statuses.
func sendSenderOverrideBroadcast(t *testing.T, client *testutil.APIClient, workspaceID, broadcastID string, doneStatuses ...string) {
	t.Helper()
	resp, err := client.ScheduleBroadcast(map[string]interface{}{
		"workspace_id": workspaceID,
		"id":           broadcastID,
		"send_now":     true,
	})
	require.NoError(t, err)
	body, _ := client.ReadBody(resp)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "schedule broadcast: %s", body)

	_, err = testutil.WaitForBroadcastStatusWithExecution(t, client, broadcastID, doneStatuses, 60*time.Second)
	require.NoError(t, err)
}

// removeSenderFromMarketingProvider takes a sender off the workspace's marketing provider, as
// settings do when someone deletes it.
func removeSenderFromMarketingProvider(t *testing.T, suite *testutil.IntegrationTestSuite, workspaceID string, sender domain.EmailSender) {
	t.Helper()
	repo := suite.ServerManager.GetApp().GetWorkspaceRepository()
	workspace, err := repo.GetByID(context.Background(), workspaceID)
	require.NoError(t, err)

	integration := workspace.GetIntegrationByID(workspace.Settings.MarketingEmailProviderID)
	require.NotNil(t, integration)

	var kept []domain.EmailSender
	for _, existing := range integration.EmailProvider.Senders {
		if existing.ID != sender.ID {
			kept = append(kept, existing)
		}
	}
	require.Less(t, len(kept), len(integration.EmailProvider.Senders), "sender %s was not on the provider", sender.ID)
	integration.EmailProvider.Senders = kept
	require.NoError(t, repo.Update(context.Background(), workspace))
}

// TestBroadcastSenderOverride is the full-stack test of the sender a broadcast variation can
// choose in place of its template's: broadcasts.create and broadcasts.update, the
// orchestrator and email queue worker, the SMTP provider, and the From the message
// delivered to Mailpit carries.
func TestBroadcastSenderOverride(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()

	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface {
		return app.NewApp(cfg)
	})
	defer suite.Cleanup()

	client := suite.APIClient
	factory := suite.DataFactory

	user, err := factory.CreateUser()
	require.NoError(t, err)
	workspace, err := factory.CreateWorkspace()
	require.NoError(t, err)
	require.NoError(t, factory.AddUserToWorkspace(user.ID, workspace.ID, "owner"))

	senders := setupSenderOverrideProvider(t, factory, workspace.ID)

	// The email queue worker sends what the broadcast enqueues
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	require.NoError(t, suite.ServerManager.StartBackgroundWorkers(workerCtx))
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, client.Login(user.Email, "password"))
	client.SetWorkspaceID(workspace.ID)

	// The same template serves the first two subtests: what differs between them is only
	// whether the variation names a sender.
	template, err := factory.CreateTemplate(workspace.ID,
		testutil.WithTemplateName("Sender override template"),
		testutil.WithTemplateSubject("Sender override"),
		withTemplateSender(senders.template))
	require.NoError(t, err)

	t.Run("a variation sends from its own sender, not from the template's", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))
		listID, emails := senderOverrideAudience(t, factory, workspace.ID, "override", 1)

		broadcastID := createSenderOverrideBroadcast(t, client, workspace.ID, "Variation sender",
			listID, singleVariationSettings(template.ID, senders.first.ID))

		stored := getSenderOverrideBroadcast(t, client, broadcastID)
		require.Len(t, stored.TestSettings.Variations, 1)
		assert.Equal(t, senders.first.ID, stored.TestSettings.Variations[0].SenderID,
			"the API keeps the sender the variation was created with")

		sendSenderOverrideBroadcast(t, client, workspace.ID, broadcastID, "processed", "completed")

		msg, err := waitForEmailByRecipientAddr(t, emails[0], 20*time.Second)
		require.NoError(t, err, "the broadcast email should reach Mailpit")
		assertSenderOverrideFrom(t, msg, senders.first, "variation with a sender")
		assert.NotEqual(t, senders.template.Email, msg.From.Address, "the template's sender must not be used")
	})

	t.Run("a variation without a sender sends from the template's", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))
		listID, emails := senderOverrideAudience(t, factory, workspace.ID, "template", 1)

		broadcastID := createSenderOverrideBroadcast(t, client, workspace.ID, "Template sender",
			listID, singleVariationSettings(template.ID, ""))

		stored := getSenderOverrideBroadcast(t, client, broadcastID)
		require.Len(t, stored.TestSettings.Variations, 1)
		assert.Empty(t, stored.TestSettings.Variations[0].SenderID)

		sendSenderOverrideBroadcast(t, client, workspace.ID, broadcastID, "processed", "completed")

		msg, err := waitForEmailByRecipientAddr(t, emails[0], 20*time.Second)
		require.NoError(t, err, "the broadcast email should reach Mailpit")
		assertSenderOverrideFrom(t, msg, senders.template, "variation without a sender")
	})

	t.Run("each A/B variation sends from its own sender", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))

		// The subject tells which variation a recipient was sent: the queue sender picks
		// the template of each recipient at random.
		subjectA, subjectB := "Sender override variation A", "Sender override variation B"
		templateA, err := factory.CreateTemplate(workspace.ID,
			testutil.WithTemplateName("Sender override A"),
			testutil.WithTemplateSubject(subjectA),
			withTemplateSender(senders.template))
		require.NoError(t, err)
		templateB, err := factory.CreateTemplate(workspace.ID,
			testutil.WithTemplateName("Sender override B"),
			testutil.WithTemplateSubject(subjectB),
			withTemplateSender(senders.template))
		require.NoError(t, err)
		wantFrom := map[string]domain.EmailSender{subjectA: senders.first, subjectB: senders.second}

		// The whole audience is the test sample, so every recipient is sent a variation and
		// none waits for a winner.
		listID, emails := senderOverrideAudience(t, factory, workspace.ID, "ab", 24)
		broadcastID := createSenderOverrideBroadcast(t, client, workspace.ID, "A/B senders", listID,
			map[string]interface{}{
				"enabled":           true,
				"sample_percentage": 100,
				"auto_send_winner":  false,
				"variations": []map[string]interface{}{
					senderOverrideVariation("A", templateA.ID, senders.first.ID),
					senderOverrideVariation("B", templateB.ID, senders.second.ID),
				},
			})

		stored := getSenderOverrideBroadcast(t, client, broadcastID)
		require.Len(t, stored.TestSettings.Variations, 2)
		assert.Equal(t, senders.first.ID, stored.TestSettings.Variations[0].SenderID)
		assert.Equal(t, senders.second.ID, stored.TestSettings.Variations[1].SenderID)

		sendSenderOverrideBroadcast(t, client, workspace.ID, broadcastID, "test_completed")

		sent := map[string]int{}
		for _, email := range emails {
			msg, err := waitForEmailByRecipientAddr(t, email, 20*time.Second)
			require.NoError(t, err, "%s should have received the broadcast", email)

			want, known := wantFrom[msg.Subject]
			require.True(t, known, "%s received an email with an unexpected subject %q", email, msg.Subject)
			assertSenderOverrideFrom(t, msg, want, fmt.Sprintf("%s, sent %q", email, msg.Subject))
			sent[msg.Subject]++
		}
		// A split that sent every recipient the same variation would prove nothing
		assert.Positive(t, sent[subjectA], "no recipient was sent variation A")
		assert.Positive(t, sent[subjectB], "no recipient was sent variation B")
		assert.Equal(t, len(emails), sent[subjectA]+sent[subjectB])
	})

	t.Run("a sender the provider does not have is refused when the broadcast is created", func(t *testing.T) {
		listID, _ := senderOverrideAudience(t, factory, workspace.ID, "unknown", 1)
		unknownSender := uuid.New().String()
		name := "Unknown sender " + unknownSender[:8]

		resp, err := client.CreateBroadcast(createSenderOverrideBroadcastRequest(workspace.ID, name, listID,
			singleVariationSettings(template.ID, unknownSender)))
		require.NoError(t, err)
		body, err := client.ReadBody(resp)
		_ = resp.Body.Close()
		require.NoError(t, err)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %s", body)
		assert.Contains(t, body, unknownSender, "the refusal should name the sender it cannot find")

		listResp, err := client.ListBroadcasts(map[string]string{"workspace_id": workspace.ID})
		require.NoError(t, err)
		defer func() { _ = listResp.Body.Close() }()
		require.Equal(t, http.StatusOK, listResp.StatusCode)
		var listed struct {
			Broadcasts []struct {
				Name string `json:"name"`
			} `json:"broadcasts"`
		}
		require.NoError(t, json.NewDecoder(listResp.Body).Decode(&listed))
		for _, broadcast := range listed.Broadcasts {
			assert.NotEqual(t, name, broadcast.Name, "a refused broadcast must not be stored")
		}
	})

	t.Run("renaming a broadcast whose sender was deleted since it was saved still works", func(t *testing.T) {
		listID, _ := senderOverrideAudience(t, factory, workspace.ID, "retired", 1)
		broadcastID := createSenderOverrideBroadcast(t, client, workspace.ID, "Before rename", listID,
			singleVariationSettings(template.ID, senders.retired.ID))

		removeSenderFromMarketingProvider(t, suite, workspace.ID, senders.retired)

		// A rename carries no test_settings: the variations are not submitted again, so they
		// are not checked against the provider.
		resp, err := client.UpdateBroadcast(map[string]interface{}{
			"workspace_id": workspace.ID,
			"id":           broadcastID,
			"name":         "After rename",
		})
		require.NoError(t, err)
		body, err := client.ReadBody(resp)
		_ = resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

		stored := getSenderOverrideBroadcast(t, client, broadcastID)
		assert.Equal(t, "After rename", stored.Name)
		require.Len(t, stored.TestSettings.Variations, 1)
		assert.Equal(t, senders.retired.ID, stored.TestSettings.Variations[0].SenderID,
			"the rename must leave the stored variation as it was")

		// A request that does submit the variation is checked against what the provider has now
		resp, err = client.UpdateBroadcast(map[string]interface{}{
			"workspace_id":  workspace.ID,
			"id":            broadcastID,
			"name":          "Rename with variations",
			"test_settings": singleVariationSettings(template.ID, senders.retired.ID),
		})
		require.NoError(t, err)
		body, err = client.ReadBody(resp)
		_ = resp.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %s", body)
		assert.Contains(t, body, senders.retired.ID)
		assert.Equal(t, "After rename", getSenderOverrideBroadcast(t, client, broadcastID).Name,
			"a refused update must not be applied")
	})
}

// TestBroadcastSenderOverrideAtSendTime covers the sender of a variation once the broadcast
// is being sent: a sender deleted from the provider after the broadcast was saved, and the
// A/B winner phase, which sends the recipients the test phase left with the winning
// variation's own sender.
func TestBroadcastSenderOverrideAtSendTime(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()

	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface {
		return app.NewApp(cfg)
	})
	defer suite.Cleanup()

	client := suite.APIClient
	factory := suite.DataFactory

	user, err := factory.CreateUser()
	require.NoError(t, err)
	workspace, err := factory.CreateWorkspace()
	require.NoError(t, err)
	require.NoError(t, factory.AddUserToWorkspace(user.ID, workspace.ID, "owner"))

	senders := setupSenderOverrideProvider(t, factory, workspace.ID)

	// The email queue worker sends what the broadcast enqueues
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	require.NoError(t, suite.ServerManager.StartBackgroundWorkers(workerCtx))
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, client.Login(user.Email, "password"))
	client.SetWorkspaceID(workspace.ID)

	t.Run("a variation sender deleted before the send pauses the broadcast and sends nothing", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))

		// The subject is this subtest's own, so Mailpit can be asked for what it received
		subject := "Sender deleted before the send"
		template, err := factory.CreateTemplate(workspace.ID,
			testutil.WithTemplateName("Sender deleted template"),
			testutil.WithTemplateSubject(subject),
			withTemplateSender(senders.template))
		require.NoError(t, err)

		listID, _ := senderOverrideAudience(t, factory, workspace.ID, "deleted", 3)
		broadcastID := createSenderOverrideBroadcast(t, client, workspace.ID, "Sender deleted before the send", listID,
			singleVariationSettings(template.ID, senders.retired.ID))

		// The sender was valid when the broadcast was saved; now it is gone, as settings do
		// when someone deletes it
		removeSenderFromMarketingProvider(t, suite, workspace.ID, senders.retired)

		resp, err := client.ScheduleBroadcast(map[string]interface{}{
			"workspace_id": workspace.ID,
			"id":           broadcastID,
			"send_now":     true,
		})
		require.NoError(t, err)
		body, _ := client.ReadBody(resp)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "schedule broadcast: %s", body)

		// Counting every recipient as a failed build used to run the list to a processed
		// broadcast with nothing sent, and nothing left to retry
		status, err := testutil.WaitForBroadcastStatusWithExecution(t, client, broadcastID, []string{"paused"}, 90*time.Second)
		require.NoError(t, err, "a broadcast whose variation sender is gone must pause, not run on or fail")
		assert.Equal(t, "paused", status)

		stored := getSenderOverrideBroadcast(t, client, broadcastID)
		assert.Equal(t, "paused", stored.Status)
		assert.Contains(t, stored.PauseReason, "Variation sender not found")
		assert.Contains(t, stored.PauseReason, senders.retired.ID, "the reason should name the sender to restore")

		// Give the queue worker time to send anything that had been enqueued
		time.Sleep(5 * time.Second)
		recipients, err := testutil.GetAllMailpitRecipients(t, subject)
		require.NoError(t, err)
		assert.Empty(t, recipients, "nothing of this broadcast may have been sent")
	})

	t.Run("the winner phase sends the remaining recipients from the winning variation's sender", func(t *testing.T) {
		require.NoError(t, testutil.ClearMailpitMessages(t))

		// The subject tells which variation a recipient was sent
		const subjectPrefix = "Sender winner variation"
		subjectA, subjectB := subjectPrefix+" A", subjectPrefix+" B"
		templateA, err := factory.CreateTemplate(workspace.ID,
			testutil.WithTemplateName("Sender winner A"),
			testutil.WithTemplateSubject(subjectA),
			withTemplateSender(senders.template))
		require.NoError(t, err)
		templateB, err := factory.CreateTemplate(workspace.ID,
			testutil.WithTemplateName("Sender winner B"),
			testutil.WithTemplateSubject(subjectB),
			withTemplateSender(senders.template))
		require.NoError(t, err)
		wantFrom := map[string]domain.EmailSender{subjectA: senders.first, subjectB: senders.second}

		const samplePercentage = 50
		listID, emails := senderOverrideAudience(t, factory, workspace.ID, "winner", 24)
		broadcastID := createSenderOverrideBroadcast(t, client, workspace.ID, "Winner phase senders", listID,
			map[string]interface{}{
				"enabled":           true,
				"sample_percentage": samplePercentage,
				"auto_send_winner":  false,
				"variations": []map[string]interface{}{
					senderOverrideVariation("A", templateA.ID, senders.first.ID),
					senderOverrideVariation("B", templateB.ID, senders.second.ID),
				},
			})

		// The test phase sends its sample, then waits for a winner
		sendSenderOverrideBroadcast(t, client, workspace.ID, broadcastID, "test_completed")

		// The phase completes when its sample is enqueued, not delivered. Wait for the sample to
		// reach Mailpit, so the mailbox can be cleared without a test-phase email still on its
		// way into the winner phase's share of it.
		sampleSize := len(emails) * samplePercentage / 100
		var testPhase map[string][]*testutil.MailpitMessage
		require.Eventually(t, func() bool {
			messages, err := testutil.GetMailpitMessagesByRecipient(t, subjectPrefix)
			if err != nil {
				return false
			}
			testPhase = messages
			return len(messages) >= sampleSize
		}, 90*time.Second, time.Second, "the test phase should deliver its sample")

		// Each sample recipient was sent one of the variations from its own sender
		for email, messages := range testPhase {
			require.Len(t, messages, 1, "%s should be sent one email in the test phase", email)
			want, known := wantFrom[messages[0].Subject]
			require.True(t, known, "%s received an email with an unexpected subject %q", email, messages[0].Subject)
			assertSenderOverrideFrom(t, messages[0], want, fmt.Sprintf("%s, sent %q in the test phase", email, messages[0].Subject))
		}

		// Whatever Mailpit holds from here on was sent by the winner phase
		require.NoError(t, testutil.ClearMailpitMessages(t))

		resp, err := client.SelectBroadcastWinner(map[string]interface{}{
			"workspace_id": workspace.ID,
			"id":           broadcastID,
			"template_id":  templateB.ID,
		})
		require.NoError(t, err)
		body, _ := client.ReadBody(resp)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "select winner: %s", body)

		_, err = testutil.WaitForBroadcastStatusWithExecution(t, client, broadcastID, []string{"processed"}, 90*time.Second)
		require.NoError(t, err, "the winner phase should send the rest of the audience")

		var winnerPhase map[string][]*testutil.MailpitMessage
		require.Eventually(t, func() bool {
			messages, err := testutil.GetMailpitMessagesByRecipient(t, subjectPrefix)
			if err != nil {
				return false
			}
			winnerPhase = messages
			return len(messages) >= len(emails)-len(testPhase)
		}, 90*time.Second, time.Second, "the winner phase should deliver to the rest of the audience")

		// Every email of the winner phase is the winning variation, from the winner's sender
		for email, messages := range winnerPhase {
			_, sentInTestPhase := testPhase[email]
			assert.False(t, sentInTestPhase, "%s was sent in the test phase and again in the winner phase", email)
			assert.Len(t, messages, 1, "%s should be sent one email in the winner phase", email)
			for _, msg := range messages {
				assert.Equal(t, subjectB, msg.Subject, "%s: the winner phase sends only the winning variation", email)
				assertSenderOverrideFrom(t, msg, senders.second, fmt.Sprintf("%s, sent %q in the winner phase", email, msg.Subject))
			}
		}

		// Between the two phases the whole audience was sent the broadcast
		for _, email := range emails {
			_, inTestPhase := testPhase[email]
			_, inWinnerPhase := winnerPhase[email]
			assert.True(t, inTestPhase != inWinnerPhase, "%s should be sent the broadcast in exactly one phase", email)
		}
	})
}

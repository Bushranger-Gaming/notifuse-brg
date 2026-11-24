package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/app"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContactBulkImportE2E tests the complete end-to-end bulk contact import flow
// This test covers:
// - REAL PostgreSQL database operations (catches SQL syntax errors)
// - Bulk insert of new contacts
// - Bulk update of existing contacts
// - Mixed create/update operations
// - List subscription during bulk import
// - PostgreSQL behavior (xmax detection, ON CONFLICT)
// - Transaction integrity
func TestContactBulkImportE2E(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()

	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface {
		return app.NewApp(cfg)
	})
	defer func() { suite.Cleanup() }()

	client := suite.APIClient
	factory := suite.DataFactory

	// Create test user and workspace
	user, err := factory.CreateUser()
	require.NoError(t, err)
	workspace, err := factory.CreateWorkspace()
	require.NoError(t, err)

	// Add user to workspace as owner
	err = factory.AddUserToWorkspace(user.ID, workspace.ID, "owner")
	require.NoError(t, err)

	// Login to get auth token
	err = client.Login(user.Email, "password")
	require.NoError(t, err)
	client.SetWorkspaceID(workspace.ID)

	// Get workspace database connection for direct queries
	workspaceDB, err := suite.DBManager.GetWorkspaceDB(workspace.ID)
	require.NoError(t, err)

	t.Run("Bulk Insert New Contacts", func(t *testing.T) {
		testBulkInsertNewContacts(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Bulk Update Existing Contacts", func(t *testing.T) {
		testBulkUpdateExistingContacts(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Mixed Create and Update Operations", func(t *testing.T) {
		testMixedCreateUpdateOperations(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Bulk Import with List Subscription", func(t *testing.T) {
		testBulkImportWithListSubscription(t, client, factory, workspaceDB, workspace.ID)
	})

	t.Run("Bulk Import with Validation Errors", func(t *testing.T) {
		testBulkImportWithValidationErrors(t, client, workspace.ID)
	})

	t.Run("Large Batch Performance", func(t *testing.T) {
		testLargeBatchPerformance(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Transaction Integrity", func(t *testing.T) {
		testTransactionIntegrity(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Custom Fields Preservation", func(t *testing.T) {
		testCustomFieldsPreservation(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Duplicate Emails in Single Batch", func(t *testing.T) {
		testDuplicateEmailsInBatch(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Reimport Keeps and Replaces Created At", func(t *testing.T) {
		testReimportCreatedAt(t, client, workspaceDB)
	})

	t.Run("New Contact Created At", func(t *testing.T) {
		testNewContactCreatedAt(t, client, workspaceDB)
	})

	t.Run("Invalid Created At Fails Only Its Contact", func(t *testing.T) {
		testInvalidCreatedAtInBatch(t, client, workspaceDB)
	})

	t.Run("Upsert Sets Created At On A New Contact", func(t *testing.T) {
		testUpsertCreatedAt(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Upsert Of An Existing Contact Keeps Created At", func(t *testing.T) {
		testUpsertExistingContactCreatedAt(t, client, workspaceDB, workspace.ID)
	})

	t.Run("Import Normalizes Deduplicates And Chunks Created At", func(t *testing.T) {
		testImportDedupeKeepsLastCreatedAt(t, client, workspaceDB)
	})

	t.Run("Created At At The End Of Year 9999 Reads Back", func(t *testing.T) {
		testCreatedAtEndOfYear9999ReadsBack(t, client, workspaceDB, workspace.ID)
	})
}

func testBulkInsertNewContacts(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// Create batch of new contacts - TESTS REAL SQL EXECUTION
	contacts := []map[string]interface{}{
		{
			"email":      fmt.Sprintf("bulk_new1_%d@example.com", time.Now().UnixNano()),
			"first_name": "John",
			"last_name":  "Doe",
		},
		{
			"email":      fmt.Sprintf("bulk_new2_%d@example.com", time.Now().UnixNano()),
			"first_name": "Jane",
			"last_name":  "Smith",
		},
		{
			"email":      fmt.Sprintf("bulk_new3_%d@example.com", time.Now().UnixNano()),
			"first_name": "Bob",
			"last_name":  "Johnson",
		},
	}

	// Import contacts - executes REAL multi-row INSERT against PostgreSQL
	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
			Error  string `json:"error,omitempty"`
		} `json:"operations"`
		Error string `json:"error,omitempty"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	// Verify all operations were successful creates
	assert.Len(t, importResp.Operations, 3)
	for i, op := range importResp.Operations {
		assert.Equal(t, contacts[i]["email"], op.Email)
		assert.Equal(t, "create", op.Action, "Expected create action for new contact")
		assert.Empty(t, op.Error)
	}

	// Verify contacts exist in database with correct data
	for _, contact := range contacts {
		email := contact["email"].(string)
		var firstName, lastName string
		err := workspaceDB.QueryRow(
			"SELECT first_name, last_name FROM contacts WHERE email = $1",
			email,
		).Scan(&firstName, &lastName)
		require.NoError(t, err, "Contact should exist in database")
		assert.Equal(t, contact["first_name"], firstName)
		assert.Equal(t, contact["last_name"], lastName)
	}
}

func testBulkUpdateExistingContacts(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// First, create some contacts
	originalContacts := []map[string]interface{}{
		{
			"email":      fmt.Sprintf("bulk_update1_%d@example.com", time.Now().UnixNano()),
			"first_name": "Original",
			"last_name":  "Name1",
		},
		{
			"email":      fmt.Sprintf("bulk_update2_%d@example.com", time.Now().UnixNano()),
			"first_name": "Original",
			"last_name":  "Name2",
		},
	}

	// Import original contacts
	resp, err := client.BatchImportContacts(originalContacts, nil)
	require.NoError(t, err)
	_ = resp.Body.Close()

	// Wait a moment to ensure different updated_at timestamps
	time.Sleep(100 * time.Millisecond)

	// Update the same contacts with new data - TESTS ON CONFLICT DO UPDATE
	updatedContacts := []map[string]interface{}{
		{
			"email":      originalContacts[0]["email"],
			"first_name": "Updated",
			"last_name":  "Name1",
			"phone":      "+1234567890",
		},
		{
			"email":      originalContacts[1]["email"],
			"first_name": "Updated",
			"last_name":  "Name2",
			"phone":      "+0987654321",
		},
	}

	// Import updated contacts
	resp, err = client.BatchImportContacts(updatedContacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
			Error  string `json:"error,omitempty"`
		} `json:"operations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	// Verify all operations were successful updates (xmax detection)
	assert.Len(t, importResp.Operations, 2)
	for i, op := range importResp.Operations {
		assert.Equal(t, updatedContacts[i]["email"], op.Email)
		assert.Equal(t, "update", op.Action, "Expected update action for existing contact")
		assert.Empty(t, op.Error)
	}

	// Verify contacts were updated in database
	for _, contact := range updatedContacts {
		email := contact["email"].(string)
		var firstName, lastName, phone string
		err := workspaceDB.QueryRow(
			"SELECT first_name, last_name, COALESCE(phone, '') FROM contacts WHERE email = $1",
			email,
		).Scan(&firstName, &lastName, &phone)
		require.NoError(t, err)
		assert.Equal(t, contact["first_name"], firstName)
		assert.Equal(t, contact["last_name"], lastName)
		assert.Equal(t, contact["phone"], phone)
	}

	// Verify there's still only one row per email (no duplicates)
	for _, contact := range updatedContacts {
		email := contact["email"].(string)
		var count int
		err := workspaceDB.QueryRow(
			"SELECT COUNT(*) FROM contacts WHERE email = $1",
			email,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Should have exactly one contact per email")
	}
}

func testMixedCreateUpdateOperations(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// Create one existing contact
	existingEmail := fmt.Sprintf("mixed_existing_%d@example.com", time.Now().UnixNano())
	resp, err := client.CreateContact(map[string]interface{}{
		"workspace_id": workspaceID,
		"contact": map[string]interface{}{
			"email":      existingEmail,
			"first_name": "Existing",
		},
	})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Verify the contact was actually created
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		t.Logf("CreateContact failed with status %d: %+v", resp.StatusCode, errResp)
	}
	require.Equal(t, http.StatusOK, resp.StatusCode, "CreateContact should succeed")

	// Verify contact exists in database before the bulk operation
	var existsCount int
	err = workspaceDB.QueryRow("SELECT COUNT(*) FROM contacts WHERE email = $1", existingEmail).Scan(&existsCount)
	require.NoError(t, err)
	require.Equal(t, 1, existsCount, "Contact should exist in database before bulk import")

	// Create batch with mix of new and existing contacts
	newEmail1 := fmt.Sprintf("mixed_new1_%d@example.com", time.Now().UnixNano())
	newEmail2 := fmt.Sprintf("mixed_new2_%d@example.com", time.Now().UnixNano())

	contacts := []map[string]interface{}{
		{
			"email":      newEmail1,
			"first_name": "New",
			"last_name":  "Contact1",
		},
		{
			"email":      existingEmail,
			"first_name": "Updated",
			"last_name":  "Existing",
		},
		{
			"email":      newEmail2,
			"first_name": "New",
			"last_name":  "Contact2",
		},
	}

	// Import mixed batch - TESTS xmax DETECTION ACCURACY
	resp, err = client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
		} `json:"operations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	// Verify operations
	assert.Len(t, importResp.Operations, 3)

	// Find operations by email
	opsByEmail := make(map[string]string)
	for _, op := range importResp.Operations {
		opsByEmail[op.Email] = op.Action
	}

	assert.Equal(t, "create", opsByEmail[newEmail1], "New contact should be created")
	assert.Equal(t, "update", opsByEmail[existingEmail], "Existing contact should be updated")
	assert.Equal(t, "create", opsByEmail[newEmail2], "New contact should be created")

	// Verify all contacts exist with correct data
	var firstName string
	err = workspaceDB.QueryRow(
		"SELECT first_name FROM contacts WHERE email = $1",
		existingEmail,
	).Scan(&firstName)
	require.NoError(t, err)
	assert.Equal(t, "Updated", firstName, "Existing contact should be updated")
}

func testBulkImportWithListSubscription(t *testing.T, client *testutil.APIClient, factory *testutil.TestDataFactory, workspaceDB *sql.DB, workspaceID string) {
	// Create a test list
	list, err := factory.CreateList(workspaceID)
	require.NoError(t, err)

	// Create batch of contacts to import
	contacts := []map[string]interface{}{
		{
			"email":      fmt.Sprintf("list_sub1_%d@example.com", time.Now().UnixNano()),
			"first_name": "List",
			"last_name":  "Subscriber1",
		},
		{
			"email":      fmt.Sprintf("list_sub2_%d@example.com", time.Now().UnixNano()),
			"first_name": "List",
			"last_name":  "Subscriber2",
		},
	}

	// Import contacts with list subscription - TESTS BULK LIST SUBSCRIPTION
	resp, err := client.BatchImportContacts(contacts, []string{list.ID})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify contacts were created
	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
		} `json:"operations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)
	assert.Len(t, importResp.Operations, 2)

	// Verify contacts are subscribed to the list
	for _, contact := range contacts {
		email := contact["email"].(string)
		var status string
		err := workspaceDB.QueryRow(`
			SELECT status FROM contact_lists 
			WHERE email = $1 AND list_id = $2
		`, email, list.ID).Scan(&status)
		require.NoError(t, err, "Contact should be subscribed to list")
		assert.Equal(t, "active", status)
	}
}

func testBulkImportWithValidationErrors(t *testing.T, client *testutil.APIClient, workspaceID string) {
	// Create batch with some invalid contacts
	contacts := []map[string]interface{}{
		{
			"email":      fmt.Sprintf("valid1_%d@example.com", time.Now().UnixNano()),
			"first_name": "Valid",
		},
		{
			// Missing email - invalid
			"first_name": "Invalid",
		},
		{
			"email":      fmt.Sprintf("valid2_%d@example.com", time.Now().UnixNano()),
			"first_name": "Valid",
		},
	}

	// Import contacts - TESTS PARTIAL SUCCESS BEHAVIOR
	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
			Error  string `json:"error,omitempty"`
		} `json:"operations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	// Verify response includes all operations
	assert.Len(t, importResp.Operations, 3)

	// Count successful and failed operations
	var successCount, errorCount int
	for _, op := range importResp.Operations {
		if op.Action == "error" {
			errorCount++
			assert.NotEmpty(t, op.Error)
		} else {
			successCount++
		}
	}

	assert.Equal(t, 2, successCount, "Two contacts should succeed")
	assert.Equal(t, 1, errorCount, "One contact should fail validation")
}

func testLargeBatchPerformance(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// Create a large batch of contacts (1000 contacts)
	batchSize := 1000
	contacts := make([]map[string]interface{}, batchSize)
	timestamp := time.Now().UnixNano()

	for i := 0; i < batchSize; i++ {
		contacts[i] = map[string]interface{}{
			"email":      fmt.Sprintf("perf_test_%d_%d@example.com", timestamp, i),
			"first_name": fmt.Sprintf("User%d", i),
			"last_name":  "Performance",
		}
	}

	// Measure import time - TESTS PERFORMANCE & SCALABILITY
	startTime := time.Now()
	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	duration := time.Since(startTime)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var importResp struct {
		Operations []struct {
			Action string `json:"action"`
		} `json:"operations"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	// Verify all contacts were imported
	assert.Len(t, importResp.Operations, batchSize)
	for _, op := range importResp.Operations {
		assert.Equal(t, "create", op.Action)
	}

	// Performance assertion
	t.Logf("Imported %d contacts in %v", batchSize, duration)
	assert.Less(t, duration, 30*time.Second, "Bulk import should complete within 30 seconds")

	// Verify count in database
	var count int
	err = workspaceDB.QueryRow(
		"SELECT COUNT(*) FROM contacts WHERE email LIKE $1",
		fmt.Sprintf("perf_test_%d_%%@example.com", timestamp),
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, batchSize, count)
}

func testTransactionIntegrity(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// TESTS ACID TRANSACTION PROPERTIES
	timestamp := time.Now().UnixNano()
	email1 := fmt.Sprintf("txn_test1_%d@example.com", timestamp)
	email2 := fmt.Sprintf("txn_test2_%d@example.com", timestamp)

	contacts := []map[string]interface{}{
		{
			"email":      email1,
			"first_name": "Transaction",
			"last_name":  "Test1",
		},
		{
			"email":      email2,
			"first_name": "Transaction",
			"last_name":  "Test2",
		},
	}

	// Import contacts
	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify both contacts exist (transaction was committed)
	for _, contact := range contacts {
		email := contact["email"].(string)
		var exists bool
		err := workspaceDB.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM contacts WHERE email = $1)",
			email,
		).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "Contact should exist after successful batch import")
	}

	// Now update one contact to verify update transaction
	contacts[0]["first_name"] = "Updated"
	resp, err = client.BatchImportContacts(contacts[:1], nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Verify update was applied
	var firstName string
	err = workspaceDB.QueryRow(
		"SELECT first_name FROM contacts WHERE email = $1",
		email1,
	).Scan(&firstName)
	require.NoError(t, err)
	assert.Equal(t, "Updated", firstName)
}

func testCustomFieldsPreservation(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// Test importing a contact with ALL fields populated
	// This ensures the column count matches the placeholder count

	email := fmt.Sprintf("all_fields_%d@example.com", time.Now().UnixNano())
	contacts := []map[string]interface{}{
		{
			"email":             email,
			"external_id":       "ext_123",
			"timezone":          "America/New_York",
			"language":          "en-US",
			"first_name":        "Full",
			"last_name":         "Fields",
			"phone":             "+1234567890",
			"address_line_1":    "123 Main St",
			"address_line_2":    "Apt 4",
			"country":           "US",
			"postcode":          "12345",
			"state":             "NY",
			"job_title":         "Engineer",
			"custom_string_1":   "value1",
			"custom_string_2":   "value2",
			"custom_string_3":   "value3",
			"custom_string_4":   "value4",
			"custom_string_5":   "value5",
			"custom_number_1":   10.5,
			"custom_number_2":   20.5,
			"custom_number_3":   30.5,
			"custom_number_4":   40.5,
			"custom_number_5":   50.5,
			"custom_datetime_1": "2023-01-01T00:00:00Z",
			"custom_datetime_2": "2023-02-01T00:00:00Z",
			"custom_datetime_3": "2023-03-01T00:00:00Z",
			"custom_datetime_4": "2023-04-01T00:00:00Z",
			"custom_datetime_5": "2023-05-01T00:00:00Z",
			"custom_json_1":     map[string]interface{}{"key": "value1"},
			"custom_json_2":     map[string]interface{}{"key": "value2"},
			"custom_json_3":     map[string]interface{}{"key": "value3"},
			"custom_json_4":     map[string]interface{}{"key": "value4"},
			"custom_json_5":     map[string]interface{}{"key": "value5"},
		},
	}

	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
			Error  string `json:"error,omitempty"`
		} `json:"operations"`
		Error string `json:"error,omitempty"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	assert.Empty(t, importResp.Error)
	assert.Len(t, importResp.Operations, 1)
	assert.Equal(t, "create", importResp.Operations[0].Action)
	assert.Empty(t, importResp.Operations[0].Error)

	// Verify ALL fields in database
	var (
		dbExternalID, dbTimezone, dbLanguage, dbFirstName, dbLastName  string
		dbPhone, dbAddressLine1, dbAddressLine2, dbCountry, dbPostcode string
		dbState, dbJobTitle                                            string
		dbCustomString1, dbCustomString2                               string
	)
	err = workspaceDB.QueryRow(`
		SELECT 
			COALESCE(external_id, ''),
			COALESCE(timezone, ''),
			COALESCE(language, ''),
			COALESCE(first_name, ''),
			COALESCE(last_name, ''),
			COALESCE(phone, ''),
			COALESCE(address_line_1, ''),
			COALESCE(address_line_2, ''),
			COALESCE(country, ''),
			COALESCE(postcode, ''),
			COALESCE(state, ''),
			COALESCE(job_title, ''),
			COALESCE(custom_string_1, ''),
			COALESCE(custom_string_2, '')
		FROM contacts WHERE email = $1
	`, email).Scan(
		&dbExternalID, &dbTimezone, &dbLanguage, &dbFirstName, &dbLastName,
		&dbPhone, &dbAddressLine1, &dbAddressLine2, &dbCountry, &dbPostcode,
		&dbState, &dbJobTitle, &dbCustomString1, &dbCustomString2,
	)
	require.NoError(t, err)

	// Every scanned column is asserted. Seven of these were read from the row and
	// then never checked, which left the insert's column order guarded only where
	// it happened to be asserted — a transposition anywhere in the middle of the
	// list (phone into address_line_1, country into postcode) round-tripped
	// through this test unnoticed. That list is a single shared ordering used by
	// more than one insert path, so the whole of it needs pinning here.
	assert.Equal(t, "ext_123", dbExternalID)
	assert.Equal(t, "America/New_York", dbTimezone)
	assert.Equal(t, "en-US", dbLanguage)
	assert.Equal(t, "Full", dbFirstName)
	assert.Equal(t, "Fields", dbLastName)
	assert.Equal(t, "+1234567890", dbPhone)
	assert.Equal(t, "123 Main St", dbAddressLine1)
	assert.Equal(t, "Apt 4", dbAddressLine2)
	assert.Equal(t, "US", dbCountry)
	assert.Equal(t, "12345", dbPostcode)
	assert.Equal(t, "NY", dbState)
	assert.Equal(t, "Engineer", dbJobTitle)
	assert.Equal(t, "value1", dbCustomString1)
	assert.Equal(t, "value2", dbCustomString2)
}

// testDuplicateEmailsInBatch tests that duplicate emails in a single batch are handled correctly
// This specifically tests the fix for the PostgreSQL error:
// "ON CONFLICT DO UPDATE command cannot affect row a second time"
// The last occurrence of a duplicate email should be kept
func testDuplicateEmailsInBatch(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	// Create a unique email that will appear multiple times
	duplicateEmail := fmt.Sprintf("duplicate_%d@example.com", time.Now().UnixNano())
	uniqueEmail := fmt.Sprintf("unique_%d@example.com", time.Now().UnixNano())

	// Batch with the same email appearing 3 times with different data
	// The last occurrence (with "Third") should be kept
	contacts := []map[string]interface{}{
		{
			"email":      duplicateEmail,
			"first_name": "First",
			"last_name":  "Occurrence",
		},
		{
			"email":      uniqueEmail,
			"first_name": "Unique",
			"last_name":  "Contact",
		},
		{
			"email":      duplicateEmail,
			"first_name": "Second",
			"last_name":  "Occurrence",
		},
		{
			"email":      duplicateEmail,
			"first_name": "Third",  // This should be kept
			"last_name":  "Winner", // This should be kept
		},
	}

	// Import contacts - this would fail before the fix with:
	// "pq: ON CONFLICT DO UPDATE command cannot affect row a second time"
	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var importResp struct {
		Operations []struct {
			Email  string `json:"email"`
			Action string `json:"action"`
			Error  string `json:"error,omitempty"`
		} `json:"operations"`
		Error string `json:"error,omitempty"`
	}
	err = json.NewDecoder(resp.Body).Decode(&importResp)
	require.NoError(t, err)

	// Should have no error
	assert.Empty(t, importResp.Error)

	// Should have 2 operations (deduplicated from 4 contacts)
	assert.Len(t, importResp.Operations, 2, "Should have 2 operations after deduplication")

	// All operations should be successful
	for _, op := range importResp.Operations {
		assert.Empty(t, op.Error, "Operation should not have error")
		assert.Equal(t, "create", op.Action, "Should be create for new contacts")
	}

	// Verify only one contact exists in DB for the duplicate email
	var count int
	err = workspaceDB.QueryRow(
		"SELECT COUNT(*) FROM contacts WHERE email = $1",
		duplicateEmail,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Should have exactly one contact for duplicate email")

	// Verify the data matches the LAST occurrence (first_name="Third", last_name="Winner")
	var firstName, lastName string
	err = workspaceDB.QueryRow(
		"SELECT first_name, last_name FROM contacts WHERE email = $1",
		duplicateEmail,
	).Scan(&firstName, &lastName)
	require.NoError(t, err)
	assert.Equal(t, "Third", firstName, "Should have last occurrence's first_name")
	assert.Equal(t, "Winner", lastName, "Should have last occurrence's last_name")

	// Verify the unique contact also exists
	err = workspaceDB.QueryRow(
		"SELECT first_name, last_name FROM contacts WHERE email = $1",
		uniqueEmail,
	).Scan(&firstName, &lastName)
	require.NoError(t, err)
	assert.Equal(t, "Unique", firstName)
	assert.Equal(t, "Contact", lastName)
}

// bulkImportOperation is one entry of the contacts.import response.
type bulkImportOperation struct {
	Email  string `json:"email"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}

// importContactBatch sends contacts through contacts.import and returns the
// operations the response reports.
func importContactBatch(t *testing.T, client *testutil.APIClient, contacts []map[string]interface{}) []bulkImportOperation {
	t.Helper()

	resp, err := client.BatchImportContacts(contacts, nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var importResp struct {
		Operations []bulkImportOperation `json:"operations"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&importResp))
	return importResp.Operations
}

// requireAllImported fails unless every operation is the given action, without error.
func requireAllImported(t *testing.T, ops []bulkImportOperation, action string, want int) {
	t.Helper()

	require.Len(t, ops, want)
	for _, op := range ops {
		require.Empty(t, op.Error, "import of %s failed", op.Email)
		require.Equal(t, action, op.Action, "action for %s", op.Email)
	}
}

// readContactDates returns the created_at and updated_at stored for a contact.
func readContactDates(t *testing.T, workspaceDB *sql.DB, email string) (createdAt, updatedAt time.Time) {
	t.Helper()

	err := workspaceDB.QueryRow(
		"SELECT created_at, updated_at FROM contacts WHERE email = $1",
		email,
	).Scan(&createdAt, &updatedAt)
	require.NoError(t, err)
	return createdAt, updatedAt
}

// testReimportCreatedAt covers contacts that already exist: a re-import that
// does not name a created_at keeps the stored one, one that does replaces it,
// and the two can sit in the same batch. updated_at moves on every re-import.
func testReimportCreatedAt(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB) {
	stamp := time.Now().UnixNano()
	emailA := fmt.Sprintf("reimport_created_a_%d@example.com", stamp)
	emailB := fmt.Sprintf("reimport_created_b_%d@example.com", stamp)

	// First import, no created_at: both are created now.
	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": emailA, "first_name": "First"},
		{"email": emailB, "first_name": "First"},
	}), "create", 2)
	createdA, updatedA := readContactDates(t, workspaceDB, emailA)
	createdB, updatedB := readContactDates(t, workspaceDB, emailB)
	assert.WithinDuration(t, time.Now(), createdA, 2*time.Minute)
	assert.WithinDuration(t, time.Now(), createdB, 2*time.Minute)

	// Re-import without created_at, as the player sync does every few minutes:
	// created_at stays where it was and updated_at moves.
	time.Sleep(200 * time.Millisecond)
	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": emailA, "first_name": "Second"},
		{"email": emailB, "first_name": "Second"},
	}), "update", 2)
	reCreatedA, reUpdatedA := readContactDates(t, workspaceDB, emailA)
	reCreatedB, reUpdatedB := readContactDates(t, workspaceDB, emailB)
	assert.True(t, reCreatedA.Equal(createdA), "created_at was %s, a re-import without one left %s", createdA, reCreatedA)
	assert.True(t, reCreatedB.Equal(createdB), "created_at was %s, a re-import without one left %s", createdB, reCreatedB)
	assert.True(t, reUpdatedA.After(updatedA), "updated_at moves on a re-import")
	assert.True(t, reUpdatedB.After(updatedB), "updated_at moves on a re-import")

	// One batch with an explicit, older created_at for A and none for B: A takes
	// the date it was given exactly, B keeps the one it has.
	older := time.Date(2019, 5, 6, 7, 8, 9, 0, time.UTC)
	time.Sleep(200 * time.Millisecond)
	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": emailA, "first_name": "Third", "created_at": older.Format(time.RFC3339)},
		{"email": emailB, "first_name": "Third"},
	}), "update", 2)
	thirdCreatedA, thirdUpdatedA := readContactDates(t, workspaceDB, emailA)
	thirdCreatedB, _ := readContactDates(t, workspaceDB, emailB)
	assert.True(t, thirdCreatedA.Equal(older), "created_at supplied as %s was stored as %s", older, thirdCreatedA)
	assert.True(t, thirdCreatedB.Equal(createdB), "created_at was %s, a batch row without one left %s", createdB, thirdCreatedB)
	assert.True(t, thirdUpdatedA.After(reUpdatedA), "updated_at moves on a re-import")

	// A later re-import without created_at keeps the date that was supplied.
	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": emailA, "first_name": "Fourth"},
	}), "update", 1)
	fourthCreatedA, _ := readContactDates(t, workspaceDB, emailA)
	assert.True(t, fourthCreatedA.Equal(older), "created_at was %s, a re-import without one left %s", older, fourthCreatedA)
}

// testNewContactCreatedAt covers contacts that do not exist yet: one that names
// a created_at is stored with it, one that does not is created now.
func testNewContactCreatedAt(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB) {
	stamp := time.Now().UnixNano()
	datedEmail := fmt.Sprintf("new_dated_%d@example.com", stamp)
	undatedEmail := fmt.Sprintf("new_undated_%d@example.com", stamp)
	supplied := time.Date(2018, 1, 2, 3, 4, 5, 0, time.UTC)

	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": datedEmail, "created_at": supplied.Format(time.RFC3339)},
		{"email": undatedEmail},
	}), "create", 2)

	datedCreated, datedUpdated := readContactDates(t, workspaceDB, datedEmail)
	assert.True(t, datedCreated.Equal(supplied), "created_at supplied as %s was stored as %s", supplied, datedCreated)
	assert.WithinDuration(t, time.Now(), datedUpdated, 2*time.Minute, "updated_at is not taken from created_at")

	undatedCreated, _ := readContactDates(t, workspaceDB, undatedEmail)
	assert.WithinDuration(t, time.Now(), undatedCreated, 2*time.Minute)
}

// testInvalidCreatedAtInBatch checks that a created_at that is not a time fails
// its own contact, at its index, and nothing else in the batch.
func testInvalidCreatedAtInBatch(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB) {
	stamp := time.Now().UnixNano()
	goodEmail := fmt.Sprintf("created_good_%d@example.com", stamp)
	badEmail := fmt.Sprintf("created_bad_%d@example.com", stamp)

	ops := importContactBatch(t, client, []map[string]interface{}{
		{"email": goodEmail},
		{"email": badEmail, "created_at": "yesterday"},
	})
	require.Len(t, ops, 2)

	var created, failed []bulkImportOperation
	for _, op := range ops {
		if op.Action == "error" {
			failed = append(failed, op)
		} else {
			created = append(created, op)
		}
	}
	require.Len(t, failed, 1)
	require.Len(t, created, 1)
	assert.Contains(t, failed[0].Error, "invalid contact at index 1")
	assert.Equal(t, goodEmail, created[0].Email)
	assert.Equal(t, "create", created[0].Action)

	var count int
	require.NoError(t, workspaceDB.QueryRow("SELECT COUNT(*) FROM contacts WHERE email = $1", badEmail).Scan(&count))
	assert.Equal(t, 0, count, "the contact with the bad created_at is not stored")
}

// testUpsertCreatedAt checks contacts.upsert, which parses its contact through
// the same FromJSON: a new contact sent with a created_at is stored with it.
func testUpsertCreatedAt(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	email := fmt.Sprintf("upsert_created_%d@example.com", time.Now().UnixNano())
	supplied := time.Date(2017, 11, 12, 13, 14, 15, 0, time.UTC)

	resp, err := client.CreateContact(map[string]interface{}{
		"workspace_id": workspaceID,
		"contact": map[string]interface{}{
			"email":      email,
			"created_at": supplied.Format(time.RFC3339),
		},
	})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	createdAt, _ := readContactDates(t, workspaceDB, email)
	assert.True(t, createdAt.Equal(supplied), "created_at supplied as %s was stored as %s", supplied, createdAt)
}

// testUpsertExistingContactCreatedAt pins contacts.upsert on a contact that
// already exists: its fields change and its created_at does not, even when the
// request names an older one. Unlike contacts.import, the single upsert never
// writes created_at on an update.
func testUpsertExistingContactCreatedAt(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	email := fmt.Sprintf("upsert_existing_%d@example.com", time.Now().UnixNano())

	upsert := func(contact map[string]interface{}) string {
		t.Helper()

		resp, err := client.CreateContact(map[string]interface{}{
			"workspace_id": workspaceID,
			"contact":      contact,
		})
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var op bulkImportOperation
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&op))
		require.Empty(t, op.Error)
		return op.Action
	}

	require.Equal(t, "create", upsert(map[string]interface{}{"email": email, "first_name": "First"}))
	createdAt, _ := readContactDates(t, workspaceDB, email)
	assert.WithinDuration(t, time.Now(), createdAt, 2*time.Minute)

	older := time.Date(2016, 4, 5, 6, 7, 8, 0, time.UTC)
	require.Equal(t, "update", upsert(map[string]interface{}{
		"email":      email,
		"first_name": "Second",
		"created_at": older.Format(time.RFC3339),
	}))

	var firstName string
	require.NoError(t, workspaceDB.QueryRow("SELECT first_name FROM contacts WHERE email = $1", email).Scan(&firstName))
	assert.Equal(t, "Second", firstName, "the update changed first_name")

	storedCreatedAt, _ := readContactDates(t, workspaceDB, email)
	assert.True(t, storedCreatedAt.Equal(createdAt), "created_at was %s, an upsert naming %s left %s", createdAt, older, storedCreatedAt)
}

// testImportDedupeKeepsLastCreatedAt covers one import batch that holds a dated
// row for an existing contact, an undated row for the same address written in
// another case, and enough other contacts to span more than one insert chunk.
// Emails are normalized before the batch is deduplicated, the last occurrence
// wins, and that occurrence supplied no created_at, so the stored one stays.
// There are as many other contacts as one insert chunk holds, so once the
// batch is deduplicated the contact's row falls in the second chunk. The
// service takes the whole batch in one call.
func testImportDedupeKeepsLastCreatedAt(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB) {
	stamp := time.Now().UnixNano()
	email := fmt.Sprintf("norm_dedup_%d@example.com", stamp)
	upperEmail := fmt.Sprintf("NORM_DEDUP_%d@example.com", stamp)
	const others = domain.BulkImportChunkSize

	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": email, "first_name": "First"},
	}), "create", 1)
	createdAt, _ := readContactDates(t, workspaceDB, email)

	contacts := make([]map[string]interface{}, 0, others+2)
	contacts = append(contacts, map[string]interface{}{
		"email":      upperEmail,
		"first_name": "Dated",
		"created_at": time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	for i := 0; i < others; i++ {
		contacts = append(contacts, map[string]interface{}{
			"email": fmt.Sprintf("norm_dedup_other_%d_%d@example.com", stamp, i),
		})
	}
	contacts = append(contacts, map[string]interface{}{"email": email, "first_name": "Undated"})

	ops := importContactBatch(t, client, contacts)
	require.Len(t, ops, others+1, "the two rows for one address are one operation")
	creates, updates := 0, 0
	for _, op := range ops {
		require.Empty(t, op.Error, "import of %s failed", op.Email)
		switch op.Action {
		case "create":
			creates++
		case "update":
			updates++
			assert.Equal(t, email, op.Email)
		}
	}
	assert.Equal(t, others, creates)
	assert.Equal(t, 1, updates)

	var count int
	require.NoError(t, workspaceDB.QueryRow(
		"SELECT COUNT(*) FROM contacts WHERE email LIKE $1",
		fmt.Sprintf("norm_dedup_other_%d_%%@example.com", stamp),
	).Scan(&count))
	assert.Equal(t, others, count, "every other contact in the batch exists")

	require.NoError(t, workspaceDB.QueryRow(
		"SELECT COUNT(*) FROM contacts WHERE lower(email) = $1", email,
	).Scan(&count))
	assert.Equal(t, 1, count, "the two spellings are one contact")

	var firstName string
	require.NoError(t, workspaceDB.QueryRow("SELECT first_name FROM contacts WHERE email = $1", email).Scan(&firstName))
	assert.Equal(t, "Undated", firstName, "the last occurrence wins")

	storedCreatedAt, _ := readContactDates(t, workspaceDB, email)
	assert.True(t, storedCreatedAt.Equal(createdAt), "created_at was %s, the dated row it was deduplicated against left %s", createdAt, storedCreatedAt)
}

// testCreatedAtEndOfYear9999ReadsBack imports a created_at 100 nanoseconds
// before year 10000. PostgreSQL keeps microseconds and rounds the rest, so
// without the truncation in FromJSON it would be stored as year 10000 and every
// JSON read of the contact would fail. The contact is deleted at the end so a
// created_at in year 9999 cannot sort first in the list calls of other subtests.
func testCreatedAtEndOfYear9999ReadsBack(t *testing.T, client *testutil.APIClient, workspaceDB *sql.DB, workspaceID string) {
	email := fmt.Sprintf("created_year_9999_%d@example.com", time.Now().UnixNano())
	defer func() {
		resp, err := client.Post("/api/contacts.delete", map[string]interface{}{
			"workspace_id": workspaceID,
			"email":        email,
		})
		if assert.NoError(t, err) {
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, http.StatusOK, resp.StatusCode, "the year 9999 contact is deleted at the end")
		}
	}()

	requireAllImported(t, importContactBatch(t, client, []map[string]interface{}{
		{"email": email, "created_at": "9999-12-31T23:59:59.9999999Z"},
	}), "create", 1)

	storedCreatedAt, _ := readContactDates(t, workspaceDB, email)
	assert.True(t, storedCreatedAt.Equal(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)),
		"created_at was stored as %s", storedCreatedAt)

	resp, err := client.GetContactByEmail(email)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Contact struct {
			Email     string    `json:"email"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"contact"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body), "the contact reads back as JSON")
	assert.Equal(t, email, body.Contact.Email)
	assert.Equal(t, 9999, body.Contact.CreatedAt.UTC().Year())
}

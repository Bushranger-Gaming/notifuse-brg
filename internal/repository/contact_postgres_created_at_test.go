package repository

import (
	"context"
	"database/sql/driver"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

// createdAtArg is where contactInsertArgs puts created_at within one row's
// values: the 35th insert column.
const createdAtArg = 34

// argCapture accepts any bound value and records it.
type argCapture struct{ into *driver.Value }

func (a argCapture) Match(v driver.Value) bool {
	*a.into = v
	return true
}

// runBulkUpsert runs BulkUpsertContacts against sqlmock and returns the SQL it
// sent, whitespace collapsed, and every value it bound to it. The query only
// runs when the bound values are one per row column plus the one trailing
// parameter; any other count is an error from the mock.
func runBulkUpsert(t *testing.T, contacts []*domain.Contact) (string, []driver.Value) {
	t.Helper()

	var sent string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error {
		sent = strings.Join(strings.Fields(actual), " ")
		return nil
	})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws").Return(db, nil)

	bound := make([]driver.Value, len(contacts)*contactInsertFieldCount+1)
	matchers := make([]driver.Value, len(bound))
	for i := range bound {
		matchers[i] = argCapture{&bound[i]}
	}
	rows := sqlmock.NewRows([]string{"email", "is_new"})
	for _, c := range contacts {
		rows.AddRow(c.Email, false)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO contacts").WithArgs(matchers...).WillReturnRows(rows)
	mock.ExpectCommit()

	_, err = NewContactRepository(workspaceRepo).BulkUpsertContacts(context.Background(), "ws", contacts)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	return sent, bound
}

func TestBulkUpsertContacts_CreatedAt(t *testing.T) {
	supplied := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)

	t.Run("a conflict replaces the stored created_at only for a row that supplied one", func(t *testing.T) {
		query, args := runBulkUpsert(t, []*domain.Contact{
			{Email: "dated@example.com", CreatedAt: supplied},
			{Email: "undated@example.com"},
		})

		// The emails that supplied a created_at are the last parameter.
		last := len(args) - 1
		assert.Contains(t, query, "created_at = CASE WHEN EXCLUDED.email = ANY($"+strconv.Itoa(last+1)+"::text[]) THEN EXCLUDED.created_at ELSE contacts.created_at END")
		assert.Equal(t, `{"dated@example.com"}`, args[last])
		assert.NotContains(t, query, "created_at = EXCLUDED.created_at",
			"writing the proposed row back reset every stored date to the import time")
	})

	t.Run("no row supplying a created_at replaces none", func(t *testing.T) {
		query, args := runBulkUpsert(t, []*domain.Contact{
			{Email: "a@example.com"},
			{Email: "b@example.com"},
		})

		assert.Contains(t, query, "THEN EXCLUDED.created_at ELSE contacts.created_at END")
		assert.Equal(t, `{}`, args[len(args)-1])
	})

	t.Run("every row supplying a created_at replaces every one", func(t *testing.T) {
		_, args := runBulkUpsert(t, []*domain.Contact{
			{Email: "a@example.com", CreatedAt: supplied},
			{Email: "b@example.com", CreatedAt: supplied.Add(time.Hour)},
		})

		assert.Equal(t, `{"a@example.com","b@example.com"}`, args[len(args)-1])
	})

	t.Run("a new contact is created at the date it supplied, else now", func(t *testing.T) {
		before := time.Now().UTC().Add(-time.Second)
		_, args := runBulkUpsert(t, []*domain.Contact{
			{Email: "dated@example.com", CreatedAt: supplied},
			{Email: "undated@example.com"},
		})
		after := time.Now().UTC().Add(time.Second)

		datedAt, ok := args[createdAtArg].(time.Time)
		require.True(t, ok, "created_at is bound as a time")
		assert.True(t, datedAt.Equal(supplied))

		undatedAt, ok := args[contactInsertFieldCount+createdAtArg].(time.Time)
		require.True(t, ok, "created_at is bound as a time")
		assert.False(t, undatedAt.Before(before), "an undated new contact is created now")
		assert.False(t, undatedAt.After(after), "an undated new contact is created now")
	})

	t.Run("created_at is bound at the position of its insert column", func(t *testing.T) {
		columns := strings.Split(contactInsertColumns, ",")
		for i := range columns {
			columns[i] = strings.TrimSpace(columns[i])
		}
		require.Equal(t, "created_at", columns[createdAtArg])
	})
}

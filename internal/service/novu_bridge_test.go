package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNovuBridge_Configured(t *testing.T) {
	var nilBridge *NovuBridge
	assert.False(t, nilBridge.Configured())
	assert.False(t, NewNovuBridge("", "", nil).Configured())
	assert.False(t, NewNovuBridge("http://bridge:8090", "", nil).Configured())
	assert.False(t, NewNovuBridge("", "secret", nil).Configured())
	assert.False(t, NewNovuBridge("  ", "secret", nil).Configured())
	assert.True(t, NewNovuBridge("http://bridge:8090", "secret", nil).Configured())
}

func TestNovuBridge_ListWorkflows(t *testing.T) {
	const listBody = `{"workflows":[
		{"id":"crm-message","name":"CRM · Message","uses_text":true},
		{"id":"crm-welcome-inapp","name":"CRM · Welcome","uses_text":false}
	]}`

	t.Run("lists the workflows with the bearer secret", func(t *testing.T) {
		var gotPath, gotMethod, gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
			_, _ = w.Write([]byte(listBody))
		}))
		defer server.Close()

		workflows, err := NewNovuBridge(server.URL, testNovuSecret, nil).ListWorkflows(context.Background())

		require.NoError(t, err)
		assert.Equal(t, http.MethodGet, gotMethod)
		assert.Equal(t, "/novu/workflows", gotPath)
		assert.Equal(t, "Bearer "+testNovuSecret, gotAuth)
		assert.Equal(t, []domain.NovuWorkflow{
			{ID: "crm-message", Name: "CRM · Message", UsesText: true},
			{ID: "crm-welcome-inapp", Name: "CRM · Welcome", UsesText: false},
		}, workflows)
	})

	t.Run("keeps a path prefix of the address", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"workflows":[]}`))
		}))
		defer server.Close()

		workflows, err := NewNovuBridge(server.URL+"/bridge/", testNovuSecret, nil).ListWorkflows(context.Background())

		require.NoError(t, err)
		assert.Equal(t, "/bridge/novu/workflows", gotPath)
		assert.NotNil(t, workflows)
		assert.Empty(t, workflows)
	})

	t.Run("leaves out the workflows a node could not carry and names the unnamed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflows":[
				{"id":"Has Spaces","name":"Bad","uses_text":true},
				{"id":"","name":"No id"},
				{"id":"../x","name":"Traversal"},
				{"id":"nameless","uses_text":true},
				{"id":"fine","name":"Fine"}
			]}`))
		}))
		defer server.Close()

		workflows, err := NewNovuBridge(server.URL, testNovuSecret, nil).ListWorkflows(context.Background())

		require.NoError(t, err)
		assert.Equal(t, []domain.NovuWorkflow{
			{ID: "nameless", Name: "nameless", UsesText: true},
			{ID: "fine", Name: "Fine"},
		}, workflows)
	})

	t.Run("not set up", func(t *testing.T) {
		for name, bridge := range map[string]*NovuBridge{
			"nil":          nil,
			"no address":   NewNovuBridge("", testNovuSecret, nil),
			"no secret":    NewNovuBridge("http://bridge:8090", "", nil),
			"both missing": NewNovuBridge("", "", nil),
		} {
			workflows, err := bridge.ListWorkflows(context.Background())

			require.Error(t, err, name)
			assert.Nil(t, workflows, name)
			var unavailable *domain.NovuBridgeUnavailableError
			require.True(t, errors.As(err, &unavailable), name)
			assert.Contains(t, unavailable.Message, "not set up", name)
			assert.Contains(t, unavailable.Message, "BRG_NOVU_BRIDGE_URL", name)
			assert.Contains(t, unavailable.Message, "BRG_NOVU_BRIDGE_SECRET", name)
		}
	})

	t.Run("an address that is not http", func(t *testing.T) {
		_, err := NewNovuBridge("bridge:8090", testNovuSecret, nil).ListWorkflows(context.Background())
		var unavailable *domain.NovuBridgeUnavailableError
		require.True(t, errors.As(err, &unavailable))
		assert.Contains(t, unavailable.Message, "http:// or https://")
	})

	t.Run("the bridge refuses the secret", func(t *testing.T) {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			_, err := NewNovuBridge(server.URL, testNovuSecret, nil).ListWorkflows(context.Background())
			server.Close()

			var unavailable *domain.NovuBridgeUnavailableError
			require.True(t, errors.As(err, &unavailable), "status %d", status)
			assert.Contains(t, unavailable.Message, "refused the configured secret")
			assert.NotContains(t, unavailable.Message, testNovuSecret)
		}
	})

	t.Run("the bridge answers an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"detail":"novu down"}`))
		}))
		defer server.Close()

		_, err := NewNovuBridge(server.URL, testNovuSecret, nil).ListWorkflows(context.Background())

		var unavailable *domain.NovuBridgeUnavailableError
		require.True(t, errors.As(err, &unavailable))
		assert.Equal(t, "The Novu bridge answered HTTP 503", unavailable.Message)
	})

	t.Run("the bridge answers something else than a list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<html>hello</html>`))
		}))
		defer server.Close()

		_, err := NewNovuBridge(server.URL, testNovuSecret, nil).ListWorkflows(context.Background())

		var unavailable *domain.NovuBridgeUnavailableError
		require.True(t, errors.As(err, &unavailable))
		assert.Contains(t, unavailable.Message, "not a workflow list")
	})

	t.Run("the bridge is down", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		closedURL := server.URL
		server.Close()

		_, err := NewNovuBridge(closedURL, testNovuSecret, nil).ListWorkflows(context.Background())

		var unavailable *domain.NovuBridgeUnavailableError
		require.True(t, errors.As(err, &unavailable))
		assert.Equal(t, "Could not reach the Novu bridge", unavailable.Message)
		assert.NotContains(t, unavailable.Message, closedURL, "the address stays on the server")
	})

	t.Run("does not follow a redirect", func(t *testing.T) {
		var elsewhereCalls atomic.Int32
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			elsewhereCalls.Add(1)
			_, _ = w.Write([]byte(listBody))
		}))
		defer elsewhere.Close()
		redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL, http.StatusFound)
		}))
		defer redirecting.Close()

		_, err := NewNovuBridge(redirecting.URL, testNovuSecret, nil).ListWorkflows(context.Background())

		require.Error(t, err)
		assert.Equal(t, int32(0), elsewhereCalls.Load())
	})

	t.Run("reads no more than the limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflows":[{"id":"a","name":"` + strings.Repeat("x", novuBridgeListLimit+10) + `"}]}`))
		}))
		defer server.Close()

		_, err := NewNovuBridge(server.URL, testNovuSecret, nil).ListWorkflows(context.Background())

		var unavailable *domain.NovuBridgeUnavailableError
		require.True(t, errors.As(err, &unavailable))
	})
}

func TestAutomationService_ListNovuWorkflows(t *testing.T) {
	workspaceID := "workspace-123"
	admin := &domain.UserWorkspace{UserID: "user-1", WorkspaceID: workspaceID, Role: "admin", Permissions: domain.FullPermissions}

	newService := func(t *testing.T, bridge *NovuBridge) (*AutomationService, *mocks.MockAuthService, *pkgmocks.MockLogger) {
		ctrl := gomock.NewController(t)
		authService := mocks.NewMockAuthService(ctrl)
		logger := pkgmocks.NewMockLogger(ctrl)
		s := NewAutomationService(mocks.NewMockAutomationRepository(ctrl), authService, nil, logger, false)
		s.SetNovuBridge(bridge)
		return s, authService, logger
	}

	t.Run("lists the bridge's workflows", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflows":[{"id":"crm-message","name":"CRM · Message","uses_text":true}]}`))
		}))
		defer server.Close()
		s, authService, _ := newService(t, NewNovuBridge(server.URL, testNovuSecret, nil))
		ctx := context.Background()
		authService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).Return(ctx, &domain.User{ID: "user-1"}, admin, nil)

		workflows, err := s.ListNovuWorkflows(ctx, workspaceID)

		require.NoError(t, err)
		assert.Equal(t, []domain.NovuWorkflow{{ID: "crm-message", Name: "CRM · Message", UsesText: true}}, workflows)
	})

	t.Run("a member who can only read automations may list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflows":[]}`))
		}))
		defer server.Close()
		s, authService, _ := newService(t, NewNovuBridge(server.URL, testNovuSecret, nil))
		ctx := context.Background()
		reader := &domain.UserWorkspace{UserID: "user-2", WorkspaceID: workspaceID, Role: "member", Permissions: domain.UserPermissions{
			domain.PermissionResourceAutomations: {Read: true, Write: false},
		}}
		authService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).Return(ctx, &domain.User{ID: "user-2"}, reader, nil)

		_, err := s.ListNovuWorkflows(ctx, workspaceID)

		require.NoError(t, err)
	})

	t.Run("a member without read access to automations is refused before the bridge is asked", func(t *testing.T) {
		var asked atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
		}))
		defer server.Close()
		s, authService, _ := newService(t, NewNovuBridge(server.URL, testNovuSecret, nil))
		ctx := context.Background()
		stranger := &domain.UserWorkspace{UserID: "user-3", WorkspaceID: workspaceID, Role: "member", Permissions: domain.UserPermissions{
			domain.PermissionResourceContacts: {Read: true, Write: true},
		}}
		authService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).Return(ctx, &domain.User{ID: "user-3"}, stranger, nil)

		workflows, err := s.ListNovuWorkflows(ctx, workspaceID)

		assert.Nil(t, workflows)
		var permErr *domain.PermissionError
		require.True(t, errors.As(err, &permErr), "want a permission error, got %v", err)
		assert.Equal(t, domain.PermissionResourceAutomations, permErr.Resource)
		assert.Equal(t, domain.PermissionTypeRead, permErr.Permission)
		assert.Equal(t, int32(0), asked.Load())
	})

	t.Run("a failed authentication is reported, with no call to the bridge", func(t *testing.T) {
		s, authService, _ := newService(t, NewNovuBridge("http://bridge:8090", testNovuSecret, nil))
		ctx := context.Background()
		authService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).Return(ctx, nil, nil, errors.New("token expired"))

		_, err := s.ListNovuWorkflows(ctx, workspaceID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to authenticate")
	})

	t.Run("a bridge that is not set up answers unavailable, and the call is logged", func(t *testing.T) {
		s, authService, logger := newService(t, nil)
		ctx := context.Background()
		authService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).Return(ctx, &domain.User{ID: "user-1"}, admin, nil)
		logger.EXPECT().WithField("workspace_id", workspaceID).Return(logger)
		logger.EXPECT().Warn(gomock.Any())

		workflows, err := s.ListNovuWorkflows(ctx, workspaceID)

		assert.Nil(t, workflows)
		var unavailable *domain.NovuBridgeUnavailableError
		require.True(t, errors.As(err, &unavailable))
		assert.Contains(t, unavailable.Message, "not set up")
	})
}

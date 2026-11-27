package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
)

// The bridge of novu_message nodes is an operator setting on the private network of the
// deployment, so it is reached with the default WEBHOOK_DELIVERY_ALLOW_PRIVATE_HOSTS=false, which
// leaves the guard of webhook nodes (see TestNewOutgoingWebhookClient) in place. It does not
// follow redirects, whatever tracing does to the client.
func TestNewNovuBridge(t *testing.T) {
	var hits int32
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		assert.Equal(t, "Bearer the-secret", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"workflows":[{"id":"crm-message","name":"CRM · Message","uses_text":true}]}`))
	}))
	defer bridge.Close()

	cfg := func(url, secret string, tracing bool) *config.Config {
		return &config.Config{
			NovuBridge: config.NovuBridgeConfig{URL: url, Secret: secret},
			Tracing:    config.TracingConfig{Enabled: tracing},
		}
	}

	for _, tracing := range []bool{false, true} {
		t.Run("reaches a private address with the guard on for webhooks", func(t *testing.T) {
			c := cfg(bridge.URL, "the-secret", tracing)
			require.False(t, c.Webhook.AllowPrivateDeliveryHosts)
			before := atomic.LoadInt32(&hits)

			workflows, err := newNovuBridge(c, 5*time.Second).ListWorkflows(context.Background())

			require.NoError(t, err)
			require.Len(t, workflows, 1)
			assert.Equal(t, "crm-message", workflows[0].ID)
			assert.Equal(t, before+1, atomic.LoadInt32(&hits))
		})

		t.Run("does not follow a redirect", func(t *testing.T) {
			var elsewhere int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&elsewhere, 1)
			}))
			defer target.Close()
			redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			defer redirecting.Close()

			_, err := newNovuBridge(cfg(redirecting.URL, "the-secret", tracing), 5*time.Second).ListWorkflows(context.Background())

			require.Error(t, err)
			assert.Equal(t, int32(0), atomic.LoadInt32(&elsewhere))
		})
	}

	t.Run("an unset bridge is still a bridge, that is not configured", func(t *testing.T) {
		assert.False(t, newNovuBridge(cfg("", "", false), time.Second).Configured())
		assert.False(t, newNovuBridge(cfg(bridge.URL, "", false), time.Second).Configured())
		assert.False(t, newNovuBridge(cfg("", "the-secret", false), time.Second).Configured())
		assert.True(t, newNovuBridge(cfg(bridge.URL, "the-secret", false), time.Second).Configured())
	})
}

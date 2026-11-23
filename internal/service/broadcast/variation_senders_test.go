package broadcast

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckVariationSenders(t *testing.T) {
	emailProvider := &domain.EmailProvider{
		Kind: domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{
			{ID: "sender-default", Email: "default@example.com", Name: "Default", IsDefault: true},
			{ID: "sender-two", Email: "two@example.com", Name: "Two"},
		},
	}
	variationA := domain.BroadcastVariation{VariationName: "A", TemplateID: "tpl-a", SenderID: "sender-two"}
	variationB := domain.BroadcastVariation{VariationName: "B", TemplateID: "tpl-b", SenderID: "sender-gone"}
	variationBKept := domain.BroadcastVariation{VariationName: "B", TemplateID: "tpl-b"}
	winnerA, winnerB := "tpl-a", "tpl-b"

	tests := []struct {
		name            string
		provider        *domain.EmailProvider
		enabled         bool
		variations      []domain.BroadcastVariation
		winningTemplate *string
		wantMissing     string
	}{
		{
			name:       "every variation has its sender",
			provider:   emailProvider,
			enabled:    true,
			variations: []domain.BroadcastVariation{variationA, variationBKept},
		},
		{
			name:        "A/B test reports the second variation whose sender is gone",
			provider:    emailProvider,
			enabled:     true,
			variations:  []domain.BroadcastVariation{variationA, variationB},
			wantMissing: "tpl-b",
		},
		{
			name:        "single variation whose sender is gone",
			provider:    emailProvider,
			variations:  []domain.BroadcastVariation{variationB},
			wantMissing: "tpl-b",
		},
		{
			name:            "winner phase checks the winning variation",
			provider:        emailProvider,
			enabled:         true,
			variations:      []domain.BroadcastVariation{variationA, variationB},
			winningTemplate: &winnerB,
			wantMissing:     "tpl-b",
		},
		{
			// Only the winning template is loaded and sent in the winner phase, so a sender gone
			// from the losing variation does not stop it
			name:            "winner phase does not check the losing variation",
			provider:        emailProvider,
			enabled:         true,
			variations:      []domain.BroadcastVariation{variationA, variationB},
			winningTemplate: &winnerA,
		},
		{
			// The winner is found by its template, not by its place among the variations
			name:            "winner phase does not check a losing variation listed before the winner",
			provider:        emailProvider,
			enabled:         true,
			variations:      []domain.BroadcastVariation{variationB, variationA},
			winningTemplate: &winnerA,
		},
		{
			// The orchestrator loads the template of every variation and the draw among them is not
			// limited to the first, so a second variation is checked with A/B testing off too
			name:        "without an A/B test the second variation is checked",
			provider:    emailProvider,
			variations:  []domain.BroadcastVariation{variationA, variationB},
			wantMissing: "tpl-b",
		},
		{
			name:        "without an A/B test the first variation is checked",
			provider:    emailProvider,
			variations:  []domain.BroadcastVariation{variationB, variationA},
			wantMissing: "tpl-b",
		},
		{
			name:       "without an A/B test every variation has its sender",
			provider:   emailProvider,
			variations: []domain.BroadcastVariation{variationA, variationBKept},
		},
		{
			name:       "variation without a sender override is not looked up",
			provider:   emailProvider,
			variations: []domain.BroadcastVariation{variationBKept},
		},
		{
			name:     "no variations",
			provider: emailProvider,
		},
		{
			name:       "no provider to check against",
			provider:   nil,
			variations: []domain.BroadcastVariation{variationB},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			broadcast := &domain.Broadcast{
				ID:              "broadcast-1",
				TestSettings:    domain.BroadcastTestSettings{Enabled: tt.enabled, Variations: tt.variations},
				WinningTemplate: tt.winningTemplate,
			}

			err := checkVariationSenders(broadcast, tt.provider)

			if tt.wantMissing == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBroadcastShouldPause)
			assert.ErrorIs(t, err, domain.ErrSenderNotFound)
			assert.Contains(t, err.Error(), tt.wantMissing)
			assert.Contains(t, err.Error(), "sender-gone")
		})
	}
}

package broadcast

import (
	"github.com/Notifuse/notifuse/internal/domain"
)

// checkVariationSenders resolves the sender of every variation a batch can draw, once, before
// anything is sent or enqueued. Until a winner is chosen the templates the orchestrator loads and
// the draw among them are not narrowed to one variation, so every variation that names a sender the
// provider no longer has is a pause waiting to happen. Once a winner is chosen the orchestrator
// loads the winning template alone and every recipient gets it, so only the variation that sends
// it is checked: a sender gone from a losing variation is no reason to stop the winner. A variation
// whose sender is gone from the provider fails every recipient it is drawn for, and finding that
// out on the recipient it first meets would leave the recipients before it sent while the
// orchestrator has not recorded them, so they are sent again when the broadcast resumes. It returns
// the pause error for the first such variation, or nil.
func checkVariationSenders(broadcast *domain.Broadcast, emailProvider *domain.EmailProvider) error {
	if emailProvider == nil {
		return nil
	}
	for _, variation := range broadcast.TestSettings.Variations {
		if variation.SenderID == "" {
			continue
		}
		if broadcast.WinningTemplate != nil && variation.TemplateID != *broadcast.WinningTemplate {
			continue
		}
		// Only the override is looked up: a variation without one sends with its template's sender,
		// which is resolved with the template of each recipient's language
		if _, err := emailProvider.ResolveSender(variation.SenderID, ""); err != nil {
			return errVariationSenderMissing(variation.TemplateID, err)
		}
	}
	return nil
}

package render

import "testing"

// corpus pins representative router messages: neutral CommonMark in, the
// MarkdownV2 the Telegram bot must still send out. Derived from router.go's
// pre-refactor literals. This is the regression gate for the router rewrite.
var corpus = []struct{ neutral, wantTelegram string }{
	{
		"You have no agents yet. Use /agent create <name> to build one.",
		"You have no agents yet\\. Use /agent create <name\\> to build one\\.",
	},
	{
		"Hi **Ilija**! Your Telegram account is now linked. Send /help to see what you can do.",
		"Hi *Ilija*\\! Your Telegram account is now linked\\. Send /help to see what you can do\\.",
	},
	{
		"Usage: /run <agent_name>",
		"Usage: /run <agent\\_name\\>",
	},
	{
		"Agent `daily-digest` not found.",
		"Agent `daily-digest` not found\\.",
	},
}

func TestTelegramCorpus(t *testing.T) {
	for _, tc := range corpus {
		if got := RenderTelegram(tc.neutral); got != tc.wantTelegram {
			t.Errorf("RenderTelegram(%q)\n got: %q\nwant: %q", tc.neutral, got, tc.wantTelegram)
		}
	}
}

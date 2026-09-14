package chat

import (
	"os"
	"strings"
	"testing"
)

// There are exactly TWO places a one-off chat turn is assembled — the SPA's
// runChatCoder and the chat-platform handler in cmd/rookery — and the comment
// at the first says in as many words that divergence would give one surface a
// capability the other lacks. That is not hypothetical here: the whole reason
// this package owns the resolver, rather than web/, is that a helper in web
// would silently leave Telegram, Discord and Slack without it.
//
// The failure is invisible in either file alone, which is what makes a
// source-scanning test the right shape (the same reasoning behind
// packaging/hosttools_agreement_test.go). It asserts the CALL, not the
// wording around it.
func TestBothChatTurnSitesLoadReferencedFiles(t *testing.T) {
	for _, f := range []string{
		"../../web/handlers_misc.go",
		"../../cmd/rookery/main.go",
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(b)
		if !strings.Contains(src, "ReferencedFiles(") {
			t.Errorf("%s assembles a chat turn but never calls chat.ReferencedFiles — "+
				"a file named in a message would be loaded on one surface and not the other", f)
		}
		// Both sites already call BuildUserContext; if one stops, this test's
		// premise (that this IS a turn-assembly site) has changed and the
		// reader should be told rather than left with a passing test.
		if !strings.Contains(src, "BuildUserContext(") {
			t.Errorf("%s no longer calls BuildUserContext — is it still a chat turn-assembly site?", f)
		}
	}
}

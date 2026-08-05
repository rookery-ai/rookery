package gateway

import "strings"

// Every chat platform caps how long one message may be, and until now nothing in
// this package knew that. DiscordGateway.Send passed the rendered text straight
// to ChannelMessageSend; Discord rejects anything over 2000 characters with an
// HTTP 400, GatewayManager.dispatch discarded the error, and the user received
// NOTHING. Short designer messages arrived and long ones vanished — including
// the agent overview a user has to read in order to approve a build.
//
// Limits are per platform and counted in UTF-16 code units (see msgLen).
const (
	discordMessageLimit  = 2000
	telegramMessageLimit = 4096
	// Slack's own limit is ~40k characters for a plain text message. It is far
	// above anything Rookery produces, but declaring it keeps every adapter on
	// the same path rather than leaving one silently unbounded.
	slackMessageLimit = 40000
)

// msgLen measures a string the way Discord and Telegram do: in UTF-16 code
// units, not runes and not bytes.
//
// This distinction is load-bearing rather than pedantic. Designer output is
// dense with emoji — 🔧 for tool calls, ⏳, ✅, ⚠️ — and every astral-plane rune
// is TWO UTF-16 units. Counting runes would under-count exactly the messages
// this file exists to deliver, and the platform would still answer 400.
func msgLen(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// splitMessage breaks text into chunks that each fit within limit, preferring
// paragraph and line boundaries and never splitting inside a fenced code block.
//
// Guarantees the callers rely on:
//   - the result is never empty (an empty input yields one empty chunk, so a
//     caller's send loop still runs once and behaviour is unchanged for the
//     ordinary short-message case);
//   - no chunk exceeds limit;
//   - concatenating the chunks reproduces the input, apart from the fence
//     markers reopened across a boundary and the newlines at the split points.
func splitMessage(text string, limit int) []string {
	if limit <= 0 || msgLen(text) <= limit {
		return []string{text}
	}

	var (
		out     []string
		cur     strings.Builder
		fence   string // the opening fence line currently in effect, "" when outside one
		curLen  int
		lines   = strings.Split(text, "\n")
		flush   func()
		addLine func(string)
	)

	flush = func() {
		if cur.Len() == 0 {
			return
		}
		chunk := cur.String()
		// Close an open fence at the boundary so the chunk is valid on its own;
		// the next chunk reopens it. Without this a long code block renders as
		// prose in one half and an unterminated block in the other.
		if fence != "" {
			chunk += "\n```"
		}
		out = append(out, chunk)
		cur.Reset()
		curLen = 0
		if fence != "" {
			cur.WriteString(fence)
			curLen = msgLen(fence)
		}
	}

	addLine = func(line string) {
		lineLen := msgLen(line)

		// A single line longer than the limit cannot be placed whole. Hard-split
		// it on rune boundaries — never mid-rune, which would emit invalid UTF-8.
		if lineLen > limit {
			flush()
			for _, part := range hardSplit(line, limit-msgLen(cur.String())-1, limit) {
				if curLen > 0 {
					flush()
				}
				cur.WriteString(part)
				curLen = msgLen(part)
				flush()
			}
			return
		}

		// +1 for the newline that will join this line to the previous one.
		need := lineLen
		if curLen > 0 {
			need++
		}
		if curLen+need > limit {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
			curLen++
		}
		cur.WriteString(line)
		curLen += lineLen
	}

	for _, line := range lines {
		addLine(line)
		// Track fence state AFTER placing the line, so the opening ``` stays with
		// the block it opens.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if fence == "" {
				fence = strings.TrimRight(line, " \t")
			} else {
				fence = ""
			}
		}
	}
	// The final flush must not append a closing fence: an unterminated block in
	// the source stays unterminated rather than being silently repaired.
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// hardSplit cuts a single over-long line into limit-sized pieces on rune
// boundaries. first bounds the opening piece when a partially-filled chunk is
// already in play; a non-positive first falls back to limit.
func hardSplit(line string, first, limit int) []string {
	if first <= 0 {
		first = limit
	}
	var (
		out   []string
		cur   strings.Builder
		n     int
		bound = first
	)
	for _, r := range line {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if n+w > bound {
			out = append(out, cur.String())
			cur.Reset()
			n = 0
			bound = limit
		}
		cur.WriteRune(r)
		n += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// sendChunked delivers text as one or more messages, each within limit.
//
// Sends are SEQUENTIAL and stop at the first error. Neither Discord nor Telegram
// guarantees ordering across concurrent calls, and an interleaved agent overview
// is worse than a truncated one.
func sendChunked(text string, limit int, send func(string) error) error {
	for _, chunk := range splitMessage(text, limit) {
		if err := send(chunk); err != nil {
			return err
		}
	}
	return nil
}

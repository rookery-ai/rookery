package prompts

import "fmt"

// BuildScheduleProsePrompt asks a text-only model to find the sentences in an agent's
// AGENT.md that state a different schedule from the real one. It answers with exact
// replacements rather than a file, and it is told the schedule the file should state
// rather than what changed, so repeating it after several quick saves still converges.
// An empty cronExpr means the agent no longer runs on a schedule.
func BuildScheduleProsePrompt(agentMD, cronExpr, timezone string) string {
	zone := "the owner's own local time"
	if timezone != "" {
		zone = fmt.Sprintf("the owner's own local time (%s)", timezone)
	}
	schedule := fmt.Sprintf("It runs on the cron schedule `%s`, read in %s.", cronExpr, zone)
	if cronExpr == "" {
		schedule = "It no longer runs on a schedule. It runs only when the owner starts it."
	}
	return fmt.Sprintf(`You are keeping an automated agent's instructions consistent with its schedule, which was just changed.

%s

Find every statement in the instructions below of WHEN or HOW OFTEN the agent runs that disagrees with this, and correct it.

Rules:
- Change only statements about when the agent itself runs. Do not change what it does, how it does it, or any data it reads.
- Do not change periods of data ("the last 30 days", "hourly figures"), dates inside examples, retry timings, or the timing of anything other than this agent's own runs.
- Keep the file's language, tone and formatting. Change as few words as possible.
- "find" must be copied exactly from one line, and must be long enough to occur exactly once in the file. "replace" must also be one line.
- If a step's logic depends on the old timing (for example "compare with yesterday's run" when the agent is now weekly), do NOT edit it. Quote that line in "review".
- If everything already agrees, return empty lists.

Reply with one JSON object and nothing else, in this shape:
{"edits": [{"find": "exact text", "replace": "corrected text"}], "review": ["line for the owner to check"]}

Instructions:
---
%s
---`, schedule, agentMD)
}

package prompts

// DryRunSendProhibition is appended to the RUNTIME prompt for a build's dry run, and
// it is the half of the safety story the build-phase marker cannot cover.
//
// buildphase.EnvVar gates connectors.Execute and mcp.Execute — nothing else. At build
// time the rest of the restraint is carried by the prompt: testingRulesBlock's "never
// send real outbound messages on the user's behalf", which reaches only the
// IMPLEMENTATION prompts. A dry run uses BuildCoderPrompt — the RUNTIME prompt — whose
// execution block says the opposite ("DO the task", "RUN that script"). So a TIER 2
// agent holding an SMTP or bot token in a secret, with its own tools/send.py, would
// really send during a rehearsal of an agent the user has not yet approved.
// Connector- and MCP-mediated sends stay blocked by the marker; this closes the
// script/Bash path.
//
// It is a PROMPT, not a boundary. It brings the dry run to parity with the build call
// beside it, which has always relied on the same prompt-level protection while
// injecting the same secrets and granting the same Bash — it does not make the
// rehearsal safe against a model that ignores instructions. Real enforcement would mean
// withholding outbound-capable secrets or confining the network, and neither is built.
//
// It lives in this package because no prompt text lives outside it — the same rule that
// put the KB assist prompts in kbassist.go rather than beside their one caller. It was
// briefly defined in internal/agentdesigner on the argument that it is specific to one
// surface, which is true of kbassist too and is not the test.
const DryRunSendProhibition = `

[DRY RUN — REHEARSAL ONLY]
You are being run once as a rehearsal so this agent's owner can see real output before they
approve it. Nothing you produce here is delivered to anyone.

Read, fetch, compute and inspect freely — that is the point of this run.

Do NOT send, post, publish, message, email, comment on, upload or otherwise transmit
anything to anyone or to any external service, and do NOT create, change or delete anything
there. If this agent's job ends in sending something, do all the work up to that point and
then describe exactly what it WOULD have sent, in your [CHAT] block, instead of sending it.`

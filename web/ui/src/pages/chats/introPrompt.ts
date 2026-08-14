/**
 * The opening message the setup wizard's "Explore what you can do!" button
 * sends on the new owner's behalf.
 *
 * This is UI copy, not a prompt, and that is why it lives here rather than in
 * `internal/prompts`. That package owns what the MODEL is told about itself and
 * its tools; this is a sentence attributed to the USER, shown in their own
 * bubble, and putting it server-side would also make it unreachable from the
 * only place that sends it.
 *
 * It names the surfaces deliberately, because the chat's answer is only as
 * broad as the question: asked "what can you do" a model describes its own
 * tools, which is a much smaller thing than the platform.
 */
export const EXPLORE_INTRO =
  "I've just set up this workspace. Give me a tour of what Rookery can do — " +
  "agents, the knowledge base, skills, connected accounts and MCP servers, " +
  "secrets, chat apps, and how the coder and providers fit together. Keep it " +
  "short and concrete, and finish by suggesting two or three things worth " +
  "setting up first for someone starting out.";

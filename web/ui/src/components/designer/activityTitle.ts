import type { ActivityStatus } from "@/components/chat/ActivityCard";

// The title of the designer's progress card, by stream kind and status.
//
// A BUILD's card deliberately outlives its stream — it is the record of a
// multi-minute build, sitting above the review the user is about to act on — so
// its title has to follow the status. It used to be fixed at "Building your
// agent…", which kept announcing a build in progress beside a finished one's
// Save button.
export function activityTitle(kind: "turn" | "build", status: ActivityStatus, entity: string): string {
  if (kind === "turn") return "Looking through your knowledge base…";
  switch (status) {
    case "done":
      return `Built and tested your ${entity}`;
    case "error":
      return "Build stopped";
    default:
      return `Building your ${entity}…`;
  }
}

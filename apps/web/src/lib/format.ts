import type { AssignmentStage, StageStatus } from "./api";

const dateTime = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

const dateOnly = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return "Not set";
  return dateTime.format(new Date(iso));
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return "Not set";
  return dateOnly.format(new Date(iso));
}

/**
 * Relative deadline, phrased the way a student reads it.
 *
 * Past deadlines say so plainly rather than counting up, because "3 hours ago"
 * next to an upload button reads as if uploading might still work.
 */
export function formatDeadline(iso: string | null | undefined): string {
  if (!iso) return "No deadline";
  const target = new Date(iso).getTime();
  const diff = target - Date.now();
  const abs = Math.abs(diff);

  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;

  let amount: string;
  if (abs < minute) amount = "less than a minute";
  else if (abs < hour) amount = plural(Math.round(abs / minute), "minute");
  else if (abs < day) amount = plural(Math.round(abs / hour), "hour");
  else amount = plural(Math.round(abs / day), "day");

  return diff >= 0 ? `in ${amount}` : `${amount} ago`;
}

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? "" : "s"}`;
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function formatDuration(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

export const stageKindLabel: Record<AssignmentStage["stage_kind"], string> = {
  wait: "Preparation",
  artifact_submission: "Submission",
  live_turn: "Live round",
  automated_evaluation: "Evaluation",
  human_review: "Review",
};

export const stageStatusLabel: Record<StageStatus, string> = {
  pending: "Not open",
  active: "Open",
  grace: "Grace period",
  completed: "Complete",
  expired: "Closed",
  skipped: "Skipped",
  failed: "Failed",
};

export type Tone = "neutral" | "accent" | "pass" | "warn" | "fail";

export function stageTone(status: StageStatus): Tone {
  switch (status) {
    case "active":
      return "accent";
    case "grace":
      return "warn";
    case "completed":
      return "pass";
    case "expired":
    case "failed":
      return "fail";
    default:
      return "neutral";
  }
}

export function assignmentStatusLabel(status: string): string {
  switch (status) {
    case "assigned":
      return "Not started";
    case "in_progress":
      return "In progress";
    case "awaiting_review":
      return "Awaiting review";
    case "finalized":
      return "Finalised";
    case "abandoned":
      return "Missed";
    case "withdrawn":
      return "Withdrawn";
    default:
      return status;
  }
}

/** Title-cases a stage id like `oral_speaker_1` when no label was stored. */
export function humanise(value: string): string {
  return value
    .split(/[_\s]+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

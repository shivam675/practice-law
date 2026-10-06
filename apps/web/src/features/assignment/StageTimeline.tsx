import type { AssignmentStage } from "../../lib/api";
import {
  formatDateTime,
  formatDeadline,
  humanise,
  stageKindLabel,
  stageStatusLabel,
  stageTone,
} from "../../lib/format";
import { Badge } from "../../ui/Layout";

/**
 * The stage timeline is the spine of the page: a student's first question is
 * always "what do I have to do and by when".
 *
 * It is a vertical rail rather than a table because the relationship between
 * stages is sequence, and a table of five rows with hairlines under each would
 * read as a spec sheet.
 */
export function StageTimeline({
  stages,
  activeStageId,
  onSelect,
}: {
  stages: AssignmentStage[];
  activeStageId: string | null;
  onSelect: (stageId: string) => void;
}) {
  return (
    <ol className="relative">
      {stages.map((stage, index) => {
        const isLast = index === stages.length - 1;
        const selected = stage.stage_id === activeStageId;
        const open = stage.status === "active" || stage.status === "grace";
        const settled =
          stage.status === "completed" ||
          stage.status === "expired" ||
          stage.status === "skipped" ||
          stage.status === "failed";

        return (
          <li key={stage.id} className="relative pl-8">
            {!isLast ? (
              <span
                className="absolute left-[7px] top-5 h-full w-px bg-rule"
                aria-hidden
              />
            ) : null}

            <span
              className={`absolute left-0 top-[6px] size-[15px] rounded-full border-2 ${
                open
                  ? "border-accent bg-accent"
                  : stage.status === "completed"
                    ? "border-pass bg-pass"
                    : "border-rule-strong bg-paper"
              }`}
              aria-hidden
            />

            <button
              type="button"
              onClick={() => onSelect(stage.stage_id)}
              className={`mb-5 block w-full rounded-md px-3 py-2 text-left transition-colors ${
                selected ? "bg-accent-soft" : "hover:bg-paper-sunken"
              }`}
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className={`text-sm ${open ? "font-medium text-ink" : "text-ink"}`}>
                  {stage.label ?? humanise(stage.stage_id)}
                </span>
                <Badge tone={stageTone(stage.status)}>{stageStatusLabel[stage.status]}</Badge>
              </div>

              <p className="mt-1 text-xs text-ink-muted">
                {stageKindLabel[stage.stage_kind]}
                {/* A settled stage shows when it settled. Showing its original
                    deadline would read as if something were still due. */}
                {settled && stage.completed_at ? (
                  <>
                    {" · "}
                    <span className="numeric">{formatDeadline(stage.completed_at)}</span>
                  </>
                ) : stage.due_at && !settled ? (
                  <>
                    {" · due "}
                    <span className="numeric">{formatDeadline(stage.due_at)}</span>
                  </>
                ) : stage.opens_at && !settled ? (
                  <>
                    {" · opens "}
                    <span className="numeric">{formatDeadline(stage.opens_at)}</span>
                  </>
                ) : settled ? null : (
                  " · follows the previous stage"
                )}
              </p>

              {stage.status === "grace" && stage.grace_until ? (
                <p className="mt-1 text-xs text-warn">
                  Late submissions accepted until {formatDateTime(stage.grace_until)}
                </p>
              ) : null}
            </button>
          </li>
        );
      })}
    </ol>
  );
}

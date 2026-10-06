import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ArrowRight } from "@phosphor-icons/react";
import { api, type Assignment } from "../lib/api";
import {
  assignmentStatusLabel,
  formatDeadline,
  formatDateTime,
  humanise,
  stageKindLabel,
  stageTone,
  stageStatusLabel,
} from "../lib/format";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

export function MyWork() {
  const { data, isPending, error, refetch } = useQuery({
    queryKey: ["assignments"],
    queryFn: () => api.get<{ assignments: Assignment[] }>("/assignments"),
  });

  return (
    <>
      <PageHeader
        title="My work"
        meta="Assessments you have been assigned, newest first."
      />

      {isPending ? <SkeletonRows rows={2} /> : null}

      {error ? (
        <ErrorState
          error={error}
          action={
            <button onClick={() => void refetch()} className="text-sm underline">
              Try again
            </button>
          }
        />
      ) : null}

      {data && data.assignments.length === 0 ? (
        <EmptyState title="Nothing assigned yet">
          When a teacher assigns your team to an assessment, it appears here with
          its deadlines.
        </EmptyState>
      ) : null}

      <ul className="space-y-4">
        {data?.assignments.map((assignment) => (
          <AssignmentRow key={assignment.id} assignment={assignment} />
        ))}
      </ul>
    </>
  );
}

function AssignmentRow({ assignment }: { assignment: Assignment }) {
  const { data } = useQuery({
    queryKey: ["assignment", assignment.id],
    queryFn: () => api.get<Assignment>(`/assignments/${assignment.id}`),
  });

  const stages = data?.stages ?? [];
  const current =
    stages.find((s) => s.status === "active" || s.status === "grace") ??
    stages.find((s) => s.status === "pending");

  return (
    <Surface as="li">
      <Link
        to={`/work/${assignment.id}`}
        className="group block rounded-lg p-5 transition-colors hover:bg-paper-sunken/60"
      >
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone={assignment.status === "in_progress" ? "accent" : "neutral"}>
                {assignmentStatusLabel(assignment.status)}
              </Badge>
              <span className="text-xs text-ink-faint">
                Appearing for the {assignment.side}
              </span>
            </div>
            <h2 className="mt-2 text-xl leading-snug">{assignment.assessment_title}</h2>
            <p className="mt-1 text-sm text-ink-muted">{assignment.team_name}</p>
          </div>

          <ArrowRight
            size={18}
            className="mt-1 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5"
            aria-hidden
          />
        </div>

        {current ? (
          <div className="mt-5 flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-rule pt-4 text-sm">
            <span className="text-ink-muted">
              Next: <span className="text-ink">{current.label ?? humanise(current.stage_id)}</span>
            </span>
            <Badge tone={stageTone(current.status)}>{stageStatusLabel[current.status]}</Badge>
            {current.due_at ? (
              <span className="text-ink-muted">
                Due <span className="numeric text-ink">{formatDeadline(current.due_at)}</span>
                <span className="text-ink-faint"> · {formatDateTime(current.due_at)}</span>
              </span>
            ) : current.opens_at ? (
              <span className="text-ink-muted">
                Opens <span className="numeric text-ink">{formatDeadline(current.opens_at)}</span>
              </span>
            ) : (
              <span className="text-ink-faint">
                {stageKindLabel[current.stage_kind]} follows the previous stage
              </span>
            )}
          </div>
        ) : null}
      </Link>
    </Surface>
  );
}

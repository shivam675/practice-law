import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  api,
  type Assignment,
  type Resource,
  type Submission,
} from "../lib/api";
import {
  assignmentStatusLabel,
  formatDateTime,
  humanise,
  stageKindLabel,
} from "../lib/format";
import { Badge, DataPoint, PageHeader, Surface } from "../ui/Layout";
import { Alert, ErrorState, SkeletonRows } from "../ui/Feedback";
import { ButtonLink } from "../ui/Button";
import { StageTimeline } from "../features/assignment/StageTimeline";
import { SubmissionPanel } from "../features/assignment/SubmissionPanel";
import { ResourceReader } from "../features/assignment/ResourceReader";

type Tab = "stage" | "materials";

export function AssignmentDetail() {
  const { assignmentId = "" } = useParams();
  const [tab, setTab] = useState<Tab>("stage");
  const [selectedStageId, setSelectedStageId] = useState<string | null>(null);

  const assignment = useQuery({
    queryKey: ["assignment", assignmentId],
    queryFn: () => api.get<Assignment>(`/assignments/${assignmentId}`),
    // A deadline can pass while the page is open and the server will close the
    // stage without being asked. Refetching keeps the UI honest about it.
    refetchInterval: 30_000,
  });

  const submissions = useQuery({
    queryKey: ["submissions", assignmentId],
    queryFn: () =>
      api.get<{ submissions: Submission[] }>(`/assignments/${assignmentId}/submissions`),
  });

  const resources = useQuery({
    queryKey: ["resources", assignmentId],
    queryFn: () => api.get<{ resources: Resource[] }>(`/assignments/${assignmentId}/resources`),
  });

  const stages = assignment.data?.stages ?? [];

  const activeStage = useMemo(() => {
    if (selectedStageId) {
      return stages.find((s) => s.stage_id === selectedStageId) ?? null;
    }
    return (
      stages.find((s) => s.status === "active" || s.status === "grace") ??
      stages.find((s) => s.status === "pending") ??
      stages[stages.length - 1] ??
      null
    );
  }, [selectedStageId, stages]);

  if (assignment.isPending) return <SkeletonRows rows={3} />;
  if (assignment.error) return <ErrorState error={assignment.error} />;
  if (!assignment.data) return null;

  const data = assignment.data;
  const liveStage = stages.find(
    (s) => s.stage_kind === "live_turn" && (s.status === "active" || s.status === "pending"),
  );

  return (
    <>
      <PageHeader
        title={data.assessment_title ?? "Assessment"}
        meta={
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <Badge tone={data.status === "in_progress" ? "accent" : "neutral"}>
              {assignmentStatusLabel(data.status)}
            </Badge>
            <span>
              {data.team_name} · appearing for the {data.side}
            </span>
          </span>
        }
        actions={
          liveStage ? (
            <ButtonLink to={`/session/${data.id}/${liveStage.stage_id}`} variant="secondary">
              Open the session room
            </ButtonLink>
          ) : undefined
        }
      />

      <div className="grid gap-10 lg:grid-cols-[minmax(0,17rem)_minmax(0,1fr)]">
        <div>
          <h2 className="mb-4 text-xs font-medium text-ink-faint">Stages</h2>
          <StageTimeline
            stages={stages}
            activeStageId={activeStage?.stage_id ?? null}
            onSelect={(id) => {
              setSelectedStageId(id);
              setTab("stage");
            }}
          />
        </div>

        <div className="min-w-0">
          <div
            role="tablist"
            aria-label="Assignment views"
            className="mb-6 flex gap-1 border-b border-rule"
          >
            <TabButton active={tab === "stage"} onClick={() => setTab("stage")}>
              {activeStage ? (activeStage.label ?? humanise(activeStage.stage_id)) : "Stage"}
            </TabButton>
            <TabButton active={tab === "materials"} onClick={() => setTab("materials")}>
              Case materials
            </TabButton>
          </div>

          {tab === "materials" ? (
            resources.isPending ? (
              <SkeletonRows rows={2} />
            ) : resources.error ? (
              <ErrorState error={resources.error} />
            ) : (
              <ResourceReader resources={resources.data?.resources ?? []} />
            )
          ) : activeStage ? (
            <StageView
              assignmentId={assignmentId}
              stage={activeStage}
              submissions={submissions.data?.submissions ?? []}
              loadingSubmissions={submissions.isPending}
            />
          ) : null}
        </div>
      </div>
    </>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={`-mb-px border-b-2 px-3 py-2 text-sm transition-colors ${
        active
          ? "border-accent font-medium text-accent"
          : "border-transparent text-ink-muted hover:text-ink"
      }`}
    >
      {children}
    </button>
  );
}

function StageView({
  assignmentId,
  stage,
  submissions,
  loadingSubmissions,
}: {
  assignmentId: string;
  stage: NonNullable<Assignment["stages"]>[number];
  submissions: Submission[];
  loadingSubmissions: boolean;
}) {
  return (
    <div className="space-y-6">
      <Surface className="px-5 py-5">
        <dl className="grid grid-cols-2 gap-x-8 gap-y-4 sm:grid-cols-4">
          <DataPoint label="Kind" value={stageKindLabel[stage.stage_kind]} />
          <DataPoint label="Opens" value={formatDateTime(stage.opens_at)} numeric />
          <DataPoint label="Due" value={formatDateTime(stage.due_at)} numeric />
          <DataPoint
            label="Grace until"
            value={stage.grace_until ? formatDateTime(stage.grace_until) : "None"}
            numeric
          />
        </dl>
      </Surface>

      {stage.stage_kind === "artifact_submission" ? (
        loadingSubmissions ? (
          <SkeletonRows rows={1} />
        ) : (
          <SubmissionPanel
            assignmentId={assignmentId}
            stage={stage}
            submissions={submissions}
          />
        )
      ) : null}

      {stage.stage_kind === "wait" ? (
        <Alert tone="info" title="Preparation">
          Read the problem and the listed authorities under Case materials.
          Submission opens when this period ends.
        </Alert>
      ) : null}

      {stage.stage_kind === "live_turn" ? (
        <Alert tone="info" title="Oral round">
          Join from the session room at the scheduled time. Headphones are
          required: without them the bench hears its own voice through your
          microphone.
        </Alert>
      ) : null}

      {stage.stage_kind === "automated_evaluation" ? (
        <Alert tone="info" title="Evaluation">
          Marking runs against the rubric once the material it covers is in.
          Results are released after a teacher has reviewed them.
        </Alert>
      ) : null}

      {stage.stage_kind === "human_review" ? (
        <Alert tone="info" title="Teacher review">
          A teacher signs off every result before you see it. Scores produced
          automatically are advisory until then.
        </Alert>
      ) : null}
    </div>
  );
}

import { useState } from "react";
import { AssessmentResources } from "./AssessmentResources";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Assessment, type Assignment, type Team } from "../lib/api";
import { assignmentStatusLabel, formatDateTime, humanise } from "../lib/format";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field } from "../ui/Field";
import { Badge, DataPoint, PageHeader, SectionTitle, Surface } from "../ui/Layout";
import { EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

type TemplateVersion = {
  id: string;
  version: number;
  participation: { sides: string[]; speakers: number; min_team_size: number; max_team_size: number };
  stages: { id: string; kind: string; label: string }[];
};

export function AssessmentDetail() {
  const { assessmentId = "" } = useParams();
  const { can } = useAuth();

  const assessment = useQuery({
    queryKey: ["assessment", assessmentId],
    queryFn: () => api.get<Assessment>(`/assessments/${assessmentId}`),
  });

  const assignments = useQuery({
    queryKey: ["assessment-assignments", assessmentId],
    queryFn: () =>
      api.get<{ assignments: Assignment[] }>(`/assessments/${assessmentId}/assignments`),
  });

  const version = useQuery({
    queryKey: ["template-version", assessment.data?.template_version_id],
    queryFn: () =>
      api.get<TemplateVersion>(`/templates/versions/${assessment.data!.template_version_id}`),
    enabled: Boolean(assessment.data?.template_version_id),
  });

  if (assessment.isPending) return <SkeletonRows rows={3} />;
  if (assessment.error) return <ErrorState error={assessment.error} />;
  if (!assessment.data) return null;

  const data = assessment.data;

  return (
    <>
      <PageHeader
        title={data.title}
        meta={
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <Badge tone={data.status === "draft" ? "neutral" : "accent"}>{data.status}</Badge>
            <span>{data.template_name}</span>
          </span>
        }
      />

      <Surface className="mb-10 px-5 py-5">
        <dl className="grid grid-cols-2 gap-x-8 gap-y-4 md:grid-cols-4">
          <DataPoint label="Opens" value={formatDateTime(data.opens_at)} numeric />
          <DataPoint label="Closes" value={formatDateTime(data.closes_at)} numeric />
          <DataPoint label="Teams assigned" value={assignments.data?.assignments.length ?? 0} numeric />
          <DataPoint
            label="Sides"
            value={version.data?.participation.sides.map(humanise).join(" and ") ?? "Loading"}
          />
        </dl>
      </Surface>

      {version.data && (can("knowledge.view") || can("knowledge.upload")) && <AssessmentResources assessmentId={assessmentId} sides={version.data.participation.sides} />}

      {can("assessment.assign") && version.data ? (
        <AssignTeam assessmentId={assessmentId} participation={version.data.participation} />
      ) : null}

      <SectionTitle
        trailing={
          <span className="text-sm text-ink-muted">
            <span className="numeric">{assignments.data?.assignments.length ?? 0}</span> assigned
          </span>
        }
      >
        Teams
      </SectionTitle>

      {assignments.isPending ? <SkeletonRows rows={2} /> : null}
      {assignments.error ? <ErrorState error={assignments.error} /> : null}

      {assignments.data && assignments.data.assignments.length === 0 ? (
        <EmptyState title="No teams assigned">
          Assign a team above. Each assignment gets its own copy of the stage
          timeline with absolute deadlines.
        </EmptyState>
      ) : null}

      {assignments.data && assignments.data.assignments.length > 0 ? (
        <Surface className="overflow-hidden">
          <table className="w-full text-sm">
            <caption className="sr-only">Teams assigned to this assessment</caption>
            <thead>
              <tr className="border-b border-rule bg-paper-sunken/60 text-left">
                <th scope="col" className="px-5 py-3 font-medium">Team</th>
                <th scope="col" className="px-5 py-3 font-medium">Side</th>
                <th scope="col" className="px-5 py-3 font-medium">Progress</th>
                <th scope="col" className="px-5 py-3 font-medium">Current stage</th>
                <th scope="col" className="px-5 py-3"><span className="sr-only">Open</span></th>
              </tr>
            </thead>
            <tbody>
              {assignments.data.assignments.map((assignment) => (
                <tr key={assignment.id} className="border-b border-rule last:border-0">
                  <td className="px-5 py-3">{assignment.team_name}</td>
                  <td className="px-5 py-3 text-ink-muted">{humanise(assignment.side)}</td>
                  <td className="px-5 py-3">
                    <Badge tone={assignment.status === "in_progress" ? "accent" : "neutral"}>
                      {assignmentStatusLabel(assignment.status)}
                    </Badge>
                  </td>
                  <td className="px-5 py-3 text-ink-muted">
                    {assignment.current_stage_id ? humanise(assignment.current_stage_id) : "Not started"}
                  </td>
                  <td className="px-5 py-3 text-right">
                    <Link
                      to={`/work/${assignment.id}`}
                      className="text-sm text-accent underline-offset-2 hover:underline"
                    >
                      Open
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Surface>
      ) : null}
    </>
  );
}

function AssignTeam({
  assessmentId,
  participation,
}: {
  assessmentId: string;
  participation: TemplateVersion["participation"];
}) {
  const queryClient = useQueryClient();
  const [teamId, setTeamId] = useState("");
  const [side, setSide] = useState(participation.sides[0] ?? "");

  const teams = useQuery({
    queryKey: ["teams"],
    queryFn: () => api.get<{ teams: Team[] }>("/teams"),
  });

  const assign = useMutation({
    mutationFn: () =>
      api.post<Assignment>(`/assessments/${assessmentId}/assignments`, {
        team_id: teamId,
        side,
      }),
    onSuccess: () => {
      setTeamId("");
      void queryClient.invalidateQueries({ queryKey: ["assessment-assignments", assessmentId] });
      void queryClient.invalidateQueries({ queryKey: ["assessment", assessmentId] });
    },
  });

  const eligible =
    teams.data?.teams.filter((team) => {
      const speakers = team.members.filter((m) => m.role === "speaker").length;
      return (
        speakers === participation.speakers &&
        team.members.length >= participation.min_team_size &&
        team.members.length <= participation.max_team_size
      );
    }) ?? [];

  const ineligibleCount = (teams.data?.teams.length ?? 0) - eligible.length;

  return (
    <Surface className="mb-10 px-5 py-5">
      <h2 className="text-lg">Assign a team</h2>
      <p className="mt-1 text-sm text-ink-muted">
        This template expects <span className="numeric">{participation.speakers}</span> speakers and{" "}
        <span className="numeric">{participation.min_team_size}</span> to{" "}
        <span className="numeric">{participation.max_team_size}</span> members.
      </p>

      <form
        className="mt-5 grid items-end gap-5 md:grid-cols-[1fr_12rem_auto]"
        onSubmit={(e) => {
          e.preventDefault();
          assign.mutate();
        }}
      >
        <Field
          label="Team"
          helper={
            ineligibleCount > 0
              ? `${ineligibleCount} team(s) hidden: wrong size or speaker count.`
              : undefined
          }
        >
          {({ id }) => (
            <select
              id={id}
              required
              value={teamId}
              onChange={(e) => setTeamId(e.target.value)}
              className="h-10 w-full rounded-md border border-rule-strong bg-paper-raised px-3 text-sm text-ink"
            >
              <option value="">Choose a team</option>
              {eligible.map((team) => (
                <option key={team.id} value={team.id}>
                  {team.name}
                </option>
              ))}
            </select>
          )}
        </Field>

        <Field label="Side">
          {({ id }) => (
            <select
              id={id}
              value={side}
              onChange={(e) => setSide(e.target.value)}
              className="h-10 w-full rounded-md border border-rule-strong bg-paper-raised px-3 text-sm text-ink"
            >
              {participation.sides.map((s) => (
                <option key={s} value={s}>
                  {humanise(s)}
                </option>
              ))}
            </select>
          )}
        </Field>

        <Button type="submit" disabled={!teamId || assign.isPending}>
          {assign.isPending ? "Assigning" : "Assign"}
        </Button>

        {assign.error ? (
          <div className="md:col-span-3">
            <ErrorState error={assign.error} />
          </div>
        ) : null}
      </form>

      {eligible.length === 0 && !teams.isPending ? (
        <p className="mt-4 text-sm text-warn">
          No team matches this template's shape. Create one under Teams with{" "}
          <span className="numeric">{participation.speakers}</span> speakers.
        </p>
      ) : null}
    </Surface>
  );
}

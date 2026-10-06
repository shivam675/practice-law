import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "@phosphor-icons/react";
import { api, type Assessment } from "../lib/api";
import { formatDate, formatDateTime } from "../lib/format";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

type TemplateSummary = {
  id: string;
  key: string;
  name: string;
  versions: { id: string; version: number; status: string }[];
};

const statusTone = {
  draft: "neutral",
  published: "accent",
  running: "accent",
  closed: "neutral",
  archived: "neutral",
} as const;

export function Assessments() {
  const { can } = useAuth();
  const [creating, setCreating] = useState(false);

  const assessments = useQuery({
    queryKey: ["assessments"],
    queryFn: () => api.get<{ assessments: Assessment[] }>("/assessments"),
  });

  return (
    <>
      <PageHeader
        title="Assessments"
        meta="Published template versions, scheduled for a cohort."
        actions={
          can("assessment.create") ? (
            <Button onClick={() => setCreating((v) => !v)} variant={creating ? "ghost" : "primary"}>
              {creating ? (
                "Cancel"
              ) : (
                <>
                  <Plus size={16} aria-hidden />
                  New assessment
                </>
              )}
            </Button>
          ) : undefined
        }
      />

      {creating ? <CreateAssessment onDone={() => setCreating(false)} /> : null}

      {assessments.isPending ? <SkeletonRows rows={3} /> : null}
      {assessments.error ? <ErrorState error={assessments.error} /> : null}

      {assessments.data && assessments.data.assessments.length === 0 && !creating ? (
        <EmptyState
          title="No assessments yet"
          action={
            can("assessment.create") ? (
              <Button onClick={() => setCreating(true)}>Create the first one</Button>
            ) : undefined
          }
        >
          An assessment schedules a template for a group of teams and sets the
          clock every deadline is measured from.
        </EmptyState>
      ) : null}

      <ul className="space-y-3">
        {assessments.data?.assessments.map((assessment) => (
          <Surface as="li" key={assessment.id}>
            <Link
              to={`/assessments/${assessment.id}`}
              className="block rounded-lg px-5 py-4 transition-colors hover:bg-paper-sunken/60"
            >
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge tone={statusTone[assessment.status]}>{assessment.status}</Badge>
                    <span className="text-xs text-ink-faint">{assessment.template_name}</span>
                  </div>
                  <h2 className="mt-2 text-lg leading-snug">{assessment.title}</h2>
                </div>

                <dl className="flex gap-8 text-sm">
                  <div>
                    <dt className="text-xs text-ink-faint">Opens</dt>
                    <dd className="numeric mt-0.5">{formatDate(assessment.opens_at)}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-ink-faint">Teams</dt>
                    <dd className="numeric mt-0.5">{assessment.assignment_count}</dd>
                  </div>
                </dl>
              </div>
            </Link>
          </Surface>
        ))}
      </ul>
    </>
  );
}

function CreateAssessment({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState("");
  const [versionId, setVersionId] = useState("");
  const [opensAt, setOpensAt] = useState(defaultOpensAt());

  const templates = useQuery({
    queryKey: ["templates"],
    queryFn: () => api.get<{ templates: TemplateSummary[] }>("/templates"),
  });

  const publishedVersions =
    templates.data?.templates.flatMap((template) =>
      template.versions
        .filter((v) => v.status === "published")
        .map((v) => ({ id: v.id, label: `${template.name} v${v.version}` })),
    ) ?? [];

  const create = useMutation({
    mutationFn: async () => {
      const created = await api.post<Assessment>("/assessments", {
        template_version_id: versionId,
        title,
        opens_at: new Date(opensAt).toISOString(),
      });
      await api.post(`/assessments/${created.id}/publish`);
      return created;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["assessments"] });
      onDone();
    },
  });

  return (
    <Surface className="mb-8 px-5 py-5">
      <h2 className="text-lg">New assessment</h2>
      <p className="mt-1 text-sm text-ink-muted">
        Every stage deadline is measured from the opening time, so it cannot be
        changed once teams are assigned.
      </p>

      <form
        className="mt-5 grid gap-5 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Title">
          {({ id }) => (
            <TextInput
              id={id}
              required
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Spring moot, round one"
            />
          )}
        </Field>

        <Field label="Template version" helper="Only published versions can be scheduled.">
          {({ id }) => (
            <select
              id={id}
              required
              value={versionId}
              onChange={(e) => setVersionId(e.target.value)}
              className="h-10 w-full rounded-md border border-rule-strong bg-paper-raised px-3 text-sm text-ink"
            >
              <option value="">Choose a template</option>
              {publishedVersions.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.label}
                </option>
              ))}
            </select>
          )}
        </Field>

        <Field label="Opens at" helper="Local time.">
          {({ id }) => (
            <TextInput
              id={id}
              type="datetime-local"
              required
              value={opensAt}
              onChange={(e) => setOpensAt(e.target.value)}
            />
          )}
        </Field>

        <div className="flex items-end gap-3">
          <Button type="submit" disabled={create.isPending || !versionId}>
            {create.isPending ? "Creating" : "Create and publish"}
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        </div>

        {create.error ? (
          <div className="md:col-span-2">
            <ErrorState error={create.error} />
          </div>
        ) : null}
      </form>

      {publishedVersions.length === 0 && !templates.isPending ? (
        <p className="mt-4 text-sm text-warn">
          No published template versions exist. Publish one under Templates
          first.
        </p>
      ) : null}
    </Surface>
  );
}

/** Tomorrow at 09:00, formatted for datetime-local. */
function defaultOpensAt(): string {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  d.setHours(9, 0, 0, 0);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export { formatDateTime };

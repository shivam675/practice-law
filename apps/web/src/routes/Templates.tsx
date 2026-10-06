import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { humanise, stageKindLabel, formatDuration } from "../lib/format";
import type { StageKind } from "../lib/api";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { ErrorState, SkeletonRows } from "../ui/Feedback";

type Version = {
  id: string;
  version: number;
  status: string;
  stages?: Stage[];
  participation?: { sides: string[]; speakers: number };
};

type Stage = {
  id: string;
  kind: StageKind;
  label: string;
  due_after_s?: number;
  opens_after_s?: number;
  config?: { duration_s?: number; rubric_scope?: string[]; format_rules?: string };
};

type Template = {
  id: string;
  key: string;
  name: string;
  description: string;
  assessment_type: string;
  versions: Version[];
};

export function Templates() {
  const templates = useQuery({
    queryKey: ["templates"],
    queryFn: () => api.get<{ templates: Template[] }>("/templates"),
  });

  return (
    <>
      <PageHeader
        title="Templates"
        meta="A template is a stage list, a participation shape and a rubric. The same engine runs every assessment type."
      />

      {templates.isPending ? <SkeletonRows rows={2} /> : null}
      {templates.error ? <ErrorState error={templates.error} /> : null}

      <ul className="space-y-6">
        {templates.data?.templates.map((template) => (
          <TemplateCard key={template.id} template={template} />
        ))}
      </ul>

      <p className="mt-10 max-w-prose text-sm text-ink-muted">
        Editing stage configuration from this screen is not built yet. Until it
        is, templates are created through the API and reviewed here.
      </p>
    </>
  );
}

function TemplateCard({ template }: { template: Template }) {
  const latest = template.versions[0];

  const detail = useQuery({
    queryKey: ["template-version", latest?.id],
    queryFn: () => api.get<Version>(`/templates/versions/${latest!.id}`),
    enabled: Boolean(latest?.id),
  });

  const stages = detail.data?.stages ?? [];

  return (
    <Surface as="li" className="px-5 py-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <Badge>{humanise(template.assessment_type)}</Badge>
            {latest ? (
              <Badge tone={latest.status === "published" ? "pass" : "neutral"}>
                v{latest.version} {latest.status}
              </Badge>
            ) : null}
          </div>
          <h2 className="mt-2 text-lg leading-snug">{template.name}</h2>
          <p className="mt-1 max-w-prose text-sm text-ink-muted">{template.description}</p>
        </div>

        {detail.data?.participation ? (
          <dl className="flex gap-8 text-sm">
            <div>
              <dt className="text-xs text-ink-faint">Speakers</dt>
              <dd className="numeric mt-0.5">{detail.data.participation.speakers}</dd>
            </div>
            <div>
              <dt className="text-xs text-ink-faint">Sides</dt>
              <dd className="mt-0.5">
                {detail.data.participation.sides.map(humanise).join(" / ")}
              </dd>
            </div>
          </dl>
        ) : null}
      </div>

      {stages.length ? (
        <ol className="mt-5 flex flex-wrap gap-2 border-t border-rule pt-5">
          {stages.map((stage) => (
            <li
              key={stage.id}
              className="rounded-md border border-rule bg-paper-sunken/70 px-3 py-2"
            >
              <span className="block text-sm text-ink">{stage.label}</span>
              <span className="mt-0.5 block text-xs text-ink-muted">
                {stageKindLabel[stage.kind]}
                {stage.config?.duration_s ? (
                  <>
                    {" · "}
                    <span className="numeric">{formatDuration(stage.config.duration_s)}</span>
                  </>
                ) : null}
                {stage.config?.rubric_scope ? (
                  <>
                    {" · "}
                    <span className="numeric">{stage.config.rubric_scope.length}</span> criteria
                  </>
                ) : null}
                {stage.config?.format_rules ? <> · {stage.config.format_rules}</> : null}
              </span>
            </li>
          ))}
        </ol>
      ) : null}
    </Surface>
  );
}

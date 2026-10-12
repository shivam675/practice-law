import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { formatDateTime, humanise } from "../lib/format";
import { interruptionsLabel, problemLabel, relevanceLabel, type RoundFeedback } from "../features/session/FeedbackForm";
import { Badge, PageHeader } from "../ui/Layout";
import { EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

type Entry = RoundFeedback & { id: string; session_id: string; assignment_id: string; stage_id: string; student: string; assessment_title: string; created_at: string };
type Summary = { count: number; average_rating: number | null; relevance: Record<string, number>; interruptions: Record<string, number> };

// What students said about the bench after each oral round. The summary is
// computed by the API over every answer, not only the rows listed here.
export function RoundFeedbackList() {
  const { can } = useAuth();
  const list = useQuery({ queryKey: ["feedback"], queryFn: () => api.get<{ summary: Summary; feedback: Entry[] }>("/feedback"), enabled: can("report.view") });
  if (!can("report.view")) return <EmptyState title="Access restricted">You do not have permission to read round feedback.</EmptyState>;
  const summary = list.data?.summary;
  return <>
    <PageHeader title="Round feedback" meta="How students found the judge after each oral round. The latest 200 answers are listed." />
    {list.isPending ? <SkeletonRows rows={4} /> : null}
    {list.error ? <ErrorState error={list.error} /> : null}
    {summary && summary.count > 0 && <dl className="mb-8 grid gap-4 border-y border-rule py-5 sm:grid-cols-3">
      <div><dt className="text-sm text-ink-muted">Average rating</dt><dd className="numeric mt-2 text-2xl">{summary.average_rating ?? "—"} / 5</dd><dd className="text-xs text-ink-muted">{summary.count} answers</dd></div>
      <div><dt className="text-sm text-ink-muted">Questions about the case</dt><dd className="numeric mt-2 text-sm">{Object.entries(relevanceLabel).map(([key, label]) => `${label} ${summary.relevance[key]}`).join(" · ")}</dd></div>
      <div><dt className="text-sm text-ink-muted">Interruptions</dt><dd className="numeric mt-2 text-sm">{Object.entries(interruptionsLabel).map(([key, label]) => `${label} ${summary.interruptions[key]}`).join(" · ")}</dd></div>
    </dl>}
    {list.data?.feedback.length === 0 ? <EmptyState title="No feedback yet">Students are asked for it when an oral round ends.</EmptyState> : null}
    <ul className="divide-y divide-rule">{list.data?.feedback.map((f) => <li key={f.id} className="py-5">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <div><p className="font-medium">{f.student}</p><p className="text-sm text-ink-muted">{f.assessment_title} · {humanise(f.stage_id)} · {formatDateTime(f.created_at)}</p></div>
        <Link className="text-sm text-accent underline underline-offset-2" to={`/session/${f.assignment_id}/${f.stage_id}`}>Transcript</Link>
      </div>
      <div className="mt-3 flex flex-wrap gap-2">
        <Badge tone={f.rating >= 4 ? "pass" : f.rating <= 2 ? "fail" : "neutral"}>{f.rating} / 5</Badge>
        <Badge tone={f.relevance === "yes" ? "pass" : f.relevance === "no" ? "fail" : "warn"}>On the case: {relevanceLabel[f.relevance]}</Badge>
        <Badge tone={f.interruptions === "about_right" ? "neutral" : "warn"}>Interruptions: {interruptionsLabel[f.interruptions]}</Badge>
        {f.problems.map((p) => <Badge key={p} tone="warn">{problemLabel[p]}</Badge>)}
      </div>
      {f.comment && <p className="mt-3 whitespace-pre-line text-sm leading-relaxed">{f.comment}</p>}
    </li>)}</ul>
  </>;
}

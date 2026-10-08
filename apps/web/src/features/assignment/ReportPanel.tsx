import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { Button } from "../../ui/Button";
import { Field, TextInput } from "../../ui/Field";
import { Alert, ErrorState, SkeletonRows } from "../../ui/Feedback";
import { Badge, Surface } from "../../ui/Layout";

type Score = { id: string; name: string; score: number; max_score: number; weight: number; reasoning: string; overridden_score: number | null; override_reason: string | null; evidence: { quote: string; locator: string; source_kind: string }[] };
type Report = { status: string; scores: Score[]; total: number; maximum: number; notes: string; caveats: string[] };

export function ReportPanel({ assignmentId }: { assignmentId: string }) {
  const { can } = useAuth();
  const client = useQueryClient();
  const [notes, setNotes] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const report = useQuery({ queryKey: ["report", assignmentId], queryFn: () => api.get<Report>(`/assignments/${assignmentId}/report`), refetchInterval: 15000 });
  const refresh = () => { void client.invalidateQueries({ queryKey: ["report", assignmentId] }); void client.invalidateQueries({ queryKey: ["assignment", assignmentId] }); void client.invalidateQueries({ queryKey: ["assignments"] }); };
  const override = useMutation({ mutationFn: (body: { score_id: string; score: number; reason: string }) => api.patch(`/assignments/${assignmentId}/report/scores`, body), onSuccess: () => { setEditing(null); refresh(); } });
  const publish = useMutation({ mutationFn: () => api.post(`/assignments/${assignmentId}/report/publish`, { notes }), onSuccess: refresh });
  if (report.isPending) return <SkeletonRows rows={3} />;
  if (report.error) return <ErrorState error={report.error} />;
  if (!report.data) return null;
  const data = report.data;
  if (data.status === "unpublished") return <Alert tone="info" title="Results are not published yet">Your teacher will review your assessment before you can read the results.</Alert>;
  const published = data.status === "published";
  return <div className="space-y-6">
    <div className="flex flex-wrap items-center justify-between gap-4"><div><h2 className="text-2xl">Assessment report</h2><div className="mt-2"><Badge tone={published ? "pass" : "warn"}>{published ? "Published" : "Teacher review"}</Badge></div></div>{data.maximum > 0 && <p className="numeric text-3xl">{data.total.toFixed(1)}<span className="text-base text-ink-muted"> / {data.maximum.toFixed(0)}</span></p>}</div>
    {!data.scores.length && <Alert tone="info" title="Evaluation in progress">Scores appear here after the assessment service completes the evaluation.</Alert>}
    {data.caveats?.length > 0 && <Alert tone="warn" title="Assessment limitations"><ul className="list-disc pl-5">{data.caveats.map((text) => <li key={text}>{text}</li>)}</ul></Alert>}
    {data.scores.map((score) => <Surface key={score.id} className="p-5"><div className="flex justify-between gap-4"><h3 className="text-xl">{score.name}</h3><p className="numeric">{score.overridden_score ?? score.score} / {score.max_score}</p></div><p className="mt-3 text-sm leading-relaxed text-ink-muted">{score.reasoning}</p>{score.override_reason && <p className="mt-3 text-sm"><strong>Teacher adjustment:</strong> {score.override_reason}</p>}<div className="mt-4 space-y-3">{score.evidence.map((e, i) => <blockquote key={i} className="border-l-2 border-accent-rule pl-4"><p className="text-sm leading-relaxed">“{e.quote}”</p><p className="mt-1 text-xs text-ink-muted">{e.locator} · {e.source_kind}</p></blockquote>)}</div>
      {!published && can("assessment.override_grade") && <div className="mt-4">{editing !== score.id ? <Button size="sm" variant="secondary" onClick={() => { override.reset(); setEditing(score.id); }}>Adjust score</Button> : <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); const form = new FormData(e.currentTarget); override.mutate({ score_id: score.id, score: Number(form.get("score")), reason: String(form.get("reason")) }); }}><Field label={`Score, maximum ${score.max_score}`}>{({ id }) => <TextInput id={id} name="score" type="number" step="0.01" min={0} max={score.max_score} required defaultValue={score.overridden_score ?? score.score} />}</Field><Field label="Reason for the change">{({ id }) => <TextInput id={id} name="reason" required maxLength={4000} />}</Field>{override.error ? <ErrorState error={override.error} /> : null}<div className="flex gap-2"><Button size="sm" type="submit" disabled={override.isPending}>Save adjustment</Button><Button size="sm" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button></div></form>}</div>}
      {!published && can("assessment.grade") && <CriterionRegrade assignmentId={assignmentId} scoreId={score.id} refresh={refresh} />}
    </Surface>)}
    {published && data.notes && <section><h3 className="text-xl">Your teacher’s feedback</h3><p className="mt-3 whitespace-pre-wrap leading-relaxed">{data.notes}</p></section>}
    {!published && can("report.publish") && data.scores.length > 0 && <Surface className="p-5"><h3 className="text-xl">Review and publish</h3><p className="mt-2 text-sm text-ink-muted">Check the evidence and any limitations. Published results become visible to the team.</p><div className="mt-5"><Field label="Teacher feedback">{({ id }) => <textarea id={id} value={notes} onChange={(e) => setNotes(e.target.value)} rows={4} maxLength={12000} className="w-full rounded-md border border-rule-strong bg-paper-raised p-3 text-sm" />}</Field></div><label className="my-5 flex items-center gap-2 text-sm"><input type="checkbox" checked={confirm} onChange={(e) => setConfirm(e.target.checked)} />I reviewed these results and approve publication.</label>{publish.error ? <ErrorState error={publish.error} /> : null}<Button disabled={!confirm || publish.isPending} onClick={() => publish.mutate()}>{publish.isPending ? "Publishing…" : "Publish results"}</Button></Surface>}
  </div>;
}

function CriterionRegrade({ assignmentId, scoreId, refresh }: { assignmentId: string; scoreId: string; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const regrade = useMutation({ mutationFn: () => api.post(`/assignments/${assignmentId}/report/regrade`, { score_id: scoreId, reason }), onSuccess: refresh });
  if (!open) return <Button className="mt-3" size="sm" variant="ghost" onClick={() => setOpen(true)}>Regrade this criterion with AI</Button>;
  return <form className="mt-4 space-y-3" onSubmit={(event) => { event.preventDefault(); regrade.mutate(); }}>
    <p className="text-sm text-ink-muted">This replaces this criterion’s score and any teacher adjustment after grading succeeds. Other criteria stay unchanged.</p>
    <Field label="Reason for regrading">{({ id }) => <TextInput id={id} required maxLength={4000} value={reason} onChange={(event) => setReason(event.target.value)} />}</Field>
    {regrade.error && <ErrorState error={regrade.error} />}
    <div className="flex gap-2"><Button size="sm" type="submit" disabled={regrade.isPending || !reason.trim()}>{regrade.isPending ? "Regrading…" : "Regrade criterion"}</Button><Button size="sm" variant="ghost" disabled={regrade.isPending} onClick={() => setOpen(false)}>Cancel</Button></div>
  </form>;
}

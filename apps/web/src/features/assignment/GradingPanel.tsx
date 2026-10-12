import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle, CircleNotch, MinusCircle, Circle } from "@phosphor-icons/react";
import { api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { Button } from "../../ui/Button";
import { Alert, ErrorState } from "../../ui/Feedback";
import { Badge, Surface } from "../../ui/Layout";

type Criterion = { name: string; max_score: number; status: "queued" | "grading" | "scored" | "not_scored"; score: number | null; reasoning: string };
type Progress = {
  status: "none" | "pending" | "running" | "completed" | "failed";
  stage_id: string;
  failure_reason: string;
  waiting_for_live: boolean;
  can_start: boolean;
  criteria: Criterion[];
};

const statusLabel: Record<Progress["status"], string> = {
  none: "Not graded",
  pending: "Queued",
  running: "Grading",
  completed: "Graded",
  failed: "Stopped",
};

// Grading never starts by itself. The teacher starts it here and watches each
// criterion arrive; the report below fills in when the run completes.
export function GradingPanel({ assignmentId }: { assignmentId: string }) {
  const { can } = useAuth();
  const client = useQueryClient();
  const progress = useQuery({
    queryKey: ["grading", assignmentId],
    queryFn: () => api.get<Progress>(`/assignments/${assignmentId}/grading`),
    refetchInterval: (query) => (["pending", "running"].includes(query.state.data?.status ?? "") ? 2000 : 15000),
  });
  const start = useMutation({
    mutationFn: () => api.post<Progress>(`/assignments/${assignmentId}/grading`, {}),
    onSuccess: (data) => client.setQueryData(["grading", assignmentId], data),
  });
  const status = progress.data?.status;
  const previous = useRef(status);
  useEffect(() => {
    if (previous.current !== status && (status === "completed" || status === "failed")) {
      void client.invalidateQueries({ queryKey: ["report", assignmentId] });
      void client.invalidateQueries({ queryKey: ["assignment", assignmentId] });
    }
    previous.current = status;
  }, [status, assignmentId, client]);

  if (!progress.data) return progress.error ? <ErrorState error={progress.error} /> : null;
  const data = progress.data;
  const busy = data.status === "pending" || data.status === "running";
  const done = data.criteria.filter((c) => c.status === "scored" || c.status === "not_scored").length;

  return <Surface className="p-5">
    <div className="flex flex-wrap items-center justify-between gap-4">
      <div>
        <h3 className="text-xl">AI grading</h3>
        <div className="mt-2 flex items-center gap-3">
          <Badge tone={data.status === "completed" ? "pass" : data.status === "failed" ? "fail" : busy ? "accent" : "neutral"}>{statusLabel[data.status]}</Badge>
          {data.criteria.length > 0 && <span className="numeric text-sm text-ink-muted">{done} of {data.criteria.length} criteria</span>}
        </div>
      </div>
      {can("assessment.grade") && !busy && <Button disabled={!data.can_start || start.isPending} onClick={() => start.mutate()}>
        {start.isPending ? "Starting…" : data.status === "none" ? "Autograde" : "Autograde again"}
      </Button>}
    </div>
    {!busy && !data.can_start && data.status === "none" && <p className="mt-3 text-sm text-ink-muted">Autograde becomes available when the stages it covers are finished.</p>}
    {data.status === "completed" && data.criteria.length > 0 && <p className="mt-3 text-sm text-ink-muted">Autograding again replaces these scores and any adjustments when it finishes. The current report stays until then.</p>}
    {busy && data.waiting_for_live && <div className="mt-4"><Alert tone="info" title="Paused for a live oral round">Grading resumes when the round ends, so the bench keeps its speed.</Alert></div>}
    {data.status === "failed" && <div className="mt-4"><Alert tone="warn" title="Grading stopped">{data.failure_reason || "The assessor could not finish."} Any earlier result in the report is unchanged. Autograde again after checking the grader model in Model settings.</Alert></div>}
    {start.error ? <div className="mt-4"><ErrorState error={start.error} /></div> : null}
    {data.criteria.length > 0 && <ol className="mt-5 divide-y divide-rule border-t border-rule" aria-live="polite">
      {data.criteria.map((c) => <li key={c.name} className="flex gap-3 py-3">
        <span className="mt-0.5 shrink-0">{icon(c.status)}</span>
        <div className="min-w-0 flex-1">
          <div className="flex justify-between gap-4">
            <p className={c.status === "queued" ? "text-ink-muted" : "font-medium"}>{c.name}</p>
            <p className="numeric text-sm">{c.score !== null ? `${c.score} / ${c.max_score}` : c.status === "grading" ? "Reading the record…" : c.status === "not_scored" ? "Not scored" : `— / ${c.max_score}`}</p>
          </div>
          {c.reasoning && <p className="mt-1 text-sm leading-relaxed text-ink-muted">{c.reasoning}</p>}
        </div>
      </li>)}
    </ol>}
  </Surface>;
}

function icon(status: Criterion["status"]) {
  switch (status) {
    case "scored": return <CheckCircle size={18} weight="fill" className="text-pass" aria-label="Scored" />;
    case "grading": return <CircleNotch size={18} className="animate-spin text-accent" aria-label="Grading" />;
    case "not_scored": return <MinusCircle size={18} className="text-warn" aria-label="Not scored" />;
    default: return <Circle size={18} className="text-ink-faint" aria-label="Queued" />;
  }
}

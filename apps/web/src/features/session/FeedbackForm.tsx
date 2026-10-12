import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button } from "../../ui/Button";
import { ErrorState } from "../../ui/Feedback";
import { Field } from "../../ui/Field";
import { Surface } from "../../ui/Layout";

export type RoundFeedback = { rating: number; relevance: string; interruptions: string; problems: string[]; comment: string };

export const relevanceLabel: Record<string, string> = { yes: "Yes", partly: "Partly", no: "No" };
export const interruptionsLabel: Record<string, string> = { about_right: "About right", too_many: "Too many", too_few: "Too few" };
export const problemLabel: Record<string, string> = {
  audio: "Audio cut out or was hard to hear",
  transcript: "The transcript got my words wrong",
  judge_silent: "The judge stayed silent",
  judge_repeated: "The judge repeated a question",
  other: "Something else",
};
const ratingLabel: Record<string, string> = { 1: "1", 2: "2", 3: "3", 4: "4", 5: "5" };

// Asked once the round ends. The answers tune the judge; they never reach a
// grade, and the student can correct them later.
export function FeedbackForm({ sessionId }: { sessionId: string }) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const mine = useQuery({ queryKey: ["feedback", sessionId], queryFn: () => api.get<{ feedback: RoundFeedback | null }>(`/sessions/${sessionId}/feedback/mine`) });
  const save = useMutation({
    mutationFn: (body: RoundFeedback) => api.post(`/sessions/${sessionId}/feedback`, body),
    onSuccess: (_, body) => { client.setQueryData(["feedback", sessionId], { feedback: body }); setEditing(false); },
  });
  if (!mine.data) return mine.error ? <ErrorState error={mine.error} /> : null;
  const saved = mine.data.feedback;

  if (saved && !editing) return <Surface className="p-6">
    <h2 className="text-xl">Thank you for your feedback</h2>
    <p className="mt-2 text-sm text-ink-muted">It helps us improve the judge. It does not change your marks.</p>
    <Button className="mt-4" size="sm" variant="secondary" onClick={() => { save.reset(); setEditing(true); }}>Edit</Button>
  </Surface>;

  return <Surface className="p-6"><form className="space-y-6" onSubmit={(e) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    save.mutate({ rating: Number(form.get("rating")), relevance: String(form.get("relevance")), interruptions: String(form.get("interruptions")), problems: form.getAll("problems").map(String), comment: String(form.get("comment") ?? "") });
  }}>
    <div>
      <h2 className="text-xl">How did the round go?</h2>
      <p className="mt-2 text-sm text-ink-muted">Your teacher can read your answers. They do not change your marks.</p>
    </div>
    <Choice legend="Overall, how was your experience with the judge? (1 poor, 5 excellent)" name="rating" options={ratingLabel} value={saved ? String(saved.rating) : undefined} />
    <Choice legend="Were the judge's questions about your case?" name="relevance" options={relevanceLabel} value={saved?.relevance} />
    <Choice legend="How often did the judge interrupt?" name="interruptions" options={interruptionsLabel} value={saved?.interruptions} />
    <fieldset>
      <legend className="mb-3 text-sm font-medium">Did anything go wrong? (optional)</legend>
      <div className="space-y-2">{Object.entries(problemLabel).map(([key, label]) => <label key={key} className="flex items-center gap-2 text-sm"><input type="checkbox" name="problems" value={key} defaultChecked={saved?.problems.includes(key)} />{label}</label>)}</div>
    </fieldset>
    <Field label="Anything else? (optional)">{({ id }) => <textarea id={id} name="comment" rows={4} maxLength={2000} defaultValue={saved?.comment} className="w-full rounded-md border border-rule-strong bg-paper-raised p-3 text-sm" />}</Field>
    {save.error ? <ErrorState error={save.error} /> : null}
    <div className="flex gap-2">
      <Button type="submit" disabled={save.isPending}>{save.isPending ? "Saving…" : "Submit feedback"}</Button>
      {saved && <Button type="button" variant="ghost" onClick={() => setEditing(false)}>Cancel</Button>}
    </div>
  </form></Surface>;
}

function Choice({ legend, name, options, value }: { legend: string; name: string; options: Record<string, string>; value?: string }) {
  return <fieldset>
    <legend className="mb-3 text-sm font-medium">{legend}</legend>
    <div className="flex flex-wrap gap-4">{Object.entries(options).map(([key, label]) => <label key={key} className="flex items-center gap-2 text-sm"><input type="radio" name={name} value={key} required defaultChecked={value === key} />{label}</label>)}</div>
  </fieldset>;
}

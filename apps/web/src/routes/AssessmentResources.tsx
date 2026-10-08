import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { humanise } from "../lib/format";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { ErrorState } from "../ui/Feedback";
import { Surface } from "../ui/Layout";
import { control } from "./Rubrics";
type Resource = { id: string; title: string; kind: string; visibility: string };
export function AssessmentResources({ assessmentId, sides }: { assessmentId: string; sides: string[] }) {
 const { can } = useAuth(); const client = useQueryClient(); const [notice, setNotice] = useState("");
 const materials = useQuery({ queryKey: ["assessment-resources", assessmentId], queryFn: () => api.get<{ resources: Resource[] }>(`/assessments/${assessmentId}/resources`), enabled: can("knowledge.view") });
 const upload = useMutation({ mutationFn: (form: FormData) => api.upload(`/assessments/${assessmentId}/resources`, form), onSuccess: () => { setNotice("Material added."); void client.invalidateQueries({ queryKey: ["assessment-resources", assessmentId] }); } });
 return <Surface className="mb-10 space-y-4 p-5"><h2>Case materials</h2>{materials.error && <ErrorState error={materials.error} />}{materials.isPending && can("knowledge.view") && <p>Loading materials…</p>}
 <ul className="space-y-2">{materials.data?.resources.map(r => <li key={r.id}>{r.title} · {humanise(r.kind)} · {r.visibility === "all" ? "Everyone" : humanise(r.visibility)}</li>)}</ul>
 {materials.data?.resources.length === 0 && <p className="text-sm text-ink-muted">No materials added yet.</p>}
 {can("knowledge.upload") && <form className="space-y-4" onSubmit={e => { e.preventDefault(); setNotice(""); const form = e.currentTarget; upload.mutate(new FormData(form), { onSuccess: () => form.reset() }); }}><fieldset disabled={upload.isPending} className="space-y-4"><legend>Add material</legend>
 <Field label="Title">{({ id }) => <TextInput id={id} name="title" required maxLength={300} />}</Field>
 <Field label="Kind">{({ id }) => <select id={id} name="kind" className={control}>{["problem", "authority", "statute", "evidence", "guidance", "other"].map(v => <option key={v} value={v}>{humanise(v)}</option>)}</select>}</Field>
 <Field label="Who can see this?">{({ id }) => <select id={id} name="visibility" className={control}><option value="all">Everyone</option><option value="staff">Staff only</option>{sides.filter(s => s !== "all" && s !== "staff").map(s => <option key={s} value={s}>{humanise(s)}</option>)}</select>}</Field>
 <Field label="Document" helper="PDF, Word or text, up to 25 MB.">{({ id }) => <input id={id} name="file" type="file" required accept=".pdf,.docx,.txt,.md" onChange={e => e.currentTarget.setCustomValidity((e.currentTarget.files?.[0]?.size ?? 0) > 25 * 1024 * 1024 ? "Choose a file up to 25 MB." : "")} />}</Field>
 <Button type="submit">{upload.isPending ? "Adding material…" : "Add material"}</Button></fieldset>{upload.error && <ErrorState error={upload.error} />}</form>}
 {notice && <p role="status">{notice}</p>}</Surface>;
}

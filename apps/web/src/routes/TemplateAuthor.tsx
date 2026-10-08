import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type StageKind } from "../lib/api";
import { stageKindLabel } from "../lib/format";
import { Button } from "../ui/Button";
import { Field } from "../ui/Field";
import { ErrorState } from "../ui/Feedback";
import { Surface } from "../ui/Layout";
import { AuthorField, control, type Rubric } from "./Rubrics";

type Actor = { profile_key: string; role: string; display_name: string; presiding: boolean };
export type AuthorStage = { id: string; kind: StageKind; label: string; opens_after_s?: number; due_after_s?: number; grace_s?: number; config?: Record<string, unknown> };
export type AuthorVersion = { rubric_id: string; stages: AuthorStage[]; participation: { sides: string[]; speakers: number; min_team_size: number; max_team_size: number; ai_actors: Actor[] }; defaults?: unknown };
const emptyVersion = (): AuthorVersion => ({ rubric_id: "", stages: [], participation: { sides: ["candidate"], speakers: 1, min_team_size: 1, max_team_size: 1, ai_actors: [] }, defaults: {} });
const defaults: Record<StageKind, Record<string, unknown>> = {
 wait: { instructions: "", visible_resources: [] },
 artifact_submission: { formats: ["pdf", "docx", "txt"], max_bytes: 25 * 1024 * 1024, lock_on_submit: true },
 live_turn: { duration_s: 600, interruptions: "limited" },
 automated_evaluation: { rubric_scope: [], sources: [] },
 human_review: { required: true, overrides_allowed: true },
};
function SelectField({ label, value, onChange, children }: { label: string; value: string; onChange: (v: string) => void; children: ReactNode }) {
 return <Field label={label}>{({ id }) => <select id={id} className={control} value={value} onChange={e => onChange(e.target.value)}>{children}</select>}</Field>;
}
function ListField({ label, values, onChange }: { label: string; values: unknown; onChange: (v: string[]) => void }) {
 return <Field label={`${label} (comma separated)`}>{({ id }) => <input key={JSON.stringify(values)} id={id} className={control} defaultValue={Array.isArray(values) ? values.join(", ") : ""} onBlur={e => onChange(e.target.value.split(",").map(x => x.trim()).filter(Boolean))} />}</Field>;
}
export function TemplateAuthor({ templateId, initial, onClose }: { templateId?: string; initial?: AuthorVersion; onClose: () => void }) {
 const client = useQueryClient();
 const [savedTemplateId, setSavedTemplateId] = useState(templateId);
 const [meta, setMeta] = useState({ key: "", name: "", description: "", assessment_type: "moot_court" });
 const [draft, setDraft] = useState<AuthorVersion>(() => initial ? structuredClone(initial) : emptyVersion());
 const rubrics = useQuery({ queryKey: ["rubrics"], queryFn: () => api.get<{ rubrics: Rubric[] }>("/rubrics") });
 const profiles = useQuery({ queryKey: ["ai-profiles"], queryFn: () => api.get<{ profiles: { key: string; name: string; role: string; is_active: boolean }[] }>("/ai-profiles") });
 const save = useMutation({ mutationFn: async () => {
   let id = savedTemplateId;
   if (!id) { const created = await api.post<{ id: string }>("/templates", meta); id = created.id; setSavedTemplateId(id); }
   return api.post(`/templates/${id}/versions`, { rubric_id: draft.rubric_id, stages: draft.stages, participation: draft.participation, defaults: draft.defaults });
 }, onSuccess: () => { void client.invalidateQueries({ queryKey: ["templates"] }); onClose(); } });
 const participation = draft.participation;
 const actors = participation.ai_actors ?? [];
 const setParticipation = (patch: Partial<AuthorVersion["participation"]>) => setDraft(d => ({ ...d, participation: { ...d.participation, ...patch } }));
 const updateStage = (i: number, patch: Partial<AuthorStage>) => setDraft(d => ({ ...d, stages: d.stages.map((s, j) => i === j ? { ...s, ...patch } : s) }));
 const move = (i: number, delta: number) => setDraft(d => { const stages = [...d.stages]; [stages[i], stages[i + delta]] = [stages[i + delta]!, stages[i]!]; return { ...d, stages }; });
 return <Surface className="my-6 p-5"><form className="space-y-5" onSubmit={e => { e.preventDefault(); save.mutate(); }}>
 <h2>{templateId ? "Create a new version" : "Create template"}</h2>
 <fieldset disabled={save.isPending} className="space-y-5">
 {!savedTemplateId && <><AuthorField label="Template key" value={meta.key} onChange={v => setMeta({ ...meta, key: v })} /><AuthorField label="Name" value={meta.name} onChange={v => setMeta({ ...meta, name: v })} /><AuthorField label="Description" value={meta.description} onChange={v => setMeta({ ...meta, description: v })} /><SelectField label="Assessment type" value={meta.assessment_type} onChange={v => setMeta({ ...meta, assessment_type: v })}><option value="moot_court">Moot court</option><option value="viva">Oral examination</option><option value="interview">Interview</option></SelectField></>}
 {savedTemplateId && !templateId && <p>Template created. Complete and save its first version below.</p>}
 <SelectField label="Rubric" value={draft.rubric_id} onChange={v => setDraft({ ...draft, rubric_id: v })}><option value="">Choose rubric</option>{rubrics.data?.rubrics?.map(r => <option key={r.id} value={r.id}>{r.name}</option>)}</SelectField>
 {rubrics.error && <ErrorState error={rubrics.error} />}
 <ListField label="Sides" values={participation.sides} onChange={sides => setParticipation({ sides })} />
 {([['speakers', 'Speakers'], ['min_team_size', 'Minimum team size'], ['max_team_size', 'Maximum team size']] as const).map(([k, label]) => <AuthorField key={k} label={label} type="number" value={participation[k]} onChange={v => setParticipation({ [k]: Number(v) })} />)}
 <fieldset className="space-y-3"><legend>AI actors</legend>{profiles.error && <ErrorState error={profiles.error} />}
 <SelectField label="Add actor" value="" onChange={key => { const p = profiles.data?.profiles.find(p => p.key === key); if (p) setParticipation({ ai_actors: [...actors, { profile_key: p.key, role: p.role, display_name: p.name, presiding: !actors.length }] }); }}><option value="">Choose an actor</option>{profiles.data?.profiles.filter(p => p.is_active && !actors.some(a => a.profile_key === p.key)).map(p => <option key={p.key} value={p.key}>{p.name}</option>)}</SelectField>
 {actors.map((a, i) => <div key={a.profile_key} className="space-y-2 border border-rule p-3"><p>{a.profile_key}</p><AuthorField label="Display name" value={a.display_name} onChange={v => setParticipation({ ai_actors: actors.map((x, j) => i === j ? { ...x, display_name: v } : x) })} /><SelectField label="Role" value={a.role} onChange={v => setParticipation({ ai_actors: actors.map((x, j) => i === j ? { ...x, role: v } : x) })}>{["judge", "examiner", "moderator", "opponent", "evaluator"].map(role => <option key={role}>{role}</option>)}</SelectField><label className="flex gap-2"><input type="radio" name="presiding" checked={a.presiding} onChange={() => setParticipation({ ai_actors: actors.map((x, j) => ({ ...x, presiding: i === j })) })} />Presiding actor</label><Button type="button" onClick={() => { const remaining = actors.filter((_, j) => i !== j); setParticipation({ ai_actors: remaining.map((x, j) => ({ ...x, presiding: a.presiding ? j === 0 : x.presiding })) }); }}>Remove actor</Button></div>)}
 </fieldset>
 {draft.stages.map((stage, i) => <fieldset key={i} className="space-y-3 border border-rule p-4"><legend>Stage {i + 1}</legend><AuthorField label="Stage key" value={stage.id} onChange={v => updateStage(i, { id: v })} /><AuthorField label="Label" value={stage.label} onChange={v => updateStage(i, { label: v })} /><SelectField label="Stage kind" value={stage.kind} onChange={v => updateStage(i, { kind: v as StageKind, config: structuredClone(defaults[v as StageKind]) })}>{Object.entries(stageKindLabel).map(([k, label]) => <option key={k} value={k}>{label}</option>)}</SelectField>
 {([['opens_after_s', 'Opens after (seconds)'], ['due_after_s', 'Due after (seconds)'], ['grace_s', 'Grace period (seconds)']] as const).map(([k, label]) => <AuthorField key={k} label={label} type="number" value={stage[k] ?? 0} onChange={v => updateStage(i, { [k]: Number(v) })} />)}
 <StageConfig key={stage.kind} stage={stage} onChange={config => updateStage(i, { config })} />
 <div className="flex gap-2"><Button type="button" disabled={i === 0} onClick={() => move(i, -1)}>Move up</Button><Button type="button" disabled={i === draft.stages.length - 1} onClick={() => move(i, 1)}>Move down</Button><Button type="button" onClick={() => setDraft({ ...draft, stages: draft.stages.filter((_, j) => i !== j) })}>Remove stage</Button></div></fieldset>)}
 <Button type="button" onClick={() => setDraft({ ...draft, stages: [...draft.stages, { id: `stage_${crypto.randomUUID().slice(0, 8)}`, label: "New stage", kind: "wait", config: structuredClone(defaults.wait) }] })}>Add stage</Button>
 <div className="flex gap-3"><Button type="submit" disabled={!draft.rubric_id || !draft.stages.length}>Save new version</Button><Button type="button" onClick={onClose}>Cancel</Button></div>
 </fieldset>{save.error && <ErrorState error={save.error} />}</form></Surface>;
}
function StageConfig({ stage, onChange }: { stage: AuthorStage; onChange: (config: Record<string, unknown>) => void }) {
 const c = stage.config ?? {}; const set = (key: string, value: unknown) => onChange({ ...c, [key]: value });
 const text = (key: string, label: string) => <AuthorField label={label} value={String(c[key] ?? "")} onChange={v => set(key, v)} />;
 const number = (key: string, label: string) => <AuthorField label={label} type="number" value={Number(c[key] ?? 0)} onChange={v => set(key, Number(v))} />;
 const list = (key: string, label: string, numeric = false) => <ListField label={label} values={c[key]} onChange={v => set(key, numeric ? v.map(Number) : v)} />;
 const bool = (key: string, label: string) => <label className="flex gap-2"><input type="checkbox" checked={Boolean(c[key])} onChange={e => set(key, e.target.checked)} />{label}</label>;
 switch (stage.kind) {
 case "wait": return <>{text("instructions", "Instructions")}{list("visible_resources", "Visible material kinds")}</>;
 case "artifact_submission": return <>{text("instructions", "Instructions")}{list("formats", "Accepted formats")}{number("max_bytes", "Maximum file size (bytes)")}{bool("lock_on_submit", "Lock after submission")}{text("format_rules", "Format checker")}</>;
 case "live_turn": return <>{number("duration_s", "Duration (seconds)")}{number("speaker_order", "Speaker order (0 for any)")}<SelectField label="Interruptions" value={String(c.interruptions ?? "")} onChange={v => set("interruptions", v)}>{["enabled", "limited", "disabled"].map(v => <option key={v}>{v}</option>)}</SelectField>{list("warn_at_s", "Warnings before end (seconds)", true)}{number("extension_s", "Extension duration (seconds)")}{number("max_extensions", "Maximum extensions")}{list("ai_profiles", "Actor profile keys (empty for all)")}</>;
 case "automated_evaluation": return <>{list("rubric_scope", "Criterion keys")}{list("sources", "Source stage keys (empty for all preceding)")}</>;
 case "human_review": return <>{bool("required", "Review required")}{bool("overrides_allowed", "Allow score overrides")}</>;
 }
}

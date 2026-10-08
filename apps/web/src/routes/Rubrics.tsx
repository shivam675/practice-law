import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { PageHeader, Surface } from "../ui/Layout";
import { ErrorState } from "../ui/Feedback";

export type Criterion = { key: string; name: string; description: string; guidance: string; weight: number; max_score: number; scope: string[] };
export type Rubric = { id?: string; key: string; name: string; description: string; criteria: Criterion[] };
const criterion = (): Criterion => ({key:"",name:"",description:"",guidance:"",weight:1,max_score:10,scope:[]});
export const control = "w-full rounded-md border border-rule-strong bg-paper-raised p-2 text-sm text-ink";
export function AuthorField({label,value,onChange,type="text"}:{label:string;value:string|number;onChange:(v:string)=>void;type?:string}) {
 return <Field label={label}>{({id})=><TextInput id={id} type={type} step={type==="number"?"any":undefined} value={value} onChange={e=>onChange(e.target.value)} />}</Field>;
}
export function Rubrics() {
 const {can}=useAuth(); const client=useQueryClient();
 const list=useQuery({queryKey:["rubrics"],queryFn:()=>api.get<{rubrics:Rubric[]}>("/rubrics")});
 const [draft,setDraft]=useState<Rubric|null>(null);
 const save=useMutation({mutationFn:()=>api.post("/rubrics",draft),onSuccess:()=>{setDraft(null);void client.invalidateQueries({queryKey:["rubrics"]});}});
 const change=(key:keyof Rubric,value:unknown)=>setDraft(d=>d?{...d,[key]:value}:d);
 return <><PageHeader title="Rubrics" meta="Create scoring criteria. Editing creates a new rubric, preserving existing assessments." />
 {can("rubric.create")&&<Button onClick={()=>{save.reset();setDraft({key:"",name:"",description:"",criteria:[criterion()]});}}>Create rubric</Button>}
 {list.error&&<ErrorState error={list.error}/>}
 {draft&&<Surface className="my-6 p-5"><form className="space-y-4" onSubmit={e=>{e.preventDefault();save.mutate();}}>
 <AuthorField label="New rubric key" value={draft.key} onChange={v=>change("key",v)}/><AuthorField label="Name" value={draft.name} onChange={v=>change("name",v)}/><AuthorField label="Description" value={draft.description} onChange={v=>change("description",v)}/>
 {draft.criteria.map((c,i)=><fieldset key={i} className="space-y-3 border border-rule p-4"><legend>Criterion {i+1}</legend>{(["key","name","description","guidance","weight","max_score"] as const).map(k=><AuthorField key={k} label={k==="max_score"?"Maximum score":k.charAt(0).toUpperCase()+k.slice(1)} type={typeof c[k]==="number"?"number":"text"} value={c[k]} onChange={v=>change("criteria",draft.criteria.map((x,j)=>i===j?{...x,[k]:typeof c[k]==="number"?Number(v):v}:x))}/>)}<Button type="button" onClick={()=>change("criteria",draft.criteria.filter((_,j)=>j!==i))}>Remove criterion</Button></fieldset>)}
 <Button type="button" onClick={()=>change("criteria",[...draft.criteria,criterion()])}>Add criterion</Button><div className="flex gap-3"><Button disabled={save.isPending} type="submit">Save new rubric</Button><Button type="button" onClick={()=>setDraft(null)}>Cancel</Button></div>{save.error&&<ErrorState error={save.error}/>}</form></Surface>}
 <div className="mt-6 space-y-4">{list.data?.rubrics?.map(r=><Surface key={r.id} className="p-5"><h2>{r.name}</h2><p className="text-sm text-ink-muted">{r.description}</p><ul className="my-3">{r.criteria.map(c=><li key={c.key}>{c.name} · weight {c.weight} · maximum {c.max_score}</li>)}</ul>{can("rubric.create")&&<Button onClick={()=>{save.reset();setDraft({...r,id:undefined,key:"",name:r.name+" (copy)",criteria:r.criteria.map(({key,name,description,guidance,weight,max_score,scope})=>({key,name,description,guidance,weight,max_score,scope}))});}}>Edit as copy</Button>}</Surface>)}</div></>;
}

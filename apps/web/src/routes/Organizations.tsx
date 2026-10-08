import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { ErrorState, EmptyState, SkeletonRows } from "../ui/Feedback";
import { PageHeader, Surface } from "../ui/Layout";
type Organization = {id:string;name:string;slug:string;status:string;settings:{locale?:string;timezone?:string};max_concurrent_sessions:number};
export function OrganizationSettings(){
 const {can}=useAuth();const client=useQueryClient();const [saved,setSaved]=useState(false);
 const org=useQuery({queryKey:["organization"],queryFn:()=>api.get<Organization>("/organization"),enabled:can("organization.edit")});
 const update=useMutation({mutationFn:(body:Record<string,FormDataEntryValue>)=>api.put("/organization",body),onSuccess:()=>{setSaved(true);void client.invalidateQueries({queryKey:["organization"]});}});
 if(!can("organization.edit"))return <EmptyState title="Access restricted">You do not have permission to change organisation settings.</EmptyState>;
 return <><PageHeader title="Organisation settings" meta="Manage your organisation's name and regional preferences."/>{org.isPending&&<SkeletonRows rows={2}/>} {org.error&&<ErrorState error={org.error}/>}
 {org.data&&<Surface className="max-w-xl p-5"><p className="mb-5 text-sm text-ink-muted">Sign-in organisation: {org.data.slug} · Concurrent session limit: {org.data.max_concurrent_sessions}</p><form className="space-y-4" onSubmit={e=>{e.preventDefault();setSaved(false);update.mutate(Object.fromEntries(new FormData(e.currentTarget)));}}>
 <Field label="Organisation name">{({id})=><TextInput id={id} name="name" required maxLength={200} defaultValue={org.data.name}/>}</Field>
 <Field label="Language tag" helper="For example en or en-IN.">{({id,describedBy})=><TextInput id={id} aria-describedby={describedBy} name="locale" required maxLength={35} defaultValue={org.data.settings.locale??"en"}/>}</Field>
 <Field label="Time zone" helper="For example Asia/Kolkata or UTC.">{({id,describedBy})=><TextInput id={id} aria-describedby={describedBy} name="timezone" required maxLength={100} defaultValue={org.data.settings.timezone??"UTC"}/>}</Field>
 <Button type="submit" disabled={update.isPending}>Save settings</Button>{update.error&&<ErrorState error={update.error}/>} {saved&&<p role="status">Settings saved.</p>}
 </form></Surface>}</>;
}
export function Organizations(){
 const {can}=useAuth();const client=useQueryClient();const [notice,setNotice]=useState("");
 const list=useQuery({queryKey:["organizations"],queryFn:()=>api.get<{organizations:Organization[]}>("/admin/organizations"),enabled:can("platform.organization.manage")});
 const create=useMutation({mutationFn:(body:Record<string,FormDataEntryValue>)=>api.post<{slug:string}>("/admin/organizations",body),onSuccess:data=>{setNotice(`Organisation created. Sign-in organisation: ${data.slug}`);void client.invalidateQueries({queryKey:["organizations"]});}});
 if(!can("platform.organization.manage"))return <EmptyState title="Access restricted">Only platform administrators can manage organisations.</EmptyState>;
 return <><PageHeader title="Organisations" meta="Create an organisation with its own administrator, users and assessments."/><Surface className="mb-6 max-w-xl p-5"><h2>Create organisation</h2><form className="mt-4 space-y-4" onSubmit={e=>{e.preventDefault();setNotice("");const form=e.currentTarget;create.mutate(Object.fromEntries(new FormData(form)),{onSuccess:()=>form.reset()});}}>
 <Field label="Organisation name">{({id})=><TextInput id={id} name="name" required maxLength={200}/>}</Field><Field label="Administrator email">{({id})=><TextInput id={id} name="admin_email" type="email" required/>}</Field><Field label="Initial administrator password" helper="At least 10 characters. Share it privately with the administrator.">{({id,describedBy})=><TextInput id={id} aria-describedby={describedBy} name="admin_password" type="password" autoComplete="new-password" required minLength={10} maxLength={256}/>}</Field><Button type="submit" disabled={create.isPending}>Create organisation</Button>{create.error&&<ErrorState error={create.error}/>} {notice&&<p role="status">{notice}</p>}</form></Surface>
 {list.error&&<ErrorState error={list.error}/>} {list.isPending&&<SkeletonRows rows={2}/>}<ul className="space-y-3">{list.data?.organizations.map(o=><Surface as="li" key={o.id} className="p-5"><h2>{o.name}</h2><p className="text-sm text-ink-muted">{o.slug} · {o.status} · Concurrent sessions: {o.max_concurrent_sessions}</p></Surface>)}</ul></>;
}

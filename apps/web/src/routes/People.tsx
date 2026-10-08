import { useState } from "react";
import { PasswordRecovery } from "./PasswordRecovery";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { humanise } from "../lib/format";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

type Person = { id: string; full_name: string; email: string; status: string; roles: string[] };
type Role = { key: string; name: string };

export function People() {
  const { can, user } = useAuth();
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Person | null>(null);
  const [roles, setRoles] = useState<string[]>([]);
  const people = useQuery({ queryKey: ["users", search, offset], queryFn: () => api.get<{ users: Person[] }>(`/users?q=${encodeURIComponent(search)}&offset=${offset}&limit=25`), enabled: can("user.view") });
  const catalogue = useQuery({ queryKey: ["roles"], queryFn: () => api.get<{ roles: Role[] }>("/users/roles"), enabled: can("user.create") || can("user.assign_role") });
  const update = useMutation({ mutationFn: () => api.put(`/users/${editing!.id}/roles`, { roles }), onSuccess: () => { setEditing(null); void client.invalidateQueries({ queryKey: ["users"] }); } });
  const create = useMutation({ mutationFn: (body: Record<string, unknown>) => api.post("/users", body), onSuccess: () => { setCreating(false); void client.invalidateQueries({ queryKey: ["users"] }); } });
  if (!can("user.view")) return <EmptyState title="Access restricted">You do not have permission to manage people.</EmptyState>;
  return <>
    <PageHeader title="People and access" meta="Manage students and staff in your organisation." actions={can("user.create") ? <Button onClick={() => setCreating(!creating)}>{creating ? "Cancel" : "Add person"}</Button> : undefined} />
    {catalogue.error ? <ErrorState error={catalogue.error} /> : null}
    {creating && <Surface className="mb-6 p-5"><h2 className="text-xl">Add a person</h2><form className="mt-5 grid gap-4 sm:grid-cols-2" onSubmit={async (e) => {
      e.preventDefault();
      const form = new FormData(e.currentTarget);
      create.mutate({ full_name: form.get("name"), email: form.get("email"), password: form.get("password"), roles: [form.get("role")] });
    }}>
      <Field label="Full name">{({ id }) => <TextInput id={id} name="name" required maxLength={200} />}</Field>
      <Field label="Email">{({ id }) => <TextInput id={id} name="email" type="email" required />}</Field>
      <Field label="Initial password" helper="At least 10 characters. Share it securely with this person.">{({ id, describedBy }) => <TextInput id={id} aria-describedby={describedBy} name="password" type="password" minLength={10} required autoComplete="new-password" />}</Field>
      <Field label="Role">{({ id }) => <select id={id} name="role" className="h-11 rounded-md border border-rule-strong bg-paper-sunken px-4" required defaultValue="student">{catalogue.data?.roles.map((r) => <option key={r.key} value={r.key}>{r.name}</option>)}</select>}</Field>
      {create.error ? <ErrorState error={create.error} /> : null}
      <div className="sm:col-span-2"><Button type="submit" disabled={create.isPending || !catalogue.data}>{create.isPending ? "Adding person…" : "Add person"}</Button></div>
    </form></Surface>}
    <div className="mb-6 max-w-md"><Field label="Find a person">{({ id }) => <TextInput id={id} type="search" value={search} onChange={(e) => { setSearch(e.target.value); setOffset(0); }} placeholder="Name or email" />}</Field></div>
    {people.isPending ? <SkeletonRows rows={4} /> : null}
    {people.error ? <ErrorState error={people.error} /> : null}
    {people.data?.users.length === 0 ? <EmptyState title="No people found">Try another name or email.</EmptyState> : null}
    <ul className="divide-y divide-rule">{people.data?.users.map((person) => <li key={person.id} className="py-5">
      <div className="flex flex-wrap items-center justify-between gap-3"><div><p className="font-medium">{person.full_name}</p><p className="text-sm text-ink-muted">{person.email}</p></div><div className="flex flex-wrap items-center gap-2">{person.roles.map((role) => <Badge key={role}>{humanise(role)}</Badge>)}<span className="text-sm text-ink-muted">{humanise(person.status)}</span>{can("user.assign_role") && person.id !== user?.id && <Button size="sm" variant="secondary" onClick={() => { update.reset(); setEditing(person); setRoles(person.roles); }}>Edit roles</Button>}</div></div>
      {can("user.edit") && (person.status === "active" || person.status === "invited") && <PasswordRecovery userId={person.id} name={person.full_name} />}
      {editing?.id === person.id && <form className="mt-4 rounded-md bg-paper-sunken p-4" onSubmit={(e) => { e.preventDefault(); update.mutate(); }}><fieldset><legend className="mb-3 text-sm font-medium">Roles for {person.full_name}</legend><div className="flex flex-wrap gap-4">{catalogue.data?.roles.map((r) => <label key={r.key} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={roles.includes(r.key)} onChange={(e) => setRoles(e.target.checked ? [...roles, r.key] : roles.filter((key) => key !== r.key))} />{r.name}</label>)}</div></fieldset>{update.error ? <ErrorState error={update.error} /> : null}<div className="mt-4 flex gap-2"><Button size="sm" type="submit" disabled={!roles.length || update.isPending}>Save roles</Button><Button size="sm" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button></div></form>}
    </li>)}</ul>
    <div className="mt-6 flex justify-between"><Button variant="secondary" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 25))}>Previous</Button><Button variant="secondary" disabled={people.data?.users.length !== 25} onClick={() => setOffset(offset + 25)}>Next</Button></div>
  </>;

}


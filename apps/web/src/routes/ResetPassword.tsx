import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { request } from "../lib/api";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { ErrorState } from "../ui/Feedback";
import { Surface } from "../ui/Layout";
export function ResetPassword() {
 const [token] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "");
 useEffect(() => { window.history.replaceState(null, "", window.location.pathname); }, []);
 const reset = useMutation({ mutationFn: (password: string) => request("/auth/reset-password", { method:"POST", body:{ token, password }, anonymous:true }) });
 return <main className="mx-auto max-w-md px-5 py-16"><Surface className="space-y-5 p-6"><h1 className="text-2xl">Set a new password</h1>
 {reset.isSuccess ? <><p role="status">Your password has been changed and your previous sessions have ended.</p><a href="/" className="text-accent underline">Sign in</a></> : !token ? <p>This link is missing or has already been opened. Ask your administrator for a new reset link.</p> : <form className="space-y-4" onSubmit={e => {e.preventDefault(); const form=new FormData(e.currentTarget);reset.mutate(String(form.get("password")));}}>
 <p className="text-sm text-ink-muted">Choose a password with at least 10 characters. Reset links expire after 15 minutes and work once.</p>
 <Field label="New password">{({id})=><TextInput id={id} name="password" type="password" autoComplete="new-password" required minLength={10} maxLength={256} />}</Field>
 <Button type="submit" disabled={reset.isPending}>{reset.isPending ? "Saving…" : "Set password"}</Button>{reset.error && <ErrorState error={reset.error} />}</form>}
 </Surface></main>;
}

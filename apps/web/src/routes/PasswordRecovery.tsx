import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { ErrorState } from "../ui/Feedback";
export function PasswordRecovery({userId,name}:{userId:string;name:string}){
 const recovery=useMutation({mutationFn:()=>api.post<{reset_path:string;expires_at:string}>(`/users/${userId}/password-reset`)});
 return <div className="mt-3 space-y-3"><Button size="sm" variant="secondary" disabled={recovery.isPending} onClick={()=>recovery.mutate()}>Create password reset link</Button>{recovery.error&&<ErrorState error={recovery.error}/>}
 {recovery.data&&<div className="space-y-2 rounded-md border border-rule p-4"><p className="text-sm">Share this link privately with {name}. It works once and expires at {new Date(recovery.data.expires_at).toLocaleTimeString()}. Creating another link invalidates this one.</p><Field label="Temporary reset link">{({id})=><TextInput id={id} readOnly value={new URL(recovery.data.reset_path,window.location.origin).href} onFocus={e=>e.currentTarget.select()}/>}</Field><Button size="sm" variant="ghost" onClick={()=>recovery.reset()}>Hide link</Button></div>}</div>;
}

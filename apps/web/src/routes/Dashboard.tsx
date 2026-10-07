import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, CheckCircle, Gear, UsersThree } from "@phosphor-icons/react";
import { api, type Assessment, type Assignment, type ModelBinding } from "../lib/api";
import { useAuth } from "../lib/auth";
import { humanise } from "../lib/format";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { ErrorState, SkeletonRows } from "../ui/Feedback";

export function Dashboard() {
  const { can, user } = useAuth();
  const platform = can("platform.model.configure");
  const admin = can("user.create");
  const work = useQuery({ queryKey: ["assignments"], queryFn: () => api.get<{ assignments: Assignment[] }>("/assignments"), enabled: can("assessment.view") });
  const assessments = useQuery({ queryKey: ["assessments"], queryFn: () => api.get<{ assessments: Assessment[] }>("/assessments"), enabled: can("assessment.view") });
  const bindings = useQuery({ queryKey: ["bindings"], queryFn: () => api.get<{ bindings: ModelBinding[] }>("/platform/bindings"), enabled: platform });
  const assignments = work.data?.assignments ?? [];
  const review = assignments.filter((a) => a.status === "awaiting_review" || a.current_stage_kind === "human_review");
  const active = assignments.filter((a) => a.status === "in_progress" && a.current_stage_kind !== "human_review");
  const drafts = assessments.data?.assessments.filter((a) => a.status === "draft") ?? [];
  const title = platform ? "Platform overview" : admin ? "Your organisation" : "Teaching workspace";
  const actions = [
    { allowed: platform, to: "/admin/models", title: "Models and providers", text: "Connect the models that power assessments.", icon: Gear },
    { allowed: admin, to: "/admin/users", title: "People and access", text: "Add students and staff. Manage their roles.", icon: UsersThree },
    { allowed: can("assessment.create"), to: "/assessments", title: "Plan an assessment", text: "Choose a template, set a date and assign teams.", icon: ArrowRight },
    { allowed: can("team.view"), to: "/teams", title: "Prepare your teams", text: "Check speakers and team membership.", icon: UsersThree },
  ].filter((a) => a.allowed);
  return <>
    <PageHeader title={title} meta={`Welcome, ${user?.full_name ?? "colleague"}. ${platform ? "Keep assessment services ready." : admin ? "Keep your people and assessments organised." : "See what needs your attention today."}`} />
    <div className="grid gap-10 lg:grid-cols-[minmax(0,1.6fr)_minmax(240px,1fr)]">
      <section aria-labelledby="attention-title">
        <h2 id="attention-title" className="text-xl">Needs attention</h2>
        {work.isLoading || assessments.isLoading ? <SkeletonRows rows={3} /> : null}
        {work.error || assessments.error ? <ErrorState error={work.error ?? assessments.error} /> : null}
        {work.data && assessments.data && <>
          <dl className="my-6 grid grid-cols-3 gap-4 border-y border-rule py-5">
            {[["In progress", active.length], ["For review", review.length], ["Drafts", drafts.length]].map(([label, value]) => <div key={label}><dt className="text-sm text-ink-muted">{label}</dt><dd className="numeric mt-2 text-2xl">{value}</dd></div>)}
          </dl>
          {review.length === 0 && drafts.length === 0 ? <div className="py-6"><CheckCircle size={28} className="text-pass" aria-hidden /><h3 className="mt-3 text-lg">No work waiting for review</h3><p className="mt-2 text-sm text-ink-muted">Active assessments appear below. Results stay private until a teacher publishes them.</p></div> : null}
          <ul className="space-y-3">
            {review.map((a) => <li key={a.id}><Link className="flex items-center justify-between gap-4 rounded-md border border-rule bg-paper-raised p-4 hover:border-accent-rule" to={`/work/${a.id}`}><div><Badge tone="warn">Review required</Badge><h3 className="mt-2 text-lg">{a.team_name}</h3><p className="text-sm text-ink-muted">{a.assessment_title}</p></div><ArrowRight aria-hidden /></Link></li>)}
            {drafts.map((a) => <li key={a.id}><Link className="block rounded-md border border-rule p-4 hover:bg-paper-sunken" to={`/assessments/${a.id}`}><Badge>Draft</Badge><h3 className="mt-2 text-lg">{a.title}</h3><p className="text-sm text-ink-muted">Check the details before publishing.</p></Link></li>)}
          </ul>
          <h2 className="mb-4 mt-8 text-xl">Work in progress</h2>
          {active.length === 0 ? <p className="text-sm text-ink-muted">No teams are working on an assessment yet.</p> : <ul className="divide-y divide-rule">{active.slice(0, 8).map((a) => <li key={a.id}><Link to={`/work/${a.id}`} className="flex items-center justify-between gap-4 py-4 hover:text-accent"><div><p className="font-medium">{a.team_name}</p><p className="text-sm text-ink-muted">{a.assessment_title}</p></div><span className="text-right text-sm text-ink-muted">{humanise(a.current_stage_id ?? a.status)}</span></Link></li>)}</ul>}
        </>}
      </section>
      <aside className="space-y-6">
        <Surface className="p-5"><h2 className="text-xl">{platform ? "Platform controls" : admin ? "Organisation tools" : "Prepare the next round"}</h2><ul className="mt-4 divide-y divide-rule">{actions.map(({ to, title: label, text, icon: Icon }) => <li key={to}><Link to={to} className="group flex gap-3 py-4"><Icon size={20} className="mt-1 shrink-0 text-accent" aria-hidden /><div><p className="font-medium group-hover:underline">{label}</p><p className="mt-1 text-sm text-ink-muted">{text}</p></div></Link></li>)}</ul></Surface>
        {platform && <section><h2 className="text-lg">Model routing</h2>{bindings.error ? <ErrorState error={bindings.error} /> : null}{bindings.isPending ? <SkeletonRows rows={2} /> : <ul className="mt-3 space-y-3">{["judge", "grader", "monitor", "embedding"].map((tier) => { const route = bindings.data?.bindings.find((b) => b.tier === tier); return <li key={tier} className="flex justify-between gap-3 text-sm"><span>{humanise(tier)}</span><span className={route ? "max-w-44 truncate text-ink-muted" : "text-warn"} title={route?.model}>{route?.model ?? "Not configured"}</span></li>; })}</ul>}</section>}
      </aside>
    </div>
  </>;
}


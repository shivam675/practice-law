import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "@phosphor-icons/react";
import { api, type AiProfile } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { Alert, EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

/**
 * AI actor profiles: prompt, parameters, personality, capabilities.
 *
 * Profiles are versioned, not edited. A grade stamped with version 3 of a
 * judge has to stay explainable after someone tunes it to version 4, so
 * saving a change supersedes rather than overwrites and the old version stays
 * visible here.
 *
 * Capabilities are a checklist of what the coordinator enforces in code. The
 * prompt cannot grant a power that is not on this list, and listing one the
 * coordinator does not know is refused rather than silently ignored.
 */

type ProfilesResponse = {
  profiles: AiProfile[];
  roles: AiProfile["role"][];
  tiers: AiProfile["model_tier"][];
  capabilities: string[];
};

export function AiProfiles() {
  const { can } = useAuth();
  const [creating, setCreating] = useState(false);
  const [editingID, setEditingID] = useState<string | null>(null);

  const profiles = useQuery({
    queryKey: ["ai-profiles"],
    queryFn: () => api.get<ProfilesResponse>("/ai-profiles"),
  });

  const data = profiles.data;
  const current = data?.profiles.filter((p) => p.is_active) ?? [];
  const superseded = data?.profiles.filter((p) => !p.is_active) ?? [];

  return (
    <>
      <PageHeader
        title="AI actors"
        meta="Who the model is in a session: its prompt, how firm it is, and what the coordinator will let it do."
        actions={
          can("ai_profile.create") ? (
            <Button variant={creating ? "ghost" : "primary"} onClick={() => setCreating((v) => !v)}>
              {creating ? "Cancel" : (<><Plus size={16} aria-hidden /> New actor</>)}
            </Button>
          ) : undefined
        }
      />

      {profiles.isPending ? <SkeletonRows rows={2} /> : null}
      {profiles.error ? <ErrorState error={profiles.error} /> : null}

      {creating && data ? (
        <div className="mb-8">
          <Surface className="px-5 py-5">
            <h2 className="text-lg">New actor</h2>
            <div className="mt-5">
              <ProfileForm options={data} onDone={() => setCreating(false)} />
            </div>
          </Surface>
        </div>
      ) : null}

      {data && current.length === 0 && !creating ? (
        <EmptyState
          title="No actors configured"
          action={can("ai_profile.create") ? <Button onClick={() => setCreating(true)}>Create an actor</Button> : undefined}
        >
          A moot round needs at least one judge. Until one exists, live stages
          have nobody to run them.
        </EmptyState>
      ) : null}

      <ul className="space-y-3">
        {current.map((profile) => (
          <Surface as="li" key={profile.id} className="px-5 py-4">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <h2 className="text-base">{profile.name}</h2>
                  <Badge tone="accent">{profile.role}</Badge>
                  <Badge>v{profile.version}</Badge>
                  <Badge>{profile.model_tier} tier</Badge>
                </div>
                <p className="mt-2 line-clamp-2 text-sm text-ink-muted">
                  {profile.system_prompt || "No system prompt yet."}
                </p>
                <p className="mt-2 text-xs text-ink-faint">
                  temperature {profile.temperature}
                  {profile.capabilities.length > 0 ? ` · ${profile.capabilities.join(", ")}` : ""}
                  {profile.focus.length > 0 ? ` · focus: ${profile.focus.join(", ")}` : ""}
                </p>
              </div>

              {can("ai_profile.edit") ? (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setEditingID((id) => (id === profile.id ? null : profile.id))}
                >
                  {editingID === profile.id ? "Close" : "Revise"}
                </Button>
              ) : null}
            </div>

            {editingID === profile.id && data ? (
              <div className="mt-5 border-t border-rule pt-5">
                <Alert tone="info">
                  Saving creates version {profile.version + 1}. Version {profile.version} stays on
                  record so anything already graded with it can still be explained.
                </Alert>
                <div className="mt-5">
                  <ProfileForm profile={profile} options={data} onDone={() => setEditingID(null)} />
                </div>
              </div>
            ) : null}
          </Surface>
        ))}
      </ul>

      {superseded.length > 0 ? (
        <details className="mt-10">
          <summary className="cursor-pointer text-sm text-ink-muted">
            {superseded.length} superseded {superseded.length === 1 ? "version" : "versions"}
          </summary>
          <ul className="mt-3 space-y-2">
            {superseded.map((profile) => (
              <li key={profile.id} className="flex items-center gap-3 text-sm text-ink-muted">
                <Badge>v{profile.version}</Badge>
                <span className="truncate">
                  {profile.name} · {profile.role} · temperature {profile.temperature}
                </span>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </>
  );
}

/* ---------------------------------------------------------------------- form */

function ProfileForm({
  profile,
  options,
  onDone,
}: {
  profile?: AiProfile;
  options: ProfilesResponse;
  onDone: () => void;
}) {
  const queryClient = useQueryClient();
  const revising = profile !== undefined;

  const [key, setKey] = useState(profile?.key ?? "");
  const [name, setName] = useState(profile?.name ?? "");
  const [role, setRole] = useState<AiProfile["role"]>(profile?.role ?? "judge");
  const [tier, setTier] = useState<AiProfile["model_tier"]>(profile?.model_tier ?? "judge");
  const [prompt, setPrompt] = useState(profile?.system_prompt ?? "");
  const [temperature, setTemperature] = useState(profile?.temperature ?? 0.4);
  const [voice, setVoice] = useState(profile?.voice ?? "");
  const [focus, setFocus] = useState((profile?.focus ?? []).join(", "));
  const [ragSources, setRagSources] = useState((profile?.rag_sources ?? []).join(", "));
  const [capabilities, setCapabilities] = useState<string[]>(
    profile?.capabilities ?? ["ask_question", "evaluate"],
  );

  const save = useMutation({
    mutationFn: () => {
      const body = {
        key: revising ? profile.key : key,
        name,
        role,
        model_tier: tier,
        system_prompt: prompt,
        temperature,
        voice,
        personality: profile?.personality ?? {},
        interruption_policy: profile?.interruption_policy ?? {},
        focus: splitList(focus),
        capabilities,
        rag_sources: splitList(ragSources),
      };
      return revising
        ? api.post<AiProfile>(`/ai-profiles/${profile.id}/versions`, body)
        : api.post<AiProfile>("/ai-profiles", body);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["ai-profiles"] });
      onDone();
    },
  });

  const selectClass =
    "h-11 w-full rounded-md border border-rule-strong bg-paper-sunken px-4 text-sm text-ink " +
    "focus:outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid gap-5 md:grid-cols-2">
        {revising ? null : (
          <Field label="Key" helper="Permanent. Versions of this actor share it.">
            {({ id, describedBy }) => (
              <TextInput
                id={id}
                aria-describedby={describedBy}
                required
                value={key}
                onChange={(e) => setKey(e.target.value)}
                placeholder="strict_appellate_judge"
              />
            )}
          </Field>
        )}

        <Field label="Name">
          {({ id }) => (
            <TextInput
              id={id}
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Strict Appellate Judge"
            />
          )}
        </Field>

        <Field label="Role">
          {({ id }) => (
            <select id={id} className={selectClass} value={role} onChange={(e) => setRole(e.target.value as AiProfile["role"])}>
              {options.roles.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          )}
        </Field>

        <Field label="Model tier" helper="Which binding on the Models page answers for this actor.">
          {({ id, describedBy }) => (
            <select
              id={id}
              aria-describedby={describedBy}
              className={selectClass}
              value={tier}
              onChange={(e) => setTier(e.target.value as AiProfile["model_tier"])}
            >
              {options.tiers.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          )}
        </Field>

        <Field label="Temperature">
          {({ id }) => (
            <TextInput
              id={id}
              type="number"
              className="numeric"
              step={0.05}
              min={0}
              max={2}
              value={temperature}
              onChange={(e) => {
                const next = Number(e.target.value);
                if (!Number.isNaN(next)) setTemperature(next);
              }}
            />
          )}
        </Field>

        <Field label="Voice" helper="Blank until the media plane exists.">
          {({ id, describedBy }) => (
            <TextInput
              id={id}
              aria-describedby={describedBy}
              value={voice}
              onChange={(e) => setVoice(e.target.value)}
              placeholder="bm_george"
            />
          )}
        </Field>
      </div>

      <Field
        label="System prompt"
        helper="Never interpolate a student's work into this. Submissions reach the model as a delimited user block."
      >
        {({ id, describedBy }) => (
          <textarea
            id={id}
            aria-describedby={describedBy}
            rows={10}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            className="w-full rounded-md border border-rule-strong bg-paper-sunken px-4 py-3 font-mono text-[13px] leading-relaxed text-ink
              placeholder:text-ink-faint focus:outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            placeholder="You are an appellate judge hearing oral argument..."
          />
        )}
      </Field>

      <fieldset>
        <legend className="text-sm font-medium text-ink">Capabilities</legend>
        <p className="mt-1 text-xs text-ink-muted">
          Enforced by the coordinator in code, never by the prompt.
        </p>
        <div className="mt-3 flex flex-wrap gap-4">
          {options.capabilities.map((capability) => (
            <label key={capability} className="flex items-center gap-2 text-sm text-ink">
              <input
                type="checkbox"
                className="size-4 accent-accent"
                checked={capabilities.includes(capability)}
                onChange={(e) =>
                  setCapabilities((prev) =>
                    e.target.checked ? [...prev, capability] : prev.filter((c) => c !== capability),
                  )
                }
              />
              {capability}
            </label>
          ))}
        </div>
      </fieldset>

      <div className="grid gap-5 md:grid-cols-2">
        <Field label="Focus" helper="Comma separated. What this actor presses on.">
          {({ id, describedBy }) => (
            <TextInput
              id={id}
              aria-describedby={describedBy}
              value={focus}
              onChange={(e) => setFocus(e.target.value)}
              placeholder="precedent, logical_consistency, legal_authority"
            />
          )}
        </Field>

        <Field label="Retrieval sources" helper="Comma separated. Which case material it may cite.">
          {({ id, describedBy }) => (
            <TextInput
              id={id}
              aria-describedby={describedBy}
              value={ragSources}
              onChange={(e) => setRagSources(e.target.value)}
              placeholder="problem, authorities, statutes"
            />
          )}
        </Field>
      </div>

      <div className="flex gap-3">
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving" : revising ? `Save as version ${profile.version + 1}` : "Create actor"}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
      </div>

      {save.error ? <ErrorState error={save.error} /> : null}
    </form>
  );
}

function splitList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

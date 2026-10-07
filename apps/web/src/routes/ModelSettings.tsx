import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle, Plus, Trash } from "@phosphor-icons/react";
import {
  api,
  type ConnectionTest,
  type ModelBinding,
  type ModelProvider,
  type ModelTier,
} from "../lib/api";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { Badge, PageHeader, SectionTitle, Surface } from "../ui/Layout";
import { Alert, EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

/**
 * Platform model settings.
 *
 * Two things live here and they are deliberately separate. A provider is an
 * endpoint and a credential. Routing is which model answers which tier, and
 * with what parameters. Changing the second is routine; changing the first is
 * not, which is why the credential field is write-only and shows a hint
 * rather than a value.
 *
 * Every tier can be tested against the real provider from this page. A
 * configuration nobody has dialled is a guess, and finding out during a graded
 * session is the wrong time.
 */

const tierHelp: Record<ModelTier, string> = {
  monitor: "Interruption bids and claim extraction. Resident, and on the live latency budget.",
  judge: "Question generation when the bank misses. Resident, ~700 ms.",
  grader: "Memorial grading, session evaluation, reports. Offline; seconds are fine.",
  embedding: "Retrieval over case material, transcripts and the question bank.",
};

type ProvidersResponse = { providers: ModelProvider[]; tiers: ModelTier[] };
type BindingsResponse = { bindings: ModelBinding[]; tiers: ModelTier[] };

export function ModelSettings() {
  const providers = useQuery({
    queryKey: ["platform", "providers"],
    queryFn: () => api.get<ProvidersResponse>("/platform/providers"),
  });
  const bindings = useQuery({
    queryKey: ["platform", "bindings"],
    queryFn: () => api.get<BindingsResponse>("/platform/bindings"),
  });

  // Model names learned from a connection test, so the routing menus offer
  // what the provider actually serves rather than a free-text box to typo.
  const [known, setKnown] = useState<Record<string, string[]>>({});

  return (
    <>
      <PageHeader
        title="Models"
        meta="Providers and per-tier routing for the whole platform. Not organisation settings: there is one box behind all of them."
      />

      {providers.error ? <ErrorState error={providers.error} /> : null}
      {providers.isPending ? <SkeletonRows rows={2} /> : null}

      {providers.data ? (
        <Providers
          providers={providers.data.providers}
          onModels={(id, models) => setKnown((prev) => ({ ...prev, [id]: models }))}
        />
      ) : null}

      {providers.data && providers.data.providers.length > 0 ? (
        <section className="mt-12">
          <SectionTitle>Routing</SectionTitle>
          {bindings.error ? <ErrorState error={bindings.error} /> : null}
          {bindings.data ? (
            <Routing
              tiers={bindings.data.tiers}
              bindings={bindings.data.bindings}
              providers={providers.data.providers.filter((p) => p.is_active)}
              known={known}
            />
          ) : null}
        </section>
      ) : null}
    </>
  );
}

/* ------------------------------------------------------------------ providers */

function Providers({
  providers,
  onModels,
}: {
  providers: ModelProvider[];
  onModels: (providerId: string, models: string[]) => void;
}) {
  const [adding, setAdding] = useState(false);

  return (
    <section>
      <SectionTitle
        trailing={
          <Button size="sm" variant={adding ? "ghost" : "secondary"} onClick={() => setAdding((v) => !v)}>
            {adding ? "Cancel" : (<><Plus size={14} aria-hidden /> Add provider</>)}
          </Button>
        }
      >
        Providers
      </SectionTitle>

      {adding ? <ProviderForm onDone={() => setAdding(false)} /> : null}

      {providers.length === 0 && !adding ? (
        <EmptyState title="No providers yet" action={<Button onClick={() => setAdding(true)}>Add a provider</Button>}>
          An OpenAI-compatible endpoint is enough: Ollama, vLLM, llama.cpp or a
          hosted provider all speak the same shape.
        </EmptyState>
      ) : null}

      <ul className="space-y-3">
        {providers.map((provider) => (
          <ProviderRow key={provider.id} provider={provider} onModels={onModels} />
        ))}
      </ul>
    </section>
  );
}

function ProviderRow({
  provider,
  onModels,
}: {
  provider: ModelProvider;
  onModels: (providerId: string, models: string[]) => void;
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const test = useMutation({
    mutationFn: () => api.post<ConnectionTest>("/platform/test", { provider_id: provider.id }),
    onSuccess: (result) => {
      if (result.models.length > 0) onModels(provider.id, result.models);
    },
  });

  const remove = useMutation({
    mutationFn: () => api.del<void>(`/platform/providers/${provider.id}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform"] });
    },
  });

  const toggle = useMutation({
    mutationFn: (is_active: boolean) =>
      api.patch<ModelProvider>(`/platform/providers/${provider.id}`, { is_active }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform"] });
    },
  });

  return (
    <Surface as="li" className="px-5 py-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="text-base">{provider.name}</h3>
            <Badge tone={provider.is_active ? "accent" : "neutral"}>
              {provider.is_active ? provider.key : "disabled"}
            </Badge>
          </div>
          <p className="mt-1 truncate text-sm text-ink-muted">{provider.base_url}</p>
          <p className="mt-1 text-xs text-ink-faint">
            {provider.has_api_key ? `Bearer token ${provider.api_key_hint}` : "No credential stored"}
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-2">
          <Button size="sm" variant="secondary" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing" : "Test"}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setEditing((v) => !v)}>
            {editing ? "Close" : "Edit"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => toggle.mutate(!provider.is_active)}
            disabled={toggle.isPending}
          >
            {provider.is_active ? "Disable" : "Enable"}
          </Button>
          <Button
            size="sm"
            variant="danger"
            aria-label={`Delete ${provider.name}`}
            onClick={() => setConfirmingDelete(true)}
          >
            <Trash size={14} aria-hidden />
          </Button>
        </div>
      </div>

      {confirmingDelete ? (
        <div className="mt-4">
          <Alert tone="warn" title={`Delete ${provider.name}?`}>
            <p>The stored credential goes with it. A provider bound to a tier cannot be deleted.</p>
            <div className="mt-3 flex gap-2">
              <Button size="sm" variant="danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
                {remove.isPending ? "Deleting" : "Delete"}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setConfirmingDelete(false)}>
                Keep
              </Button>
            </div>
          </Alert>
        </div>
      ) : null}

      {remove.error ? <div className="mt-4"><ErrorState error={remove.error} /></div> : null}
      {test.error ? <div className="mt-4"><ErrorState error={test.error} /></div> : null}
      {test.data ? <div className="mt-4"><TestOutcome result={test.data} /></div> : null}

      {editing ? (
        <div className="mt-5 border-t border-rule pt-5">
          <ProviderForm provider={provider} onDone={() => setEditing(false)} />
        </div>
      ) : null}
    </Surface>
  );
}

function ProviderForm({ provider, onDone }: { provider?: ModelProvider; onDone: () => void }) {
  const queryClient = useQueryClient();
  const editing = provider !== undefined;

  const [key, setKey] = useState(provider?.key ?? "");
  const [name, setName] = useState(provider?.name ?? "");
  const [baseURL, setBaseURL] = useState(provider?.base_url ?? "");
  const [apiKey, setApiKey] = useState("");

  const save = useMutation({
    mutationFn: () =>
      editing
        ? api.patch<ModelProvider>(`/platform/providers/${provider.id}`, {
            name,
            base_url: baseURL,
            // Omitted entirely when untouched, so editing a URL does not wipe
            // the credential.
            ...(apiKey ? { api_key: apiKey } : {}),
          })
        : api.post<ModelProvider>("/platform/providers", {
            key,
            name,
            kind: "openai_compatible",
            base_url: baseURL,
            api_key: apiKey,
          }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform"] });
      onDone();
    },
  });

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid gap-5 md:grid-cols-2">
        {editing ? null : (
          <Field label="Key" helper="Permanent. Used in the request ledger and in logs.">
            {({ id, describedBy }) => (
              <TextInput
                id={id}
                aria-describedby={describedBy}
                required
                value={key}
                onChange={(e) => setKey(e.target.value)}
                placeholder="remote_ollama"
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
              placeholder="Remote Ollama"
            />
          )}
        </Field>

        <Field label="Base URL" helper="With or without the /v1 suffix; both work.">
          {({ id, describedBy }) => (
            <TextInput
              id={id}
              aria-describedby={describedBy}
              required
              inputMode="url"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder="https://ollama.example.com"
            />
          )}
        </Field>

        <Field
          label="Bearer token"
          helper={
            editing
              ? "Leave blank to keep the stored token. Anything entered replaces it."
              : "Sent as an Authorization header. Encrypted at rest and never shown again."
          }
        >
          {({ id, describedBy }) => (
            <TextInput
              id={id}
              aria-describedby={describedBy}
              type="password"
              autoComplete="off"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={editing && provider.has_api_key ? provider.api_key_hint : ""}
            />
          )}
        </Field>
      </div>

      <div className="flex gap-3">
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving" : editing ? "Save changes" : "Add provider"}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
      </div>

      {save.error ? <ErrorState error={save.error} /> : null}
    </form>
  );
}

/* -------------------------------------------------------------------- routing */

const defaults: Record<ModelTier, { temperature: number; max_tokens: number; timeout_ms: number }> = {
  monitor: { temperature: 0.2, max_tokens: 256, timeout_ms: 2000 },
  judge: { temperature: 0.4, max_tokens: 512, timeout_ms: 3000 },
  grader: { temperature: 0.2, max_tokens: 2048, timeout_ms: 120000 },
  embedding: { temperature: 0, max_tokens: 1, timeout_ms: 30000 },
};

type Draft = {
  tier: ModelTier;
  provider_id: string;
  model: string;
  temperature: number;
  top_p: number;
  max_tokens: number;
  timeout_ms: number;
};

function Routing({
  tiers,
  bindings,
  providers,
  known,
}: {
  tiers: ModelTier[];
  bindings: ModelBinding[];
  providers: ModelProvider[];
  known: Record<string, string[]>;
}) {
  const queryClient = useQueryClient();

  const [drafts, setDrafts] = useState<Draft[]>(() =>
    tiers.map((tier) => {
      const existing = bindings.find((b) => b.tier === tier);
      if (existing) return { ...existing };
      return {
        tier,
        provider_id: "",
        model: "",
        top_p: 1,
        ...defaults[tier],
      };
    }),
  );

  const save = useMutation({
    mutationFn: () =>
      api.put<BindingsResponse>("/platform/bindings", {
        // A tier with no provider chosen is simply not sent; the rest still save.
        bindings: drafts.filter((d) => d.provider_id && d.model.trim()),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform", "bindings"] });
    },
  });

  const patch = (tier: ModelTier, change: Partial<Draft>) =>
    setDrafts((prev) => prev.map((d) => (d.tier === tier ? { ...d, ...change } : d)));

  return (
    <>
      <ul className="space-y-3">
        {drafts.map((draft) => (
          <TierRow
            key={draft.tier}
            draft={draft}
            providers={providers}
            models={known[draft.provider_id] ?? []}
            bound={bindings.some((b) => b.tier === draft.tier)}
            onChange={(change) => patch(draft.tier, change)}
          />
        ))}
      </ul>

      <div className="mt-5 flex items-center gap-3">
        <Button disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "Saving" : "Save routing"}
        </Button>
        {save.isSuccess && !save.isPending ? (
          <span className="inline-flex items-center gap-1.5 text-sm text-pass">
            <CheckCircle size={16} weight="fill" aria-hidden />
            Saved
          </span>
        ) : null}
      </div>

      {save.error ? <div className="mt-4"><ErrorState error={save.error} /></div> : null}
    </>
  );
}

function TierRow({
  draft,
  providers,
  models,
  bound,
  onChange,
}: {
  draft: Draft;
  providers: ModelProvider[];
  models: string[];
  bound: boolean;
  onChange: (change: Partial<Draft>) => void;
}) {
  const test = useMutation({
    mutationFn: () => api.post<ConnectionTest>("/platform/test", { tier: draft.tier }),
  });

  const selectClass =
    "h-11 w-full rounded-md border border-rule-strong bg-paper-sunken px-4 text-sm text-ink " +
    "focus:outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

  return (
    <Surface as="li" className="px-5 py-4">
      <div className="flex items-baseline justify-between gap-4">
        <div>
          <h3 className="text-base capitalize">{draft.tier}</h3>
          <p className="mt-1 text-xs text-ink-muted">{tierHelp[draft.tier]}</p>
        </div>
        <Button
          size="sm"
          variant="secondary"
          disabled={!bound || test.isPending}
          title={bound ? undefined : "Save this tier before testing it"}
          onClick={() => test.mutate()}
        >
          {test.isPending ? "Testing" : "Test"}
        </Button>
      </div>

      <div className="mt-4 grid gap-4 md:grid-cols-2">
        <Field label="Provider">
          {({ id }) => (
            <select
              id={id}
              className={selectClass}
              value={draft.provider_id}
              onChange={(e) => onChange({ provider_id: e.target.value })}
            >
              <option value="">Not routed</option>
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          )}
        </Field>

        <Field
          label="Model"
          helper={models.length > 0 ? `${models.length} offered by this provider` : "Test the provider to load its model list"}
        >
          {({ id, describedBy }) => (
            <>
              <TextInput
                id={id}
                aria-describedby={describedBy}
                list={`${draft.tier}-models`}
                value={draft.model}
                onChange={(e) => onChange({ model: e.target.value })}
                placeholder="qwen3:8b"
              />
              <datalist id={`${draft.tier}-models`}>
                {models.map((m) => (
                  <option key={m} value={m} />
                ))}
              </datalist>
            </>
          )}
        </Field>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <NumberField
          label="Temperature"
          value={draft.temperature}
          step={0.05}
          min={0}
          max={2}
          onChange={(v) => onChange({ temperature: v })}
        />
        <NumberField
          label="Top p"
          value={draft.top_p}
          step={0.05}
          min={0.01}
          max={1}
          onChange={(v) => onChange({ top_p: v })}
        />
        <NumberField
          label="Max tokens"
          value={draft.max_tokens}
          step={1}
          min={1}
          max={131072}
          onChange={(v) => onChange({ max_tokens: Math.round(v) })}
        />
        <NumberField
          label="Timeout (ms)"
          value={draft.timeout_ms}
          step={100}
          min={200}
          max={600000}
          onChange={(v) => onChange({ timeout_ms: Math.round(v) })}
        />
      </div>

      {test.error ? <div className="mt-4"><ErrorState error={test.error} /></div> : null}
      {test.data ? <div className="mt-4"><TestOutcome result={test.data} /></div> : null}
    </Surface>
  );
}

function NumberField({
  label,
  value,
  step,
  min,
  max,
  onChange,
}: {
  label: string;
  value: number;
  step: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
}) {
  return (
    <Field label={label}>
      {({ id }) => (
        <TextInput
          id={id}
          type="number"
          className="numeric"
          step={step}
          min={min}
          max={max}
          value={value}
          onChange={(e) => {
            const next = Number(e.target.value);
            if (!Number.isNaN(next)) onChange(next);
          }}
        />
      )}
    </Field>
  );
}

/* -------------------------------------------------------------------- result */

function TestOutcome({ result }: { result: ConnectionTest }) {
  if (result.ok) {
    return (
      <Alert tone="info" title={result.model ? `${result.model} answered in ${result.latency_ms} ms` : "Endpoint reachable"}>
        {result.sample ? <p className="mt-1 italic">{result.sample}</p> : null}
        {result.models.length > 0 ? (
          <p className="mt-1 text-xs">
            {result.models.length} models offered: {result.models.slice(0, 6).join(", ")}
            {result.models.length > 6 ? ", ..." : ""}
          </p>
        ) : null}
      </Alert>
    );
  }

  return (
    <Alert
      tone="fail"
      title={
        result.unauthorized
          ? "The provider refused the bearer token"
          : result.reachable
            ? "The endpoint answered but the model call failed"
            : "The endpoint could not be reached"
      }
    >
      <p className="mt-1 break-words">{result.error}</p>
      {result.unauthorized ? (
        <p className="mt-2 text-xs">Re-enter the token on the provider above and test again.</p>
      ) : null}
    </Alert>
  );
}

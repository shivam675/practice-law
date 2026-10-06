import { useState } from "react";
import { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { Alert } from "../ui/Feedback";

export function SignIn() {
  const { signIn } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [organization, setOrganization] = useState("");
  const [needsOrg, setNeedsOrg] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await signIn(email, password, organization || undefined);
    } catch (err) {
      if (err instanceof ApiError && err.code === "organization_required") {
        setNeedsOrg(true);
        setError(err.message);
      } else if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError("Could not reach the server. Check your connection and try again.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="paper-grain grid min-h-[100dvh] lg:grid-cols-[1fr_1.1fr]">
      <div className="flex items-center justify-center px-6 py-12 md:px-12">
        <div className="w-full max-w-sm">
          <span className="font-serif text-2xl tracking-tight">MegaMoot</span>

          <h1 className="mt-10 text-3xl leading-tight">Sign in</h1>
          <p className="mt-2 text-sm text-ink-muted">
            Use the address your institution registered.
          </p>

          <form onSubmit={submit} className="mt-8 space-y-5" noValidate>
            {error ? <Alert tone="fail">{error}</Alert> : null}

            <Field label="Email address">
              {({ id, describedBy }) => (
                <TextInput
                  id={id}
                  aria-describedby={describedBy}
                  type="email"
                  name="email"
                  autoComplete="username"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  invalid={Boolean(error)}
                />
              )}
            </Field>

            <Field label="Password">
              {({ id, describedBy }) => (
                <TextInput
                  id={id}
                  aria-describedby={describedBy}
                  type="password"
                  name="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  invalid={Boolean(error)}
                />
              )}
            </Field>

            {needsOrg ? (
              <Field
                label="Institution"
                helper="Your address is registered at more than one institution."
              >
                {({ id, describedBy }) => (
                  <TextInput
                    id={id}
                    aria-describedby={describedBy}
                    name="organization"
                    placeholder="demo-law-school"
                    value={organization}
                    onChange={(e) => setOrganization(e.target.value)}
                  />
                )}
              </Field>
            ) : null}

            <Button type="submit" disabled={busy} className="w-full">
              {busy ? "Signing in" : "Sign in"}
            </Button>
          </form>

          <p className="mt-8 text-xs text-ink-faint">
            Lost your password? Your institution's administrator can reset it.
          </p>
        </div>
      </div>

      {/* The right panel states what the product is, in the product's own
          register. No marketing claims, no logo wall: this is a sign-in page
          for people who were told to be here. */}
      <aside className="hidden flex-col justify-between border-l border-rule bg-paper-sunken px-12 py-16 lg:flex">
        <div className="max-w-md">
          <h2 className="font-serif text-4xl leading-[1.15]">
            Argue before a bench that <em className="not-italic text-accent">listens</em>.
          </h2>
          <p className="mt-5 text-ink-muted prose-doc">
            Written memorials are read against the rubric and the record. Oral
            rounds are heard live, interrupted, and questioned on authority. Every
            score cites the passage it came from.
          </p>
        </div>

        <dl className="grid grid-cols-3 gap-8 border-t border-rule pt-8">
          <div>
            <dt className="text-xs text-ink-faint">Memorial</dt>
            <dd className="mt-1 text-sm">Structure checked, then read</dd>
          </div>
          <div>
            <dt className="text-xs text-ink-faint">Oral round</dt>
            <dd className="mt-1 text-sm">Live, with interruptions</dd>
          </div>
          <div>
            <dt className="text-xs text-ink-faint">Result</dt>
            <dd className="mt-1 text-sm">Evidence for every mark</dd>
          </div>
        </dl>
      </aside>
    </div>
  );
}

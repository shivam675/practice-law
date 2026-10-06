import type { ReactNode } from "react";
import type { Tone } from "../lib/format";

/**
 * Surface is the only container primitive.
 *
 * Elevation is used where a thing is genuinely a separate object (a
 * submission, a stage). Everything else groups with a rule or with space, so
 * the page does not become cards inside cards.
 */
export function Surface({
  children,
  className = "",
  as: Tag = "div",
}: {
  children: ReactNode;
  className?: string;
  as?: "div" | "section" | "article" | "li";
}) {
  return (
    <Tag className={`rounded-lg border border-rule bg-paper-raised ${className}`}>
      {children}
    </Tag>
  );
}

export function PageHeader({
  title,
  meta,
  actions,
}: {
  title: string;
  meta?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="mb-8 flex flex-col gap-4 border-b border-rule pb-6 md:flex-row md:items-end md:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl leading-tight md:text-3xl">{title}</h1>
        {meta ? <div className="mt-2 text-sm text-ink-muted">{meta}</div> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}

export function SectionTitle({
  children,
  trailing,
}: {
  children: ReactNode;
  trailing?: ReactNode;
}) {
  return (
    <div className="mb-4 flex items-baseline justify-between gap-4">
      <h2 className="text-lg">{children}</h2>
      {trailing}
    </div>
  );
}

const toneClasses: Record<Tone, string> = {
  neutral: "border-rule-strong bg-paper-sunken text-ink-muted",
  accent: "border-accent-rule bg-accent-soft text-accent",
  pass: "border-pass/25 bg-pass-soft text-pass",
  warn: "border-warn/25 bg-warn-soft text-warn",
  fail: "border-fail/25 bg-fail-soft text-fail",
};

export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: Tone;
}) {
  return (
    <span
      className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-xs font-medium ${toneClasses[tone]}`}
    >
      {children}
    </span>
  );
}

/** A labelled value. Used instead of stat cards, which would be card noise. */
export function DataPoint({
  label,
  value,
  numeric = false,
}: {
  label: string;
  value: ReactNode;
  numeric?: boolean;
}) {
  return (
    <div>
      <dt className="text-xs text-ink-faint">{label}</dt>
      <dd className={`mt-1 text-sm text-ink ${numeric ? "numeric" : ""}`}>{value}</dd>
    </div>
  );
}

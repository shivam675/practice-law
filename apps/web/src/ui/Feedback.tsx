import type { ReactNode } from "react";
import { CircleNotch, Info, Warning, WarningOctagon } from "@phosphor-icons/react";
import { ApiError } from "../lib/api";

/** Skeletons match the shape of what replaces them, so nothing jumps. */
export function SkeletonRows({ rows = 3 }: { rows?: number }) {
  return (
    <div className="space-y-3" aria-hidden>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="rounded-lg border border-rule bg-paper-raised p-5">
          <div className="h-4 w-2/5 animate-pulse rounded-sm bg-paper-sunken" />
          <div className="mt-3 h-3 w-1/4 animate-pulse rounded-sm bg-paper-sunken" />
        </div>
      ))}
    </div>
  );
}

export function Spinner({ label }: { label: string }) {
  return (
    <span className="inline-flex items-center gap-2 text-sm text-ink-muted">
      <CircleNotch size={16} className="animate-spin" aria-hidden />
      {label}
    </span>
  );
}

type AlertTone = "info" | "warn" | "fail";

const alertStyles: Record<AlertTone, { box: string; icon: ReactNode }> = {
  info: {
    box: "border-accent-rule bg-accent-soft text-accent",
    icon: <Info size={18} weight="bold" aria-hidden />,
  },
  warn: {
    box: "border-warn/30 bg-warn-soft text-warn",
    icon: <Warning size={18} weight="bold" aria-hidden />,
  },
  fail: {
    box: "border-fail/30 bg-fail-soft text-fail",
    icon: <WarningOctagon size={18} weight="bold" aria-hidden />,
  },
};

export function Alert({
  tone = "info",
  title,
  children,
}: {
  tone?: AlertTone;
  title?: string;
  children?: ReactNode;
}) {
  const style = alertStyles[tone];
  return (
    <div
      role={tone === "fail" ? "alert" : "status"}
      className={`flex gap-3 rounded-md border px-4 py-3 text-sm ${style.box}`}
    >
      <span className="mt-0.5 shrink-0">{style.icon}</span>
      <div className="min-w-0">
        {title ? <p className="font-medium">{title}</p> : null}
        {children ? <div className={title ? "mt-1" : ""}>{children}</div> : null}
      </div>
    </div>
  );
}

/** Turns an unknown thrown value into something a person can act on. */
export function ErrorState({ error, action }: { error: unknown; action?: ReactNode }) {
  const message =
    error instanceof ApiError
      ? error.message
      : error instanceof Error
        ? error.message
        : "Something went wrong.";

  const problems =
    error instanceof ApiError && Array.isArray(error.fields?.problems)
      ? (error.fields.problems as string[])
      : null;

  return (
    <Alert tone="fail" title={message}>
      {problems ? (
        <ul className="mt-1 list-disc space-y-1 pl-5">
          {problems.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      ) : null}
      {action ? <div className="mt-3">{action}</div> : null}
    </Alert>
  );
}

export function EmptyState({
  title,
  children,
  action,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="rounded-lg border border-dashed border-rule-strong bg-paper-raised px-6 py-12 text-center">
      <h3 className="text-lg">{title}</h3>
      {children ? (
        <p className="mx-auto mt-2 max-w-sm text-sm text-ink-muted">{children}</p>
      ) : null}
      {action ? <div className="mt-5 flex justify-center">{action}</div> : null}
    </div>
  );
}

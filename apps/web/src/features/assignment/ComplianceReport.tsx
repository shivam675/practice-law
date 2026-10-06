import { CheckCircle, MinusCircle, XCircle } from "@phosphor-icons/react";
import type { ComplianceFinding, ComplianceReport as Report } from "../../lib/api";
import { Badge } from "../../ui/Layout";

/**
 * The structure check, shown as findings rather than a score.
 *
 * A student who is told "structure: 6/10" learns nothing. A student who is
 * told "Summary of Arguments exceeds its word limit by 140 words" fixes it.
 * Rules that could not be evaluated say so and are visibly excluded from the
 * count, so nobody reads a missing page count as a failure.
 */
export function ComplianceReport({ report }: { report: Report }) {
  const failures = report.findings.filter((f) => f.status === "fail" && f.severity === "error");
  const warnings = report.findings.filter((f) => f.severity === "warning");
  const unchecked = report.findings.filter((f) => f.status === "not_checkable");

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
        <span className="flex items-center gap-2">
          <CheckCircle size={16} weight="fill" className="text-pass" aria-hidden />
          <span className="numeric">{report.passed}</span>
          <span className="text-ink-muted">passed</span>
        </span>
        <span className="flex items-center gap-2">
          <XCircle size={16} weight="fill" className={failures.length ? "text-fail" : "text-ink-faint"} aria-hidden />
          <span className="numeric">{report.failed}</span>
          <span className="text-ink-muted">failed</span>
        </span>
        {unchecked.length ? (
          <span className="flex items-center gap-2">
            <MinusCircle size={16} className="text-ink-faint" aria-hidden />
            <span className="numeric">{unchecked.length}</span>
            <span className="text-ink-muted">not checkable</span>
          </span>
        ) : null}
        <span className="text-ink-muted">
          <span className="numeric">{report.word_count.toLocaleString()}</span> words
          {report.page_count > 0 ? (
            <>
              {" · "}
              <span className="numeric">{report.page_count}</span> pages
            </>
          ) : null}
        </span>
      </div>

      {failures.length === 0 && warnings.length === 0 ? (
        <p className="text-sm text-pass">
          Every structural rule that could be checked passed.
        </p>
      ) : null}

      {failures.length ? <FindingList title="To fix" findings={failures} /> : null}
      {warnings.length ? (
        <FindingList
          title="For your teacher to confirm"
          findings={warnings}
          note="These do not affect your score on their own."
        />
      ) : null}

      <details className="group">
        <summary className="cursor-pointer text-sm text-ink-muted hover:text-ink">
          Sections detected ({report.sections.length})
        </summary>
        <dl className="mt-3 grid gap-x-8 gap-y-2 sm:grid-cols-2">
          {report.sections.map((section) => (
            <div key={section.key} className="flex items-baseline justify-between gap-3">
              <dt className="text-sm text-ink">{section.name}</dt>
              <dd className="numeric text-xs text-ink-muted">
                {section.word_count.toLocaleString()} w
              </dd>
            </div>
          ))}
        </dl>
      </details>
    </div>
  );
}

function FindingList({
  title,
  findings,
  note,
}: {
  title: string;
  findings: ComplianceFinding[];
  note?: string;
}) {
  return (
    <div>
      <div className="flex items-baseline gap-3">
        <h4 className="text-sm font-medium">{title}</h4>
        {note ? <span className="text-xs text-ink-faint">{note}</span> : null}
      </div>
      <ul className="mt-3 space-y-3">
        {findings.map((finding, i) => (
          <li key={`${finding.rule}-${finding.section ?? i}`} className="flex gap-3">
            {finding.severity === "warning" ? (
              <MinusCircle size={16} className="mt-0.5 shrink-0 text-warn" aria-hidden />
            ) : (
              <XCircle size={16} weight="fill" className="mt-0.5 shrink-0 text-fail" aria-hidden />
            )}
            <div className="min-w-0">
              <p className="text-sm text-ink">{finding.message}</p>
              {finding.expected || finding.actual ? (
                <p className="mt-1 text-xs text-ink-muted">
                  {finding.expected ? <>Expected {finding.expected}</> : null}
                  {finding.expected && finding.actual ? " · " : null}
                  {finding.actual ? <>found {finding.actual}</> : null}
                </p>
              ) : null}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ComplianceSummaryBadge({ report }: { report: Report }) {
  if (report.failed > 0) {
    return <Badge tone="fail">{report.failed} structural issue{report.failed === 1 ? "" : "s"}</Badge>;
  }
  if (report.warnings > 0) {
    return <Badge tone="warn">Check {report.warnings} note{report.warnings === 1 ? "" : "s"}</Badge>;
  }
  return <Badge tone="pass">Structure passed</Badge>;
}

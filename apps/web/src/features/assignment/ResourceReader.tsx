import { useState } from "react";
import { LockSimple } from "@phosphor-icons/react";
import type { Resource } from "../../lib/api";
import { Badge } from "../../ui/Layout";
import { EmptyState } from "../../ui/Feedback";

const kindLabel: Record<Resource["kind"], string> = {
  problem: "Problem",
  statute: "Statutes",
  authority: "Authorities",
  evidence: "Evidence",
  guidance: "Guidance",
  other: "Other",
};

const kindOrder: Resource["kind"][] = [
  "problem",
  "statute",
  "authority",
  "evidence",
  "guidance",
  "other",
];

/**
 * Reading pane for the case materials.
 *
 * A list of documents on the left, the text on the right, set at a
 * comfortable measure. This is the screen a student spends hours in, so it is
 * a reader, not a file browser: no cards, no thumbnails, no chrome competing
 * with the text.
 */
export function ResourceReader({ resources }: { resources: Resource[] }) {
  const firstAvailable = resources.find((r) => r.available);
  const [selectedId, setSelectedId] = useState<string | null>(firstAvailable?.id ?? null);

  if (resources.length === 0) {
    return (
      <EmptyState title="No materials yet">
        The problem and authorities appear here when your preparation stage opens.
      </EmptyState>
    );
  }

  const selected = resources.find((r) => r.id === selectedId) ?? firstAvailable ?? null;

  const grouped = kindOrder
    .map((kind) => ({ kind, items: resources.filter((r) => r.kind === kind) }))
    .filter((group) => group.items.length > 0);

  return (
    <div className="grid gap-8 lg:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]">
      <nav aria-label="Case materials" className="lg:border-r lg:border-rule lg:pr-6">
        {grouped.map((group) => (
          <div key={group.kind} className="mb-6 last:mb-0">
            <h3 className="mb-2 text-xs font-medium text-ink-faint">
              {kindLabel[group.kind]}
            </h3>
            <ul className="space-y-0.5">
              {group.items.map((resource) => {
                const active = selected?.id === resource.id;
                return (
                  <li key={resource.id}>
                    <button
                      type="button"
                      disabled={!resource.available}
                      onClick={() => setSelectedId(resource.id)}
                      className={`flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                        active
                          ? "bg-accent-soft font-medium text-accent"
                          : resource.available
                            ? "text-ink-muted hover:bg-paper-sunken hover:text-ink"
                            : "cursor-not-allowed text-ink-faint"
                      }`}
                    >
                      {!resource.available ? (
                        <LockSimple size={13} className="mt-1 shrink-0" aria-hidden />
                      ) : null}
                      <span className="min-w-0 flex-1">{resource.title}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </nav>

      <div className="min-w-0">
        {selected?.available && selected.body ? (
          <article>
            <div className="mb-5 flex flex-wrap items-center gap-3">
              <h2 className="text-xl leading-snug">{selected.title}</h2>
              <Badge>{kindLabel[selected.kind]}</Badge>
            </div>
            {/* Materials arrive as extracted plain text, so whitespace is
                preserved rather than parsed as markup. */}
            <pre className="prose-doc whitespace-pre-wrap font-serif text-[15px] leading-7 text-ink">
              {selected.body}
            </pre>
          </article>
        ) : (
          <EmptyState title="Not released yet">
            This material becomes readable when the stage that releases it opens.
          </EmptyState>
        )}
      </div>
    </div>
  );
}

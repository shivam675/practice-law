import { useId, type InputHTMLAttributes, type ReactNode } from "react";

/**
 * Label above, helper text present in markup, error below. Placeholders are
 * never used as labels.
 *
 * Contrast is checked against the paper surfaces these sit on: ink-muted
 * helper text and ink-faint placeholders both clear WCAG AA on paper-raised.
 */
export function Field({
  label,
  helper,
  error,
  children,
}: {
  label: string;
  helper?: ReactNode;
  error?: string | null;
  children: (props: { id: string; describedBy: string | undefined }) => ReactNode;
}) {
  const id = useId();
  const helperId = helper ? `${id}-helper` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [helperId, errorId].filter(Boolean).join(" ") || undefined;

  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="text-sm font-medium text-ink">
        {label}
      </label>
      {helper ? (
        <p id={helperId} className="text-xs text-ink-muted">
          {helper}
        </p>
      ) : null}
      {children({ id, describedBy })}
      {error ? (
        <p id={errorId} className="text-xs text-fail">
          {error}
        </p>
      ) : null}
    </div>
  );
}

export function TextInput({
  invalid = false,
  className = "",
  ...rest
}: InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean }) {
  return (
    <input
      aria-invalid={invalid || undefined}
      className={`h-10 w-full rounded-md border bg-paper-raised px-3 text-sm text-ink
        placeholder:text-ink-faint focus:outline-none focus-visible:outline-2
        focus-visible:outline-offset-2 focus-visible:outline-accent
        ${invalid ? "border-fail" : "border-rule-strong"} ${className}`}
      {...rest}
    />
  );
}

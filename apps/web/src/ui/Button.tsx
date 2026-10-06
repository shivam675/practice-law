import type { ButtonHTMLAttributes, ReactNode } from "react";
import { Link } from "react-router-dom";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

const base =
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md " +
  "font-medium transition-[background-color,color,border-color,transform] " +
  "duration-150 active:translate-y-px disabled:pointer-events-none disabled:opacity-50";

// Every pairing below is checked for WCAG AA contrast against the surface it
// sits on. Navy on paper and white on navy both clear 4.5:1.
const variants: Record<Variant, string> = {
  primary: "bg-accent text-white hover:bg-accent-hover",
  secondary:
    "border border-rule-strong bg-paper-raised text-ink hover:bg-paper-sunken",
  ghost: "text-ink-muted hover:bg-paper-sunken hover:text-ink",
  danger: "border border-fail/30 bg-fail-soft text-fail hover:bg-fail/10",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-[13px]",
  md: "h-10 px-4 text-sm",
};

type Common = {
  variant?: Variant;
  size?: Size;
  children: ReactNode;
  className?: string;
};

export function Button({
  variant = "primary",
  size = "md",
  className = "",
  children,
  ...rest
}: Common & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      className={`${base} ${variants[variant]} ${sizes[size]} ${className}`}
      {...rest}
    >
      {children}
    </button>
  );
}

export function ButtonLink({
  to,
  variant = "primary",
  size = "md",
  className = "",
  children,
}: Common & { to: string }) {
  return (
    <Link to={to} className={`${base} ${variants[variant]} ${sizes[size]} ${className}`}>
      {children}
    </Link>
  );
}

import { NavLink, Outlet } from "react-router-dom";
import {
  Books,
  Gavel,
  House,
  Robot,
  SignOut,
  SlidersHorizontal,
  Stack,
  UsersThree,
} from "@phosphor-icons/react";
import { useAuth, useIsStaff } from "../lib/auth";
import { Button } from "../ui/Button";

type NavItem = { to: string; label: string; icon: typeof Gavel; permission?: string };

const studentNav: NavItem[] = [{ to: "/work", label: "My work", icon: Books }];

const staffNav: NavItem[] = [
  { to: "/", label: "Overview", icon: House },
  { to: "/assessments", label: "Assessments", icon: Gavel, permission: "assessment.view" },
  { to: "/teams", label: "Teams", icon: UsersThree, permission: "team.view" },
  { to: "/admin/users", label: "People", icon: UsersThree, permission: "user.view" },
  { to: "/templates", label: "Templates", icon: Stack, permission: "template.view" },
  { to: "/rubrics", label: "Rubrics", icon: Stack, permission: "rubric.view" },
  { to: "/admin/actors", label: "AI actors", icon: Robot, permission: "ai_profile.view" },
  { to: "/admin/organization", label: "Organisation", icon: House, permission: "organization.edit" },
  { to: "/admin/organizations", label: "Organisations", icon: House, permission: "platform.organization.manage" },
  // Platform operators only. Everyone else never sees this exists.
  {
    to: "/admin/models",
    label: "Models",
    icon: SlidersHorizontal,
    permission: "platform.model.configure",
  },
];

export function AppShell() {
  const { user, signOut, can } = useAuth();
  const staff = useIsStaff();

  const items = (staff ? staffNav : studentNav).filter(
    (item) => !item.permission || can(item.permission),
  );

  return (
    <div className="paper-grain min-h-[100dvh]">
      <header className="sticky top-0 z-20 h-16 border-b border-rule bg-paper/90 backdrop-blur-sm">
        <div className="mx-auto flex h-full max-w-[1400px] items-center justify-between gap-6 px-4 md:px-8">
          <div className="flex items-baseline gap-3">
            <span className="font-serif text-xl tracking-tight">MegaMoot</span>
            <span className="hidden text-xs text-ink-faint sm:inline">
              {user?.organization ?? "Assessment platform"}
            </span>
          </div>

          <div className="flex items-center gap-4">
            <span className="hidden text-sm text-ink-muted md:inline">{user?.full_name}</span>
            <Button variant="ghost" size="sm" onClick={() => void signOut()}>
              <SignOut size={16} aria-hidden />
              Sign out
            </Button>
          </div>
        </div>
      </header>

      <div className="mx-auto flex max-w-[1400px] gap-8 px-4 md:px-8">
        <nav
          aria-label="Sections"
          className="hidden w-52 shrink-0 py-8 md:block"
        >
          <ul className="space-y-1">
            {items.map(({ to, label, icon: Icon }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={to === "/"}
                  className={({ isActive }) =>
                    `flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors ${
                      isActive
                        ? "bg-accent-soft font-medium text-accent"
                        : "text-ink-muted hover:bg-paper-sunken hover:text-ink"
                    }`
                  }
                >
                  <Icon size={18} aria-hidden />
                  {label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        {/* On narrow screens the sidebar becomes a horizontal strip rather
            than a drawer: there are only a handful of destinations. */}
        <main className="min-w-0 flex-1 py-6 md:py-8">
          <ul className="mb-6 flex gap-2 overflow-x-auto md:hidden">
            {items.map(({ to, label }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={to === "/"}
                  className={({ isActive }) =>
                    `inline-flex whitespace-nowrap rounded-md border px-3 py-1.5 text-sm ${
                      isActive
                        ? "border-accent-rule bg-accent-soft text-accent"
                        : "border-rule text-ink-muted"
                    }`
                  }
                >
                  {label}
                </NavLink>
              </li>
            ))}
          </ul>
          <Outlet />
        </main>
      </div>
    </div>
  );
}

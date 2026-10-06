import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  onSessionExpired,
  refreshSession,
  rememberSession,
  setAccessToken,
  type Me,
  type SessionResponse,
  type User,
} from "./api";

type AuthState = {
  status: "loading" | "signed-in" | "signed-out";
  user: User | null;
  permissions: Set<string>;
  signIn: (email: string, password: string, organization?: string) => Promise<void>;
  signOut: () => Promise<void>;
  can: (permission: string) => boolean;
  canAny: (...permissions: string[]) => boolean;
};

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthState["status"]>("loading");
  const [user, setUser] = useState<User | null>(null);
  const [permissions, setPermissions] = useState<Set<string>>(new Set());

  const clear = useCallback(() => {
    setAccessToken(null);
    setUser(null);
    setPermissions(new Set());
    setStatus("signed-out");
  }, []);

  const load = useCallback(async () => {
    const me = await api.get<Me>("/me");
    setUser(me.user);
    setPermissions(new Set(me.permissions));
    setStatus("signed-in");
  }, []);

  // On first load the access token is gone (it only ever lived in memory), but
  // the refresh cookie may still be valid, so a reload keeps the session.
  useEffect(() => {
    let cancelled = false;
    onSessionExpired(() => {
      if (!cancelled) clear();
    });

    // Goes through the shared single-flight refresh: a second call here would
    // replay the rotated token and the server would revoke the session, which
    // is exactly what it should do to a replay.
    (async () => {
      const restored = await refreshSession();
      if (cancelled) return;
      if (!restored) {
        clear();
        return;
      }
      try {
        await load();
      } catch {
        if (!cancelled) clear();
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [clear, load]);

  const signIn = useCallback(
    async (email: string, password: string, organization?: string) => {
      const session = await api.post<SessionResponse>("/auth/login", {
        email,
        password,
        ...(organization ? { organization } : {}),
      });
      rememberSession(session);
      await load();
    },
    [load],
  );

  const signOut = useCallback(async () => {
    try {
      await api.post("/auth/logout");
    } finally {
      clear();
    }
  }, [clear]);

  const value = useMemo<AuthState>(
    () => ({
      status,
      user,
      permissions,
      signIn,
      signOut,
      can: (p) => permissions.has(p),
      canAny: (...ps) => ps.some((p) => permissions.has(p)),
    }),
    [status, user, permissions, signIn, signOut],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider");
  return ctx;
}

/** Staff see the organisation-wide views; everyone else sees their own work. */
export function useIsStaff(): boolean {
  const { canAny } = useAuth();
  return canAny("assessment.view", "assessment.create", "assessment.grade");
}

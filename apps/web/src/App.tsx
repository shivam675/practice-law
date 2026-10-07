import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "./lib/api";
import { AuthProvider, useAuth, useIsStaff } from "./lib/auth";
import { AppShell } from "./app/AppShell";
import { SignIn } from "./routes/SignIn";
import { MyWork } from "./routes/MyWork";
import { AssignmentDetail } from "./routes/AssignmentDetail";
import { Assessments } from "./routes/Assessments";
import { AssessmentDetail } from "./routes/AssessmentDetail";
import { Teams } from "./routes/Teams";
import { Templates } from "./routes/Templates";
import { SessionRoom } from "./routes/SessionRoom";
import { ModelSettings } from "./routes/ModelSettings";
import { AiProfiles } from "./routes/AiProfiles";
import { Dashboard } from "./routes/Dashboard";
import { People } from "./routes/People";
import { Spinner } from "./ui/Feedback";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        // Retrying a 4xx just repeats the same refusal. Only transient
        // failures are worth a second attempt.
        if (error instanceof ApiError && error.status < 500) return false;
        return failureCount < 2;
      },
    },
  },
});

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <BrowserRouter>
          <Router />
        </BrowserRouter>
      </AuthProvider>
    </QueryClientProvider>
  );
}

function Router() {
  const { status } = useAuth();

  if (status === "loading") {
    return (
      <div className="flex min-h-[100dvh] items-center justify-center">
        <Spinner label="Restoring your session" />
      </div>
    );
  }

  if (status === "signed-out") {
    return (
      <Routes>
        <Route path="*" element={<SignIn />} />
      </Routes>
    );
  }

  return (
    <Routes>
      {/* The session room is full-bleed: the shell's navigation would be
          competing for attention during a graded round. */}
      <Route path="/session/:assignmentId/:stageId" element={<SessionRoom />} />

      <Route element={<AppShell />}>
        <Route path="/" element={<Home />} />
        <Route path="/work" element={<MyWork />} />
        <Route path="/work/:assignmentId" element={<AssignmentDetail />} />
        <Route path="/assessments" element={<Assessments />} />
        <Route path="/assessments/:assessmentId" element={<AssessmentDetail />} />
        <Route path="/teams" element={<Teams />} />
        <Route path="/templates" element={<Templates />} />
        <Route path="/admin/actors" element={<AiProfiles />} />
        <Route path="/admin/models" element={<ModelSettings />} />
        <Route path="/admin/users" element={<People />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

function Home() {
  const staff = useIsStaff();
  return staff ? <Dashboard /> : <Navigate to="/work" replace />;
}

function NotFound() {
  const staff = useIsStaff();
  return (
    <div className="py-16 text-center">
      <h1 className="text-2xl">That page does not exist</h1>
      <p className="mt-2 text-sm text-ink-muted">
        The link may be out of date, or the record may have been removed.
      </p>
      <a
        href={staff ? "/assessments" : "/work"}
        className="mt-6 inline-block text-sm text-accent underline underline-offset-2"
      >
        Back to {staff ? "assessments" : "my work"}
      </a>
    </div>
  );
}

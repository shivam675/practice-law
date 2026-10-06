import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "@phosphor-icons/react";
import { api, type Team } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button } from "../ui/Button";
import { Field, TextInput } from "../ui/Field";
import { Badge, PageHeader, Surface } from "../ui/Layout";
import { EmptyState, ErrorState, SkeletonRows } from "../ui/Feedback";

type DirectoryUser = { id: string; full_name: string; email: string; roles: string[] };

export function Teams() {
  const { can } = useAuth();
  const [creating, setCreating] = useState(false);

  const teams = useQuery({
    queryKey: ["teams"],
    queryFn: () => api.get<{ teams: Team[] }>("/teams"),
  });

  return (
    <>
      <PageHeader
        title="Teams"
        meta="A solo candidate is a team of one. Speaking order decides who holds the floor in each live stage."
        actions={
          can("team.create") ? (
            <Button onClick={() => setCreating((v) => !v)} variant={creating ? "ghost" : "primary"}>
              {creating ? (
                "Cancel"
              ) : (
                <>
                  <Plus size={16} aria-hidden />
                  New team
                </>
              )}
            </Button>
          ) : undefined
        }
      />

      {creating ? <CreateTeam onDone={() => setCreating(false)} /> : null}

      {teams.isPending ? <SkeletonRows rows={2} /> : null}
      {teams.error ? <ErrorState error={teams.error} /> : null}

      {teams.data && teams.data.teams.length === 0 && !creating ? (
        <EmptyState
          title="No teams yet"
          action={can("team.create") ? <Button onClick={() => setCreating(true)}>Create a team</Button> : undefined}
        >
          Teams are assigned to assessments and are the unit everything is
          graded and reported against.
        </EmptyState>
      ) : null}

      <ul className="grid gap-3 md:grid-cols-2">
        {teams.data?.teams.map((team) => (
          <Surface as="li" key={team.id} className="px-5 py-4">
            <h2 className="text-base">{team.name}</h2>
            <ul className="mt-3 space-y-2">
              {team.members.map((member) => (
                <li key={member.user_id} className="flex items-center justify-between gap-3 text-sm">
                  <span className="min-w-0">
                    <span className="block truncate text-ink">{member.full_name}</span>
                    <span className="block truncate text-xs text-ink-faint">{member.email}</span>
                  </span>
                  <Badge tone={member.role === "speaker" ? "accent" : "neutral"}>
                    {member.role === "speaker"
                      ? `Speaker ${member.speaking_order ?? ""}`.trim()
                      : "Researcher"}
                  </Badge>
                </li>
              ))}
            </ul>
          </Surface>
        ))}
      </ul>
    </>
  );
}

type Draft = { userId: string; role: "speaker" | "researcher" };

function CreateTeam({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [members, setMembers] = useState<Draft[]>([{ userId: "", role: "speaker" }]);

  const directory = useQuery({
    queryKey: ["users"],
    queryFn: () => api.get<{ users: DirectoryUser[] }>("/users?limit=200"),
  });

  const students =
    directory.data?.users.filter((u) => u.roles.includes("student")) ??
    directory.data?.users ??
    [];

  const create = useMutation({
    mutationFn: () => {
      let order = 0;
      return api.post<Team>("/teams", {
        name,
        members: members
          .filter((m) => m.userId)
          .map((m) => ({
            user_id: m.userId,
            role: m.role,
            speaking_order: m.role === "speaker" ? ++order : null,
          })),
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["teams"] });
      onDone();
    },
  });

  const chosen = new Set(members.map((m) => m.userId).filter(Boolean));

  return (
    <Surface className="mb-8 px-5 py-5">
      <h2 className="text-lg">New team</h2>
      <p className="mt-1 text-sm text-ink-muted">
        Speaking order is assigned top to bottom from the speakers you list.
      </p>

      <form
        className="mt-5 space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div className="md:max-w-sm">
          <Field label="Team name">
            {({ id }) => (
              <TextInput
                id={id}
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Team 14"
              />
            )}
          </Field>
        </div>

        <fieldset>
          <legend className="text-sm font-medium">Members</legend>
          <ul className="mt-3 space-y-3">
            {members.map((member, index) => (
              <li key={index} className="flex items-center gap-3">
                <select
                  aria-label={`Member ${index + 1}`}
                  value={member.userId}
                  onChange={(e) =>
                    setMembers((prev) =>
                      prev.map((m, i) => (i === index ? { ...m, userId: e.target.value } : m)),
                    )
                  }
                  className="h-10 min-w-0 flex-1 rounded-md border border-rule-strong bg-paper-raised px-3 text-sm text-ink"
                >
                  <option value="">Choose a student</option>
                  {students
                    .filter((u) => u.id === member.userId || !chosen.has(u.id))
                    .map((u) => (
                      <option key={u.id} value={u.id}>
                        {u.full_name} ({u.email})
                      </option>
                    ))}
                </select>

                <select
                  aria-label={`Role for member ${index + 1}`}
                  value={member.role}
                  onChange={(e) =>
                    setMembers((prev) =>
                      prev.map((m, i) =>
                        i === index ? { ...m, role: e.target.value as Draft["role"] } : m,
                      ),
                    )
                  }
                  className="h-10 w-36 rounded-md border border-rule-strong bg-paper-raised px-3 text-sm text-ink"
                >
                  <option value="speaker">Speaker</option>
                  <option value="researcher">Researcher</option>
                </select>

                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-label={`Remove member ${index + 1}`}
                  disabled={members.length === 1}
                  onClick={() => setMembers((prev) => prev.filter((_, i) => i !== index))}
                >
                  <X size={16} aria-hidden />
                </Button>
              </li>
            ))}
          </ul>

          <Button
            type="button"
            variant="secondary"
            size="sm"
            className="mt-3"
            onClick={() => setMembers((prev) => [...prev, { userId: "", role: "speaker" }])}
          >
            <Plus size={14} aria-hidden />
            Add member
          </Button>
        </fieldset>

        <div className="flex gap-3">
          <Button type="submit" disabled={create.isPending || !members.some((m) => m.userId)}>
            {create.isPending ? "Creating" : "Create team"}
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        </div>

        {create.error ? <ErrorState error={create.error} /> : null}
      </form>
    </Surface>
  );
}

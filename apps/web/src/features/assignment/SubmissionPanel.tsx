import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { FileArrowUp, FileText, Lock } from "@phosphor-icons/react";
import { api, ApiError, type AssignmentStage, type Submission } from "../../lib/api";
import { formatBytes, formatDateTime, formatDeadline } from "../../lib/format";
import { Button } from "../../ui/Button";
import { Alert, ErrorState, Spinner } from "../../ui/Feedback";
import { Badge } from "../../ui/Layout";
import { ComplianceReport, ComplianceSummaryBadge } from "./ComplianceReport";

export function SubmissionPanel({
  assignmentId,
  stage,
  submissions,
}: {
  assignmentId: string;
  stage: AssignmentStage;
  submissions: Submission[];
}) {
  const mine = submissions
    .filter((s) => s.stage_id === stage.stage_id)
    .sort((a, b) => b.version - a.version);
  const latest = mine[0];
  const locked = Boolean(latest?.locked_at);
  const open = stage.status === "active" || stage.status === "grace";

  return (
    <div className="space-y-6">
      {locked ? (
        <Alert tone="info" title="Submitted and locked">
          Submitted {formatDateTime(latest?.submitted_at)}. This stage accepts no
          further uploads. Speak to your teacher if something is wrong with it.
        </Alert>
      ) : open ? (
        <UploadForm assignmentId={assignmentId} stage={stage} />
      ) : (
        <Alert tone={stage.status === "pending" ? "info" : "warn"}>
          {stage.status === "pending"
            ? `This stage opens ${formatDeadline(stage.opens_at)}.`
            : "This stage is closed. No submission was recorded."}
        </Alert>
      )}

      {mine.length ? (
        <div>
          <h3 className="text-sm font-medium">
            {mine.length === 1 ? "Your submission" : `Your submissions (${mine.length})`}
          </h3>
          <ul className="mt-3 space-y-4">
            {mine.map((submission) => (
              <SubmissionRow key={submission.id} submission={submission} />
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}

function SubmissionRow({ submission }: { submission: Submission }) {
  return (
    <li className="rounded-lg border border-rule bg-paper-raised">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-rule px-5 py-4">
        <div className="flex min-w-0 gap-3">
          <FileText size={20} className="mt-0.5 shrink-0 text-ink-faint" aria-hidden />
          <div className="min-w-0">
            <p className="truncate text-sm text-ink">{submission.filename}</p>
            <p className="mt-1 text-xs text-ink-muted">
              Version <span className="numeric">{submission.version}</span>
              {" · "}
              <span className="numeric">{formatBytes(submission.byte_size)}</span>
              {submission.pages ? (
                <>
                  {" · "}
                  <span className="numeric">{submission.pages}</span> pages
                </>
              ) : null}
              {" · "}
              {formatDateTime(submission.submitted_at)}
            </p>
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {submission.is_late ? <Badge tone="warn">Late</Badge> : null}
          {submission.locked_at ? (
            <Badge tone="neutral">
              <Lock size={11} className="mr-1" aria-hidden />
              Locked
            </Badge>
          ) : null}
          {submission.compliance ? (
            <ComplianceSummaryBadge report={submission.compliance} />
          ) : null}
        </div>
      </div>

      {submission.compliance ? (
        <div className="px-5 py-5">
          <ComplianceReport report={submission.compliance} />
        </div>
      ) : null}
    </li>
  );
}

const acceptAttribute = ".pdf,.docx,.txt,application/pdf,text/plain";

function UploadForm({
  assignmentId,
  stage,
}: {
  assignmentId: string;
  stage: AssignmentStage;
}) {
  const queryClient = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [dragging, setDragging] = useState(false);

  const upload = useMutation({
    mutationFn: async (chosen: File) => {
      const form = new FormData();
      form.append("file", chosen);
      return api.upload<Submission>(
        `/assignments/${assignmentId}/stages/${stage.stage_id}/submissions`,
        form,
      );
    },
    onSuccess: () => {
      setFile(null);
      if (inputRef.current) inputRef.current.value = "";
      void queryClient.invalidateQueries({ queryKey: ["submissions", assignmentId] });
      void queryClient.invalidateQueries({ queryKey: ["assignment", assignmentId] });
    },
  });

  const late = stage.status === "grace";

  return (
    <div className="space-y-4">
      {late ? (
        <Alert tone="warn" title="The deadline has passed">
          Uploads are still accepted during the grace period and will be marked
          late. It closes {formatDeadline(stage.grace_until)}.
        </Alert>
      ) : null}

      <div
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          const dropped = e.dataTransfer.files[0];
          if (dropped) setFile(dropped);
        }}
        className={`rounded-lg border-2 border-dashed px-6 py-10 text-center transition-colors ${
          dragging ? "border-accent bg-accent-soft" : "border-rule-strong bg-paper-raised"
        }`}
      >
        <FileArrowUp size={28} className="mx-auto text-ink-faint" aria-hidden />

        <p className="mt-3 text-sm text-ink">
          {file ? file.name : "Drop your memorial here, or choose a file"}
        </p>
        <p className="mt-1 text-xs text-ink-muted">
          PDF or Word, up to 25 MB. A PDF lets the page limit be checked.
        </p>

        <input
          ref={inputRef}
          type="file"
          accept={acceptAttribute}
          className="sr-only"
          id={`upload-${stage.stage_id}`}
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />

        <div className="mt-5 flex flex-wrap items-center justify-center gap-3">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => inputRef.current?.click()}
          >
            Choose file
          </Button>

          <Button
            type="button"
            size="sm"
            disabled={!file || upload.isPending}
            onClick={() => file && upload.mutate(file)}
          >
            {upload.isPending ? "Uploading" : late ? "Submit late" : "Submit memorial"}
          </Button>
        </div>

        {upload.isPending ? (
          <p className="mt-4">
            <Spinner label="Reading the document and checking its structure" />
          </p>
        ) : null}
      </div>

      {upload.error ? (
        <ErrorState error={upload.error} action={<UploadHint error={upload.error} />} />
      ) : null}
    </div>
  );
}

/** Turns the API's error codes into the next thing to actually do. */
function UploadHint({ error }: { error: unknown }) {
  if (!(error instanceof ApiError)) return null;

  switch (error.code) {
    case "unsupported_format":
      return <span className="text-sm">Export your memorial as a PDF and try again.</span>;
    case "unreadable_document":
      return (
        <span className="text-sm">
          Scanned or image-only PDFs cannot be read. Export from your word
          processor rather than scanning a printout.
        </span>
      );
    case "file_too_large":
    case "upload_too_large":
      return (
        <span className="text-sm">
          Compress the images in your document, or export without embedded fonts.
        </span>
      );
    case "conflict":
      return <span className="text-sm">Reload the page to see the recorded submission.</span>;
    default:
      return null;
  }
}

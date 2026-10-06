import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  Headphones,
  Microphone,
  PauseCircle,
  Scales,
  SpeakerHigh,
  Stop,
} from "@phosphor-icons/react";
import { formatDuration, humanise } from "../lib/format";
import { Button } from "../ui/Button";
import { Alert } from "../ui/Feedback";
import { Badge, Surface } from "../ui/Layout";

/**
 * Oral round room.
 *
 * This is a layout shell. Nothing here is connected: audio, transcription and
 * the bench arrive with the media plane. It exists now so the composition can
 * be argued about before it is expensive to change.
 *
 * Three decisions worth arguing with:
 *
 *   1. There is no self-view. A student watching their own face is a student
 *      not arguing. A level meter confirms they are being heard, which is the
 *      only thing a self-view was doing.
 *   2. The question stays on screen until it is answered. Under pressure
 *      people forget the question ten seconds in, and this single detail
 *      matters more than the avatar.
 *   3. The clock is numerals, not a draining bar, and stays quiet until the
 *      last minute. A progress bar that empties makes people rush.
 */
export function SessionRoom() {
  const { assignmentId = "", stageId = "" } = useParams();
  const [ready, setReady] = useState(false);

  return (
    <div className="paper-grain min-h-[100dvh] bg-paper">
      <header className="flex h-16 items-center justify-between gap-6 border-b border-rule px-4 md:px-8">
        <div className="flex min-w-0 items-center gap-4">
          <span className="font-serif text-lg tracking-tight">MegaMoot</span>
          <span className="hidden text-sm text-ink-muted sm:inline">
            {humanise(stageId)}
          </span>
        </div>

        <div className="flex items-center gap-4">
          <span className="numeric text-xl tabular-nums">{formatDuration(12 * 60)}</span>
          <Link
            to={`/work/${assignmentId}`}
            className="text-sm text-ink-muted underline-offset-2 hover:text-ink hover:underline"
          >
            Leave
          </Link>
        </div>
      </header>

      <main className="mx-auto max-w-[1400px] px-4 py-8 md:px-8">
        <Alert tone="warn" title="Layout preview">
          The bench, your microphone and the transcript are not connected yet.
          This screen exists so the arrangement can be settled first.
        </Alert>

        {!ready ? (
          <DeviceCheck onReady={() => setReady(true)} />
        ) : (
          <div className="mt-8 grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
            <Bench />
            <QuestionPane />
          </div>
        )}
      </main>
    </div>
  );
}

function DeviceCheck({ onReady }: { onReady: () => void }) {
  const [headphones, setHeadphones] = useState(false);

  return (
    <div className="mt-8 grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div>
        <h1 className="text-3xl leading-tight">Before you go in</h1>
        <p className="mt-3 max-w-prose text-ink-muted">
          Two checks. Both take a few seconds and both prevent the kind of
          failure that ruins a round.
        </p>

        <ol className="mt-8 space-y-6">
          <li>
            <div className="flex items-start gap-4">
              <Headphones size={22} className="mt-1 shrink-0 text-accent" aria-hidden />
              <div>
                <h2 className="text-base">Headphones are required</h2>
                <p className="mt-1 max-w-prose text-sm text-ink-muted">
                  Without them your microphone picks up the bench, the bench
                  transcribes itself, and it starts answering its own questions.
                </p>
                <label className="mt-3 inline-flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={headphones}
                    onChange={(e) => setHeadphones(e.target.checked)}
                    className="size-4 rounded-sm border-rule-strong accent-[var(--color-accent)]"
                  />
                  I am wearing headphones
                </label>
              </div>
            </div>
          </li>

          <li>
            <div className="flex items-start gap-4">
              <Microphone size={22} className="mt-1 shrink-0 text-accent" aria-hidden />
              <div>
                <h2 className="text-base">Microphone level</h2>
                <p className="mt-1 max-w-prose text-sm text-ink-muted">
                  Say your appearance out loud: "May it please the Court, counsel
                  for the applicant."
                </p>
                <div className="mt-3">
                  <LevelMeter />
                </div>
              </div>
            </div>
          </li>
        </ol>

        <Button className="mt-8" disabled={!headphones} onClick={onReady}>
          Enter the courtroom
        </Button>
      </div>

      <Surface className="px-6 py-6">
        <h2 className="text-base">What the bench will do</h2>
        <ul className="mt-4 space-y-3 text-sm text-ink-muted">
          <li>Interrupt when a proposition is asserted without authority.</li>
          <li>Press you when an answer avoids the question that was asked.</li>
          <li>Hold you to the record and to what you have already conceded.</li>
          <li>Stop speaking the moment you start. Talk over it if you need to.</li>
        </ul>

        <h2 className="mt-8 text-base">What it will not do</h2>
        <ul className="mt-4 space-y-3 text-sm text-ink-muted">
          <li>Tell you how you are doing, or give you its view of the merits.</li>
          <li>Decide your mark. A teacher signs off every result.</li>
        </ul>
      </Surface>
    </div>
  );
}

/** Static bars, placed so the live meter has a shape to land in. */
function LevelMeter() {
  const bars = [3, 7, 12, 18, 26, 34, 28, 19, 13, 8, 5, 9, 16, 23, 17, 10, 6, 4];
  return (
    <div className="flex h-10 items-end gap-[3px]" aria-hidden>
      {bars.map((height, i) => (
        <span
          key={i}
          className="w-[5px] rounded-sm bg-accent/35"
          style={{ height: `${height + 6}px` }}
        />
      ))}
    </div>
  );
}

function Bench() {
  return (
    <div>
      <Surface className="relative overflow-hidden">
        {/* The bench portrait. A single illustrated judge with a small set of
            states reads as more serious than mediocre 3D. The renderer is
            swapped in here without touching anything around it. */}
        <div className="flex aspect-[4/3] items-center justify-center bg-paper-sunken">
          <div className="text-center">
            <span className="mx-auto flex size-20 items-center justify-center rounded-full border border-rule-strong bg-paper-raised">
              <Scales size={34} className="text-accent" aria-hidden />
            </span>
            <p className="mt-4 font-serif text-lg">Presiding Judge</p>
            <p className="mt-1 text-xs text-ink-faint">Avatar renderer not connected</p>
          </div>
        </div>

        <div className="absolute left-4 top-4">
          <Badge tone="accent">
            <SpeakerHigh size={11} className="mr-1" aria-hidden />
            Speaking
          </Badge>
        </div>
      </Surface>

      <div className="mt-5 flex items-center justify-between gap-4">
        <div>
          <p className="text-xs text-ink-faint">Your microphone</p>
          <div className="mt-2">
            <LevelMeter />
          </div>
        </div>

        <div className="flex gap-2">
          <Button variant="secondary" size="sm" disabled>
            <PauseCircle size={16} aria-hidden />
            Pause
          </Button>
          <Button variant="danger" size="sm" disabled>
            <Stop size={16} aria-hidden />
            End
          </Button>
        </div>
      </div>
    </div>
  );
}

function QuestionPane() {
  return (
    <div className="flex flex-col gap-6">
      {/* The question is the largest text in the room and does not move until
          it is answered. */}
      <Surface className="border-accent-rule bg-accent-soft px-6 py-6">
        <p className="text-xs font-medium text-accent">The bench asks</p>
        <p className="mt-3 font-serif text-2xl leading-snug text-ink">
          Counsel, you say the suspension was proportionate. What authority
          supports reading necessity that broadly?
        </p>
        <p className="mt-4 text-xs text-ink-muted">
          Asked at <span className="numeric">04:12</span>
        </p>
      </Surface>

      <div className="min-h-0 flex-1">
        <p className="mb-3 text-xs text-ink-faint">Transcript</p>
        <div className="space-y-4 text-sm leading-relaxed">
          <p className="text-ink-faint">
            May it please the Court. Counsel appears for the applicant and will
            address the first and second issues.
          </p>
          <p className="text-ink-muted">
            The suspension order was renewed four times on an identical recital,
            which on its face cannot satisfy the necessity limb.
          </p>
          <p className="text-ink">
            In Anuradha Bhasin this Court held an indefinite suspension
            impermissible and required periodic review.
            <span className="ml-1 inline-block h-4 w-[2px] translate-y-0.5 bg-accent" aria-hidden />
          </p>
        </div>
      </div>

      {/* Materials are drawers, closed by default. An open sidebar invites
          reading instead of arguing. */}
      <div className="flex flex-wrap gap-2 border-t border-rule pt-5">
        {["Authorities", "Statement of facts", "My notes"].map((label) => (
          <Button key={label} variant="secondary" size="sm" disabled>
            {label}
          </Button>
        ))}
      </div>
    </div>
  );
}

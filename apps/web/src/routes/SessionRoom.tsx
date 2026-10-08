import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Scales } from "@phosphor-icons/react";
import { api, type Assignment } from "../lib/api";
import { useAuth } from "../lib/auth";
import { formatDuration, humanise } from "../lib/format";
import { Button } from "../ui/Button";
import { Alert, ErrorState } from "../ui/Feedback";
import { Badge, Surface } from "../ui/Layout";

type LiveSession = { id: string; status: string; ends_at: string; transcript: { seq: number; speaker: string; text: string }[] };

export function SessionRoom() {
  const { can } = useAuth();
  const observer = can("session.observe");
  const { assignmentId = "", stageId = "" } = useParams();
  const [sessionId, setSessionId] = useState("");
  const [consent, setConsent] = useState(false);
  const [checked, setChecked] = useState(false);
  const [state, setState] = useState("Not connected");
  const [error, setError] = useState<unknown>(null);
  const [partial, setPartial] = useState("");
  const [question, setQuestion] = useState("");
  const [level, setLevel] = useState(0);
  const [now, setNow] = useState(Date.now());
  const [busy, setBusy] = useState(false);
  const stream = useRef<MediaStream | null>(null);
  const context = useRef<AudioContext | null>(null);
  const socket = useRef<WebSocket | null>(null);
  const player = useRef<HTMLAudioElement | null>(null);
  const captureNode = useRef<AudioWorkletNode | null>(null);
  const playbackQueue = useRef<Blob[]>([]);
  const playbackUrl = useRef("");
  const finishing = useRef(false);
  const assignment = useQuery({queryKey: ["assignment", assignmentId], queryFn: () => api.get<Assignment>(`/assignments/${assignmentId}`), refetchInterval: observer ? 5000 : false});
  const session = useQuery({ queryKey: ["session", sessionId], queryFn: () => api.get<LiveSession>(`/sessions/${sessionId}`), enabled: !!sessionId, refetchInterval: 2000 });

  function stopPlayback() {
    playbackQueue.current = [];
    player.current?.pause(); player.current = null;
    if (playbackUrl.current) URL.revokeObjectURL(playbackUrl.current);
    playbackUrl.current = "";
  }
  function playNext() {
    if (player.current) return;
    const next = playbackQueue.current.shift();
    if (!next) { setState("Listening"); return; }
    playbackUrl.current = URL.createObjectURL(next);
    const sound = new Audio(playbackUrl.current); player.current = sound;
    setState("Examiner speaking");
    const done = () => {
      if (player.current !== sound) return;
      player.current = null; URL.revokeObjectURL(playbackUrl.current); playbackUrl.current = ""; playNext();
    };
    sound.onended = done;
    void sound.play().catch(() => { done(); setError(new Error("Audio playback was blocked. Read the question on screen.")); });
  }
  function disconnect() {
    const ws = socket.current; socket.current = null; ws?.close();
    stream.current?.getTracks().forEach((track) => track.stop()); stream.current = null;
    void context.current?.close(); context.current = null;
    stopPlayback();
  }
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 1000); return () => { clearInterval(timer); disconnect(); }; }, []);
  useEffect(() => { if (session.data && session.data.status !== "running") { disconnect(); setState("Session ended"); } }, [session.data?.status]);
  useEffect(() => {
    const existing = assignment.data?.stages?.find((stage) => stage.stage_id === stageId)?.session_id;
    if (existing) { setSessionId(existing); if (observer) setState("Observing saved speech"); }
  }, [assignment.data, stageId, observer]);

  async function checkMicrophone() {
    setError(null); setBusy(true);
    try {
      disconnect();
      stream.current = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
      const audio = new AudioContext({ sampleRate: 16000 }); context.current = audio;
      await audio.audioWorklet.addModule("/audio-capture.js");
      const source = audio.createMediaStreamSource(stream.current);
      const capture = new AudioWorkletNode(audio, "capture");
      captureNode.current = capture;
      capture.port.onmessage = ({ data }: MessageEvent<ArrayBuffer | { type: "finish"; audio: ArrayBuffer }>) => {
        if (!(data instanceof ArrayBuffer)) {
          finishing.current = true;
          if (socket.current?.readyState === WebSocket.OPEN) {
            if (data.audio.byteLength) socket.current.send(data.audio);
            socket.current.send(JSON.stringify({ type: "finish" }));
          }
          return;
        }
        const samples = new Int16Array(data);
        const peak = samples.reduce((max, value) => Math.max(max, Math.abs(value)), 0) / 32768;
        setLevel(peak);
        if (peak > 0.08 && player.current) stopPlayback();
        if (!finishing.current && socket.current?.readyState === WebSocket.OPEN) {
          if (socket.current.bufferedAmount > 16000 * 2 * 3) {
            socket.current.close(); setError(new Error("The connection is too slow. Reconnect to continue."));
          } else socket.current.send(data);
        }
      };
      const mute = audio.createGain(); mute.gain.value = 0;
      source.connect(capture); capture.connect(mute); mute.connect(audio.destination);
      await audio.resume(); setChecked(true); setState("Microphone ready");
    } catch (err) { disconnect(); setChecked(false); setError(err); }
    finally { setBusy(false); }
  }
  async function connect() {
    finishing.current = false;
    setError(null); setBusy(true);
    try {
      if (!context.current) throw new Error("Check your microphone before joining.");
      const joined = await api.post<{ id: string; ticket: string; speech_path: string }>(`/assignments/${assignmentId}/stages/${stageId}/session`, { consent });
      setSessionId(joined.id); setState("Connecting");
      const ws = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${joined.speech_path}`);
      socket.current = ws; ws.binaryType = "blob";
      ws.onopen = () => ws.send(JSON.stringify({ ticket: joined.ticket }));
      ws.onclose = () => { if (socket.current === ws) { socket.current = null; setState("Disconnected"); } };
      ws.onerror = () => setError(new Error("Cannot reach the local speech service. Reconnect when it is available."));
      ws.onmessage = ({ data }) => {
        if (data instanceof Blob) {
          playbackQueue.current.push(data); playNext();
          return;
        }
        const event = JSON.parse(data);
        if (event.type === "ready") setState("Listening");
        if (event.type === "speech_started") { stopPlayback(); setState("Listening"); }
        if (event.type === "transcript_partial") setPartial(event.text);
        if (event.type === "transcript_final") { setPartial(event.text); setState("Examiner considering"); }
        if (event.type === "saved") { setPartial(""); setState("Listening"); void session.refetch(); }
        if (event.type === "drained") { finishing.current = true; stream.current?.getTracks().forEach((track) => track.stop()); setState("Speech saved"); void session.refetch(); }
        if (event.type === "question") { setQuestion(event.text); setPartial(""); void session.refetch(); }
        if (event.type === "error" || event.type === "warning") setError(new Error(event.message));
      };
    } catch (err) { setError(err); }
    finally { setBusy(false); }
  }
  async function end() {
    setBusy(true); setError(null);
    try {
      const ws = socket.current;
      if (ws?.readyState === WebSocket.OPEN) {
        setState("Saving your final words");
        await new Promise<void>((resolve, reject) => {
          const cleanup = () => { clearTimeout(timer); ws.removeEventListener("message", message); ws.removeEventListener("close", closed); };
          const message = (event: MessageEvent) => {
            if (typeof event.data === "string" && JSON.parse(event.data).type === "drained") { cleanup(); resolve(); }
          };
          const closed = () => { cleanup(); reject(new Error("The connection closed before saving finished. Check your saved transcript before finishing.")); };
          const timer = window.setTimeout(() => { cleanup(); reject(new Error("Saving took too long. Check your saved transcript and try again.")); }, 120000);
          ws.addEventListener("message", message); ws.addEventListener("close", closed);
          if (captureNode.current) captureNode.current.port.postMessage("finish");
          else ws.send(JSON.stringify({ type: "finish" }));
        });
      }
      await api.post(`/sessions/${sessionId}/end`); disconnect(); setState("Session ended"); await session.refetch();
    }
    catch (err) { setError(err); } finally { setBusy(false); }
  }
  const ended = !!session.data && session.data.status !== "running";
  const remaining = session.data?.ends_at ? Math.max(0, Math.ceil((Date.parse(session.data.ends_at) - now) / 1000)) : null;
  const lastQuestion = question || session.data?.transcript.filter((turn) => turn.speaker === "Examiner").at(-1)?.text;
  return <div className="paper-grain min-h-[100dvh]">
    {/* The clock has to be findable without being hunted for, and it stays
        numerals rather than a draining bar. */}
    <header className="sticky top-0 z-20 border-b border-rule bg-paper/90 backdrop-blur-sm">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-4 px-5 py-4">
        <div className="flex items-baseline gap-4">
          <span className="font-serif text-xl tracking-tight">MegaMoot</span>
          <span className="text-sm text-ink-muted">{humanise(stageId)}</span>
        </div>
        <div className="flex items-center gap-6">
          {remaining !== null && <span className="numeric text-2xl text-accent" aria-label="Time remaining">{formatDuration(remaining)}</span>}
          <Link className="text-sm text-ink-muted underline underline-offset-4 hover:text-ink" to={`/work/${assignmentId}`}>Back to assessment</Link>
        </div>
      </div>
    </header>
    <main className="mx-auto max-w-6xl px-5 py-8">
      {error ? <ErrorState error={error} /> : null}{session.error ? <ErrorState error={session.error} /> : null}{assignment.error ? <ErrorState error={assignment.error} /> : null}
      {ended ? <Alert tone="info" title="Your session has ended">The saved transcript is below. Results require teacher review before publication.</Alert> : null}
      <div className="mt-6 grid gap-8 lg:grid-cols-[minmax(260px,0.8fr)_minmax(0,1.2fr)]">
        <section><Surface className="p-6"><Scales size={48} className="text-accent" aria-hidden /><h1 className="mt-5 text-3xl">{observer ? "Session observation" : sessionId ? "Your oral round" : "Before you begin"}</h1><div className="mt-4"><Badge>{state}</Badge></div><p className="mt-5 text-sm leading-relaxed text-ink-muted">Wear headphones. Speak naturally and pause at the end of a point. The examiner’s question stays on screen.</p><div className="mt-6"><label htmlFor="mic-level" className="text-sm">Microphone level</label><meter id="mic-level" className="mt-3 h-3 w-full" min={0} max={1} value={level} /></div>
          {!ended && !observer && <div className="mt-6 space-y-4"><Button variant="secondary" disabled={busy || !!socket.current} onClick={() => void checkMicrophone()}>{checked ? "Check microphone again" : "Check microphone"}</Button><label className="flex items-start gap-3 text-sm"><input className="mt-1" type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />I agree to speech transcription and AI assessment. My teacher can review the saved transcript.</label><Button disabled={busy || !checked || !consent || !!socket.current} onClick={() => void connect()}>{sessionId ? "Reconnect" : "Begin session"}</Button>{sessionId && <Button variant="danger" disabled={busy} onClick={() => void end()}>Finish session</Button>}</div>}
        </Surface></section>
        <section><div className="rounded-lg bg-accent-soft p-6"><h2 className="text-sm font-medium text-accent">{lastQuestion ? "The examiner asks" : "Your speaking floor"}</h2><p className="mt-3 text-2xl leading-relaxed">{lastQuestion || "Introduce your position when the session begins."}</p></div><h2 className="mb-4 mt-8 text-xl">Saved transcript</h2><div className="max-h-[55vh] space-y-5 overflow-y-auto" aria-live="polite">{session.data?.transcript.length ? session.data.transcript.map((turn) => <div key={turn.seq}><p className="text-xs font-medium text-accent">{turn.speaker}</p><p className="mt-1 leading-relaxed">{turn.text}</p></div>) : <p className="text-sm text-ink-muted">Your transcript appears here after you speak.</p>}</div>{partial && !ended && <p className="mt-5 border-t border-rule pt-4 text-sm text-ink-muted">{partial}</p>}</section>
      </div>
    </main>
  </div>;
}


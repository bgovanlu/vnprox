// SPDX-License-Identifier: Apache-2.0

// T-2808: the in-app assistant panel.
//
// A right-hand drawer that runs the mirrored MCP read tools against the
// local daemon with the caller's own session, sends the results plus the
// question to the operator's own model backend (if they configured one),
// and renders the answer ONLY when it cites something that came back from
// those tools.
//
// What this component does NOT contain, and cannot:
//
//   - an apply/confirm/rollback control. The one write is "stage a draft",
//     which hands off to the ordinary review surface (AC4/AC5).
//   - a place a prompt or an answer is stored or shipped. Both live in this
//     component's state and die with it (AC6).
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Drawer, DrawerContent, DrawerTitle, DrawerDescription } from "../components/Drawer";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { useSession } from "../api/useSession";
import { useCreateChangesetMutation } from "../changesets/queries";
import { useChangesetDrawerStore } from "../changesets/store";
import { useAssistantStore } from "./store";
import { ask } from "./engine";
import { permittedTools, ASSISTANT_TOOLS, hasCapAnywhere } from "./tools";
import {
  clearModelBackend,
  createHttpModelTransport,
  loadModelBackend,
  saveModelBackend,
  type ModelBackend,
} from "./backend";
import type { AssistantResult } from "./citations";
import { assistantDraftTitle, proposalSummary, proposalToOp, type StagingProposal } from "./proposals";

function BackendSettings({
  backend,
  apiKey,
  onSave,
  onClear,
}: {
  backend: ModelBackend | undefined;
  apiKey: string;
  onSave: (next: ModelBackend) => void;
  onClear: () => void;
}) {
  const [endpoint, setEndpoint] = useState(backend?.endpoint ?? "");
  const [model, setModel] = useState(backend?.model ?? "");
  const [key, setKey] = useState(apiKey);

  return (
    <form
      className="mt-4 space-y-3 rounded-md border border-border p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (endpoint.trim() === "" || model.trim() === "") {
          return;
        }
        onSave({
          endpoint: endpoint.trim(),
          model: model.trim(),
          ...(key.trim() === "" ? {} : { apiKey: key.trim() }),
        });
      }}
    >
      <h3 className="text-sm font-semibold text-fg">Model backend</h3>
      <label className="block text-xs text-fg-muted">
        Endpoint (OpenAI-compatible chat completions URL)
        <input
          type="url"
          value={endpoint}
          onChange={(e) => {
            setEndpoint(e.target.value);
          }}
          placeholder="https://model.example.internal/v1/chat/completions"
          className="mt-1 h-9 w-full rounded-md border border-border-strong bg-white px-2 text-sm dark:bg-slate-900"
        />
      </label>
      <label className="block text-xs text-fg-muted">
        Model
        <input
          type="text"
          value={model}
          onChange={(e) => {
            setModel(e.target.value);
          }}
          className="mt-1 h-9 w-full rounded-md border border-border-strong bg-white px-2 text-sm dark:bg-slate-900"
        />
      </label>
      <label className="block text-xs text-fg-muted">
        API key (optional)
        <input
          type="password"
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
          }}
          className="mt-1 h-9 w-full rounded-md border border-border-strong bg-white px-2 text-sm dark:bg-slate-900"
        />
        <span className="mt-1 block text-[11px] text-fg-subtle">
          Kept in memory for this tab only — never written to storage and never sent to vnprox.
        </span>
      </label>
      <div className="flex gap-2">
        <Button type="submit" size="sm">
          Save backend
        </Button>
        {backend !== undefined && (
          <Button type="button" variant="ghost" size="sm" onClick={onClear}>
            Remove backend
          </Button>
        )}
      </div>
    </form>
  );
}

function ToolEvidence({ result }: { result: AssistantResult }) {
  if (result.status === "no-backend" || result.runs.length === 0) {
    return null;
  }
  return (
    <section className="mt-4">
      <h3 className="text-xs font-semibold uppercase tracking-wide text-fg-subtle">
        Tools run
      </h3>
      <ul className="mt-1 space-y-0.5 text-xs text-fg-muted">
        {result.runs.map((run) => (
          <li key={run.tool}>
            <code className="font-mono">{run.tool}</code>
            {run.status === "ok"
              ? ` — ${String(run.entities.length)} result(s)`
              : ` — ${run.status}${run.note === undefined ? "" : `: ${run.note}`}`}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Proposals({
  proposals,
  onStage,
  staging,
}: {
  proposals: StagingProposal[];
  onStage: () => void;
  staging: boolean;
}) {
  if (proposals.length === 0) {
    return null;
  }
  return (
    <section className="mt-4 rounded-md border border-amber-300 p-3 dark:border-amber-700">
      <h3 className="text-sm font-semibold text-fg">Proposed change</h3>
      <ul className="mt-1 list-disc space-y-1 pl-5 text-sm text-fg-muted">
        {proposals.map((proposal) => (
          <li key={`${proposal.kind}:${proposal.targetRef}`}>{proposalSummary(proposal)}</li>
        ))}
      </ul>
      <p className="mt-2 text-xs text-fg-subtle">
        Staging opens a normal draft changeset tagged as the assistant&apos;s. Nothing is applied — you review
        and apply it in the change engine exactly like any other draft.
      </p>
      <Button size="sm" className="mt-2" onClick={onStage} disabled={staging}>
        {staging ? "Staging…" : "Stage for review"}
      </Button>
    </section>
  );
}

function Answer({ result }: { result: AssistantResult }) {
  if (result.status === "answered") {
    return (
      <section className="mt-4">
        <p className="whitespace-pre-wrap text-sm leading-relaxed text-slate-800 dark:text-slate-100">
          {result.answer}
        </p>
        <h3 className="mt-3 text-xs font-semibold uppercase tracking-wide text-fg-subtle">
          Citations
        </h3>
        <ul className="mt-1 space-y-1">
          {result.citations.map((citation) => (
            <li key={`${citation.tool}:${citation.ref}`} className="text-sm">
              <a className="text-sky-700 underline dark:text-sky-400" href={citation.href}>
                {citation.label}
              </a>
              <span className="ml-1 text-xs text-fg-subtle">
                (<code className="font-mono">{citation.tool}</code> · {citation.ref})
              </span>
            </li>
          ))}
        </ul>
        {result.unresolved.length > 0 && (
          <p className="mt-2 text-xs text-amber-700 dark:text-amber-400">
            {String(result.unresolved.length)} claimed citation(s) matched nothing the tools returned and were
            dropped.
          </p>
        )}
      </section>
    );
  }
  if (result.status === "withheld") {
    // The answer text is not in `result` at all — see citations.ts. There is
    // nothing here to render even if this branch wanted to.
    return (
      <section className="mt-4 rounded-md border border-border-strong p-3 text-sm text-fg-body">
        <p className="font-semibold">Answer withheld</p>
        <p className="mt-1">
          {result.reason === "unparsable-reply"
            ? "The model's reply did not follow the required format, so it carried no citations. vnprox does not show an answer it cannot trace back to a tool result."
            : "The model's answer cited nothing that this session's tool results support, so it was discarded rather than shown."}
        </p>
        {result.unresolved.length > 0 && (
          <ul className="mt-2 list-disc pl-5 text-xs text-fg-muted">
            {result.unresolved.map((c) => (
              <li key={`${c.tool}:${c.ref}`}>
                claimed <code className="font-mono">{c.tool}</code> · {c.ref}
              </li>
            ))}
          </ul>
        )}
      </section>
    );
  }
  if (result.status === "error") {
    return (
      <p className="mt-4 text-sm text-rose-700 dark:text-rose-400">
        The model backend could not be reached: {result.message}
      </p>
    );
  }
  return null;
}

export function AssistantPanel() {
  const open = useAssistantStore((s) => s.open);
  const closePanel = useAssistantStore((s) => s.closePanel);
  const { data: session } = useSession();
  const navigate = useNavigate();
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const createChangeset = useCreateChangesetMutation();

  const [backend, setBackend] = useState<ModelBackend | undefined>(() => loadModelBackend());
  // The API key never leaves this component's memory (see backend.ts).
  const [apiKey, setApiKey] = useState("");
  const [showSettings, setShowSettings] = useState(false);
  const [question, setQuestion] = useState("");
  const [asking, setAsking] = useState(false);
  const [staging, setStaging] = useState(false);
  const [result, setResult] = useState<AssistantResult | undefined>(undefined);

  const caps = session?.caps ?? {};
  const permitted = permittedTools(caps);
  const unavailable = ASSISTANT_TOOLS.filter((t) => !hasCapAnywhere(caps, t.requiredCap));
  const configured = backend !== undefined;

  async function handleAsk(): Promise<void> {
    if (!configured || question.trim() === "") {
      return;
    }
    setAsking(true);
    try {
      const withKey: ModelBackend = { ...backend, ...(apiKey === "" ? {} : { apiKey }) };
      setResult(
        await ask({
          question: question.trim(),
          caps,
          backend: withKey,
          transport: createHttpModelTransport(withKey),
        }),
      );
    } finally {
      setAsking(false);
    }
  }

  async function handleStage(proposals: StagingProposal[]): Promise<void> {
    setStaging(true);
    try {
      const changeset = await createChangeset.mutateAsync({
        title: assistantDraftTitle(proposals),
        ops: proposals.map(proposalToOp),
      });
      // Hand off to the normal review surface: the draft becomes the
      // drawer's active changeset, and we navigate to the review screen.
      setActiveId(changeset.id);
      closePanel();
      void navigate(`/changesets/${encodeURIComponent(changeset.id)}/review`);
    } finally {
      setStaging(false);
    }
  }

  return (
    <Drawer
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          closePanel();
        }
      }}
    >
      <DrawerContent side="right" aria-describedby="assistant-panel-summary" className="max-w-lg">
        <DrawerTitle className="flex items-center gap-2 text-base font-semibold text-fg">
          Assistant
          <HelpAnchor topic="in-app-assistant" />
        </DrawerTitle>
        <DrawerDescription
          id="assistant-panel-summary"
          className="mt-1 text-sm leading-relaxed text-fg-subtle"
        >
          Asks the same read-only tools vnprox exposes over MCP, with your session and your permissions. It
          can draft a change for review; it can never apply one.
        </DrawerDescription>

        {!configured && (
          <div className="mt-4 rounded-md border border-border-strong p-3 text-sm text-fg-body">
            <p className="font-semibold">No model backend is configured.</p>
            <p className="mt-1">
              vnprox ships no model and no credential for one, and nothing is sent anywhere until you point
              this at a backend you control. Until then this panel does nothing.
            </p>
          </div>
        )}

        <div className="mt-3 flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setShowSettings((s) => !s);
            }}
          >
            {showSettings ? "Hide backend settings" : "Backend settings"}
          </Button>
        </div>

        {showSettings && (
          <BackendSettings
            backend={backend}
            apiKey={apiKey}
            onSave={(next) => {
              saveModelBackend(next);
              setBackend({ endpoint: next.endpoint, model: next.model });
              setApiKey(next.apiKey ?? "");
              setShowSettings(false);
            }}
            onClear={() => {
              clearModelBackend();
              setBackend(undefined);
              setApiKey("");
            }}
          />
        )}

        <label className="mt-4 block">
          <span className="text-xs font-semibold uppercase tracking-wide text-fg-subtle">
            Question
          </span>
          <textarea
            value={question}
            onChange={(e) => {
              setQuestion(e.target.value);
            }}
            rows={3}
            disabled={!configured}
            placeholder="Which subnets are nearly full?"
            className="mt-1 w-full rounded-md border border-border-strong bg-white p-2 text-sm disabled:opacity-50 dark:bg-slate-900"
          />
        </label>
        <Button
          size="sm"
          className="mt-2"
          disabled={!configured || asking || question.trim() === ""}
          onClick={() => {
            void handleAsk();
          }}
        >
          {asking ? "Asking…" : "Ask"}
        </Button>

        {unavailable.length > 0 && (
          <p className="mt-3 text-xs text-fg-subtle">
            Your account cannot reach {unavailable.map((t) => t.name).join(", ")}, so the assistant does not
            either. It runs {String(permitted.length)} of {String(ASSISTANT_TOOLS.length)} tools for you.
          </p>
        )}

        {result !== undefined && (
          <>
            <Answer result={result} />
            {result.status === "answered" && (
              <Proposals
                proposals={result.proposals}
                staging={staging}
                onStage={() => {
                  void handleStage(result.proposals);
                }}
              />
            )}
            <ToolEvidence result={result} />
          </>
        )}
      </DrawerContent>
    </Drawer>
  );
}

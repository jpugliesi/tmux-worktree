import {
  AgentBusyError,
  AgentNotFoundError,
  AuthenticationError,
  ConfigurationError,
  CursorSdkError,
  IntegrationNotConnectedError,
  NetworkError,
  RateLimitError,
} from "@cursor/sdk/bundled";
import type {
  AgentOptions,
  ModelListItem,
  ModelParameterDefinition,
  ModelSelection,
  SendOptions,
} from "@cursor/sdk/bundled";

import type { DispatchRequest, Effort, SessionReference, SyncRequest } from "./protocol.ts";

type HarnessError = {
  kind: string;
  code?: string;
  message: string;
  retryable?: boolean;
  helpUrl?: string;
  requestId?: string;
};

type GitBranch = {
  repoUrl: string;
  branch?: string;
  prUrl?: string;
};

export type RunView = {
  id: string;
  agentId: string;
  requestId?: string;
  status: "running" | "finished" | "error" | "cancelled";
  createdAt?: number;
  result?: string;
  error?: { message: string; code?: string };
  git?: { branches: GitBranch[] };
};

type AgentHandle = {
  agentId: string;
  send(prompt: string, options: SendOptions): Promise<RunView>;
  close(): void;
};

export interface CloudSDK {
  listModels(): Promise<ModelListItem[]>;
  create(options: AgentOptions): Promise<AgentHandle>;
  getRun(reference: SessionReference): Promise<RunView | undefined>;
}

export type DispatchResult = {
  agentId: string;
  runId: string;
  requestId?: string;
  effort: { kind: "parameter" | "prompt"; value: string };
};

export type SyncObservation = {
  sessionId: string;
  agentId?: string;
  runId?: string;
  status: "running" | "finished" | "error" | "cancelled" | "unknown";
  requestId?: string;
  result?: string;
  error?: HarnessError;
  repositories: Array<{ url: string; branch?: string; prUrl?: string }>;
};

export type SyncResult = { sessions: SyncObservation[] };

type EffortSelection = {
  model?: ModelSelection;
  prompt: string;
  effective: DispatchResult["effort"];
};

const effortValues: Record<Effort, readonly string[]> = {
  small: ["low", "minimal", "small"],
  medium: ["medium"],
  large: ["high", "large"],
  xlarge: ["xhigh", "max", "extra_high", "extra-high", "xlarge"],
};

const effortInstructions: Record<Effort, string> = {
  small: "Use a small reasoning effort for this task.",
  medium: "Use a medium reasoning effort for this task.",
  large: "Use a large reasoning effort. Check the important edge cases before you finish.",
  xlarge: "Use an extra-large reasoning effort. Analyze the design and edge cases in depth before you finish.",
};

function normalized(value: string): string {
  return value.trim().toLowerCase().replaceAll(" ", "_");
}

function matchesModel(model: ModelListItem, requested: string): boolean {
  const name = normalized(requested);
  return normalized(model.id) === name || (model.aliases ?? []).some((alias) => normalized(alias) === name);
}

function effortParameter(model: ModelListItem): ModelParameterDefinition | undefined {
  return model.parameters?.find((parameter) => {
    const id = normalized(parameter.id);
    const displayName = normalized(parameter.displayName ?? "");
    return id.includes("reasoning") || id.includes("effort") || displayName.includes("reasoning") || displayName.includes("effort");
  });
}

function promptFallback(request: DispatchRequest, model?: ModelSelection): EffortSelection {
  return {
    ...(model === undefined ? {} : { model }),
    prompt: `${effortInstructions[request.effort]}\n\n${request.prompt}`,
    effective: { kind: "prompt", value: request.effort },
  };
}

async function selectEffort(request: DispatchRequest, sdk: CloudSDK): Promise<EffortSelection> {
  if (request.model === undefined) {
    return promptFallback(request);
  }
  const requestedModel = request.model;
  const selected = (await sdk.listModels()).find((model) => matchesModel(model, requestedModel));
  if (selected === undefined) {
    return promptFallback(request, { id: request.model });
  }
  const parameter = effortParameter(selected);
  if (parameter === undefined) {
    return promptFallback(request, { id: selected.id });
  }
  const candidates = effortValues[request.effort];
  const value = parameter.values.find((item) => candidates.includes(normalized(item.value)))?.value;
  if (value === undefined) {
    return promptFallback(request, { id: selected.id });
  }
  return {
    model: { id: selected.id, params: [{ id: parameter.id, value }] },
    prompt: request.prompt,
    effective: { kind: "parameter", value },
  };
}

export async function dispatch(request: DispatchRequest, sdk: CloudSDK): Promise<DispatchResult> {
  const selected = await selectEffort(request, sdk);
  const agent = await sdk.create({
    ...(selected.model === undefined ? {} : { model: selected.model }),
    idempotencyKey: request.createIdempotencyKey,
    mode: request.mode,
    cloud: {
      repos: request.repositories.map((repository) => ({
        url: repository.url,
        ...(repository.startingRef === undefined ? {} : { startingRef: repository.startingRef }),
      })),
      workOnCurrentBranch: false,
      autoCreatePR: request.mode === "agent",
      metadata: request.metadata,
    },
  });
  try {
    const run = await agent.send(selected.prompt, {
      mode: request.mode,
      idempotencyKey: request.sendIdempotencyKey,
    });
    return {
      agentId: agent.agentId,
      runId: run.id,
      ...(run.requestId === undefined ? {} : { requestId: run.requestId }),
      effort: selected.effective,
    };
  } finally {
    agent.close();
  }
}

function terminalError(run: RunView): HarnessError | undefined {
  if (run.error === undefined) {
    return undefined;
  }
  return {
    kind: "cursor_run",
    ...(run.error.code === undefined ? {} : { code: run.error.code }),
    message: run.error.message,
  };
}

function observation(sessionId: string, run: RunView): SyncObservation {
  const error = terminalError(run);
  return {
    sessionId,
    agentId: run.agentId,
    runId: run.id,
    status: run.status,
    ...(run.requestId === undefined ? {} : { requestId: run.requestId }),
    ...(run.result === undefined ? {} : { result: run.result }),
    ...(error === undefined ? {} : { error }),
    repositories: (run.git?.branches ?? []).map((branch) => ({
      url: branch.repoUrl,
      ...(branch.branch === undefined ? {} : { branch: branch.branch }),
      ...(branch.prUrl === undefined ? {} : { prUrl: branch.prUrl }),
    })),
  };
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function classifyError(error: unknown): HarnessError {
  if (error instanceof IntegrationNotConnectedError) {
    return sdkError("configuration", error, { helpUrl: error.helpUrl });
  }
  if (error instanceof AuthenticationError || error instanceof ConfigurationError || error instanceof AgentNotFoundError) {
    return sdkError("configuration", error);
  }
  if (error instanceof NetworkError || error instanceof RateLimitError) {
    return sdkError("network", error, { retryable: true });
  }
  if (error instanceof AgentBusyError) {
    return sdkError("unknown", error);
  }
  if (error instanceof CursorSdkError) {
    return sdkError("unknown", error);
  }
  return { kind: "unknown", message: message(error) };
}

function sdkError(
  kind: string,
  error: CursorSdkError,
  extra: { retryable?: boolean; helpUrl?: string } = {},
): HarnessError {
  return {
    kind,
    ...(error.code === undefined ? {} : { code: error.code }),
    message: error.message,
    ...(extra.retryable === undefined && !error.isRetryable ? {} : { retryable: extra.retryable ?? error.isRetryable }),
    ...(extra.helpUrl === undefined ? {} : { helpUrl: extra.helpUrl }),
    ...(error.requestId === undefined ? {} : { requestId: error.requestId }),
  };
}

export async function sync(request: SyncRequest, sdk: CloudSDK): Promise<SyncResult> {
  const sessions = await Promise.all(
    request.sessions.map(async (session): Promise<SyncObservation> => {
      try {
        const run = await sdk.getRun(session);
        if (run === undefined) {
          return {
            sessionId: session.sessionId,
            status: "unknown",
            error: { kind: "unknown", message: "No Cursor Cloud run matches this Session" },
            repositories: [],
          };
        }
        return observation(session.sessionId, run);
      } catch (error) {
        return {
          sessionId: session.sessionId,
          status: "unknown",
          error: classifyError(error),
          repositories: [],
        };
      }
    }),
  );
  return { sessions };
}

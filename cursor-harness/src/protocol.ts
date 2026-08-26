export const schemaVersion = 1;

export type Mode = "agent" | "plan";
export type Effort = "small" | "medium" | "large" | "xlarge";

export type Repository = {
  name: string;
  url: string;
  startingRef?: string;
};

export type DispatchRequest = {
  schemaVersion: 1;
  operation: "dispatch";
  sessionId: string;
  mode: Mode;
  prompt: string;
  model?: string;
  effort: Effort;
  createIdempotencyKey: string;
  sendIdempotencyKey: string;
  metadata: Record<string, string>;
  repositories: Repository[];
};

export type SessionReference = {
  sessionId: string;
  agentId?: string;
  runId?: string;
};

export type SyncRequest = {
  schemaVersion: 1;
  operation: "sync";
  sessions: SessionReference[];
};

export type Request = DispatchRequest | SyncRequest;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function record(value: unknown, name: string): Record<string, unknown> {
  if (!isRecord(value)) {
    throw new Error(`${name} must be an object`);
  }
  return value;
}

function exactKeys(value: Record<string, unknown>, allowed: readonly string[], name: string): void {
  const allowedKeys = new Set(allowed);
  for (const key of Object.keys(value)) {
    if (!allowedKeys.has(key)) {
      throw new Error(`${name} has unknown field ${JSON.stringify(key)}`);
    }
  }
}

function requiredString(value: Record<string, unknown>, key: string): string {
  const found = value[key];
  if (typeof found !== "string" || found.trim() === "") {
    throw new Error(`${key} must be a non-empty string`);
  }
  return found;
}

function optionalString(value: Record<string, unknown>, key: string): string | undefined {
  const found = value[key];
  if (found === undefined) {
    return undefined;
  }
  if (typeof found !== "string" || found.trim() === "") {
    throw new Error(`${key} must be a non-empty string when set`);
  }
  return found;
}

function array(value: Record<string, unknown>, key: string): unknown[] {
  const found = value[key];
  if (!Array.isArray(found)) {
    throw new Error(`${key} must be an array`);
  }
  return found;
}

function mode(value: string): Mode {
  switch (value) {
    case "agent":
    case "plan":
      return value;
    default:
      throw new Error(`mode ${JSON.stringify(value)} is invalid`);
  }
}

function effort(value: string): Effort {
  switch (value) {
    case "small":
    case "medium":
    case "large":
    case "xlarge":
      return value;
    default:
      throw new Error(`effort ${JSON.stringify(value)} is invalid`);
  }
}

function metadata(value: unknown): Record<string, string> {
  const source = record(value, "metadata");
  const parsed: Record<string, string> = {};
  for (const [key, item] of Object.entries(source)) {
    if (typeof item !== "string") {
      throw new Error(`metadata field ${JSON.stringify(key)} must be a string`);
    }
    parsed[key] = item;
  }
  return parsed;
}

function repository(value: unknown): Repository {
  const source = record(value, "repository");
  exactKeys(source, ["name", "url", "startingRef"], "repository");
  const startingRef = optionalString(source, "startingRef");
  return {
    name: requiredString(source, "name"),
    url: requiredString(source, "url"),
    ...(startingRef === undefined ? {} : { startingRef }),
  };
}

function sessionReference(value: unknown): SessionReference {
  const source = record(value, "session reference");
  exactKeys(source, ["sessionId", "agentId", "runId"], "session reference");
  const agentId = optionalString(source, "agentId");
  const runId = optionalString(source, "runId");
  if ((agentId === undefined) !== (runId === undefined)) {
    throw new Error("agentId and runId must be set together");
  }
  return {
    sessionId: requiredString(source, "sessionId"),
    ...(agentId === undefined ? {} : { agentId }),
    ...(runId === undefined ? {} : { runId }),
  };
}

function parseDispatch(source: Record<string, unknown>): DispatchRequest {
  exactKeys(
    source,
    [
      "schemaVersion",
      "operation",
      "sessionId",
      "mode",
      "prompt",
      "model",
      "effort",
      "createIdempotencyKey",
      "sendIdempotencyKey",
      "metadata",
      "repositories",
    ],
    "dispatch request",
  );
  const model = optionalString(source, "model");
  return {
    schemaVersion,
    operation: "dispatch",
    sessionId: requiredString(source, "sessionId"),
    mode: mode(requiredString(source, "mode")),
    prompt: requiredString(source, "prompt"),
    ...(model === undefined ? {} : { model }),
    effort: effort(requiredString(source, "effort")),
    createIdempotencyKey: requiredString(source, "createIdempotencyKey"),
    sendIdempotencyKey: requiredString(source, "sendIdempotencyKey"),
    metadata: metadata(source.metadata),
    repositories: array(source, "repositories").map(repository),
  };
}

function parseSync(source: Record<string, unknown>): SyncRequest {
  exactKeys(source, ["schemaVersion", "operation", "sessions"], "sync request");
  return {
    schemaVersion,
    operation: "sync",
    sessions: array(source, "sessions").map(sessionReference),
  };
}

export function parseRequest(value: unknown): Request {
  const source = record(value, "request");
  if (source.schemaVersion !== schemaVersion) {
    throw new Error(`schemaVersion must be ${schemaVersion}`);
  }
  switch (source.operation) {
    case "dispatch":
      return parseDispatch(source);
    case "sync":
      return parseSync(source);
    default:
      throw new Error(`operation ${JSON.stringify(source.operation)} is invalid`);
  }
}

#!/usr/bin/env bun

import { Agent, ConfigurationError, Cursor } from "@cursor/sdk/bundled";
import type { AgentOptions } from "@cursor/sdk/bundled";

import { parseRequest, schemaVersion, type Request, type SessionReference } from "./protocol.ts";
import { classifyError, dispatch, sync, type CloudSDK, type RunView } from "./runtime.ts";

const maximumInputBytes = 1024 * 1024;

const sdk: CloudSDK = {
  async listModels() {
    return Cursor.models.list();
  },
  async create(options: AgentOptions) {
    return Agent.create(options);
  },
  async getRun(reference: SessionReference) {
    if (reference.agentId !== undefined && reference.runId !== undefined) {
      return Agent.getRun(reference.runId, { runtime: "cloud", agentId: reference.agentId });
    }
    return findRunBySession(reference.sessionId);
  },
};

async function findRunBySession(sessionId: string): Promise<RunView | undefined> {
  let cursor: string | undefined;
  const agentIds: string[] = [];
  do {
    const page = await Agent.list({
      runtime: "cloud",
      limit: 100,
      ...(cursor === undefined ? {} : { cursor }),
    });
    for (const agent of page.items) {
      if (agent.runtime === "cloud" && agent.metadata?.session === sessionId) {
        agentIds.push(agent.agentId);
      }
    }
    cursor = page.nextCursor;
  } while (cursor !== undefined);
  if (agentIds.length === 0) {
    return undefined;
  }
  if (agentIds.length > 1) {
    throw new ConfigurationError(`More than one Cursor Cloud Agent matches Session ${JSON.stringify(sessionId)}`);
  }
  const agentId = agentIds[0];
  if (agentId === undefined) {
    return undefined;
  }
  let runCursor: string | undefined;
  const runs: RunView[] = [];
  do {
    const page = await Agent.listRuns(agentId, {
      runtime: "cloud",
      limit: 100,
      ...(runCursor === undefined ? {} : { cursor: runCursor }),
    });
    runs.push(...page.items);
    runCursor = page.nextCursor;
  } while (runCursor !== undefined);
  runs.sort((left, right) => (right.createdAt ?? 0) - (left.createdAt ?? 0));
  return runs[0];
}

async function readInput(): Promise<string> {
  const reader = Bun.stdin.stream().getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  while (true) {
    const item = await reader.read();
    if (item.done) {
      break;
    }
    size += item.value.byteLength;
    if (size > maximumInputBytes) {
      await reader.cancel();
      throw new Error(`request is larger than ${maximumInputBytes} bytes`);
    }
    chunks.push(item.value);
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(bytes);
}

function detectedOperation(value: unknown): "dispatch" | "sync" | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return undefined;
  }
  if ("operation" in value && (value.operation === "dispatch" || value.operation === "sync")) {
    return value.operation;
  }
  return undefined;
}

function write(value: unknown): void {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

async function execute(request: Request): Promise<unknown> {
  switch (request.operation) {
    case "dispatch":
      return dispatch(request, sdk);
    case "sync":
      return sync(request, sdk);
  }
}

async function main(): Promise<void> {
  let source: unknown;
  try {
    source = JSON.parse(await readInput());
    const request = parseRequest(source);
    try {
      write({ schemaVersion, operation: request.operation, result: await execute(request) });
    } catch (error) {
      write({ schemaVersion, operation: request.operation, error: classifyError(error) });
    }
  } catch (error) {
    const operation = detectedOperation(source);
    if (operation === undefined) {
      process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
      process.exitCode = 2;
      return;
    }
    write({ schemaVersion, operation, error: { kind: "configuration", message: error instanceof Error ? error.message : String(error) } });
  }
}

await main();

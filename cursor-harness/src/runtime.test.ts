import { describe, expect, test } from "bun:test";

import { dispatch, sync, type CloudSDK, type RunView } from "./runtime.ts";
import type { DispatchRequest, SyncRequest } from "./protocol.ts";

function request(overrides: Partial<DispatchRequest> = {}): DispatchRequest {
  return {
    schemaVersion: 1,
    operation: "dispatch",
    sessionId: "session-one",
    mode: "agent",
    prompt: "Implement the Ticket.",
    model: "model-one",
    effort: "large",
    createIdempotencyKey: "create-one",
    sendIdempotencyKey: "send-one",
    metadata: { ticket: "fix-auth" },
    repositories: [{ name: "api", url: "https://github.com/acme/api.git", startingRef: "main" }],
    ...overrides,
  };
}

function sdk(run: RunView): { api: CloudSDK; calls: Array<Record<string, unknown>> } {
  const calls: Array<Record<string, unknown>> = [];
  return {
    calls,
    api: {
      async listModels() {
        return [
          {
            id: "model-one",
            displayName: "Model One",
            parameters: [
              {
                id: "reasoning_effort",
                values: [{ value: "low" }, { value: "medium" }, { value: "high" }, { value: "xhigh" }],
              },
            ],
          },
        ];
      },
      async create(options) {
        calls.push({ create: options });
        return {
          agentId: "bc-agent",
          async send(prompt, options) {
            calls.push({ send: { prompt, options } });
            return run;
          },
          close() {},
        };
      },
      async getRun() {
        return run;
      },
    },
  };
}

describe("dispatch", () => {
  test("maps large effort to the selected model parameter", async () => {
    const fake = sdk({ id: "run-one", agentId: "bc-agent", status: "running" });

    const result = await dispatch(request(), fake.api);

    expect(result).toEqual({
      agentId: "bc-agent",
      runId: "run-one",
      effort: { kind: "parameter", value: "high" },
    });
    expect(fake.calls[0]).toMatchObject({
      create: {
        model: { id: "model-one", params: [{ id: "reasoning_effort", value: "high" }] },
        idempotencyKey: "create-one",
        cloud: {
          autoCreatePR: true,
          metadata: { ticket: "fix-auth" },
          repos: [{ url: "https://github.com/acme/api.git", startingRef: "main" }],
        },
      },
    });
    expect(fake.calls[1]).toMatchObject({ send: { options: { mode: "agent", idempotencyKey: "send-one" } } });
  });

  test("prepends an effort instruction when the selected model has no effort parameter", async () => {
    const fake = sdk({ id: "run-one", agentId: "bc-agent", status: "running" });
    fake.api.listModels = async () => [{ id: "model-one", displayName: "Model One" }];

    const result = await dispatch(request({ effort: "xlarge" }), fake.api);

    expect(result.effort).toEqual({ kind: "prompt", value: "xlarge" });
    expect(fake.calls[1]).toMatchObject({
      send: { prompt: expect.stringContaining("Use an extra-large reasoning effort") },
    });
  });

  test("does not request a pull request in plan mode", async () => {
    const fake = sdk({ id: "run-one", agentId: "bc-agent", status: "running" });

    await dispatch(request({ mode: "plan" }), fake.api);

    expect(fake.calls[0]).toMatchObject({ create: { cloud: { autoCreatePR: false } } });
  });
});

describe("sync", () => {
  test("returns branches and pull requests for a finished run", async () => {
    const fake = sdk({
      id: "run-one",
      agentId: "bc-agent",
      requestId: "request-one",
      status: "finished",
      result: "Implemented the change.",
      git: {
        branches: [
          {
            repoUrl: "https://github.com/acme/api.git",
            branch: "cursor/fix-auth",
            prUrl: "https://github.com/acme/api/pull/42",
          },
        ],
      },
    });
    const syncRequest: SyncRequest = {
      schemaVersion: 1,
      operation: "sync",
      sessions: [{ sessionId: "session-one", agentId: "bc-agent", runId: "run-one" }],
    };

    const result = await sync(syncRequest, fake.api);

    expect(result.sessions).toEqual([
      {
        sessionId: "session-one",
        agentId: "bc-agent",
        runId: "run-one",
        status: "finished",
        requestId: "request-one",
        result: "Implemented the change.",
        repositories: [
          {
            url: "https://github.com/acme/api.git",
            branch: "cursor/fix-auth",
            prUrl: "https://github.com/acme/api/pull/42",
          },
        ],
      },
    ]);
  });

  test("isolates a failed lookup to its session", async () => {
    const fake = sdk({ id: "run-one", agentId: "bc-agent", status: "running" });
    fake.api.getRun = async () => {
      throw new Error("lookup failed");
    };
    const syncRequest: SyncRequest = {
      schemaVersion: 1,
      operation: "sync",
      sessions: [{ sessionId: "session-one", agentId: "bc-agent", runId: "run-one" }],
    };

    const result = await sync(syncRequest, fake.api);

    expect(result.sessions[0]).toMatchObject({
      sessionId: "session-one",
      status: "unknown",
      error: { kind: "unknown", message: "lookup failed" },
    });
  });
});

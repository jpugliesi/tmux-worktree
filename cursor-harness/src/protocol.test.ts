import { describe, expect, test } from "bun:test";

import { parseRequest } from "./protocol.ts";

describe("parseRequest", () => {
  test("parses a dispatch request", () => {
    const request = parseRequest({
      schemaVersion: 1,
      operation: "dispatch",
      sessionId: "session-one",
      mode: "agent",
      prompt: "Implement the Ticket.",
      effort: "large",
      createIdempotencyKey: "create-one",
      sendIdempotencyKey: "send-one",
      metadata: { session: "session-one", ticket: "fix-auth", project: "core" },
      repositories: [{ name: "api", url: "https://github.com/acme/api.git", startingRef: "main" }],
    });

    expect(request.operation).toBe("dispatch");
    if (request.operation === "dispatch") {
      expect(request.repositories[0]?.name).toBe("api");
      expect(request.effort).toBe("large");
    }
  });

  test("parses a sync request", () => {
    const request = parseRequest({
      schemaVersion: 1,
      operation: "sync",
      sessions: [{ sessionId: "session-one", agentId: "bc-agent", runId: "run-one" }],
    });
    expect(request.operation).toBe("sync");
  });

  test("parses a sync recovery request without remote IDs", () => {
    const request = parseRequest({
      schemaVersion: 1,
      operation: "sync",
      sessions: [{ sessionId: "session-one" }],
    });
    expect(request.operation).toBe("sync");
  });

  test("rejects a partial remote reference", () => {
    expect(() =>
      parseRequest({
        schemaVersion: 1,
        operation: "sync",
        sessions: [{ sessionId: "session-one", agentId: "bc-agent" }],
      }),
    ).toThrow("set together");
  });

  test("rejects unknown fields", () => {
    expect(() =>
      parseRequest({ schemaVersion: 1, operation: "sync", sessions: [], surprise: true }),
    ).toThrow("unknown field");
  });

  test("rejects an unsupported effort", () => {
    expect(() =>
      parseRequest({
        schemaVersion: 1,
        operation: "dispatch",
        sessionId: "session-one",
        mode: "agent",
        prompt: "Implement.",
        effort: "huge",
        createIdempotencyKey: "create-one",
        sendIdempotencyKey: "send-one",
        metadata: {},
        repositories: [],
      }),
    ).toThrow("effort");
  });
});

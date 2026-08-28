// SPDX-License-Identifier: Apache-2.0

// T-2808: the model backend is configurable and ABSENT BY DEFAULT.
import { describe, expect, it } from "vitest";
import {
  clearModelBackend,
  extractReplyText,
  loadModelBackend,
  saveModelBackend,
  type BackendStore,
} from "./backend";

function memoryStore(initial: Record<string, string> = {}): BackendStore & { data: Record<string, string> } {
  const data: Record<string, string> = { ...initial };
  return {
    data,
    getItem: (key) => data[key] ?? null,
    setItem: (key, value) => {
      data[key] = value;
    },
    removeItem: (key) => {
      data[key] = "";
    },
  };
}

describe("loadModelBackend", () => {
  it("is absent on a fresh install", () => {
    expect(loadModelBackend(memoryStore())).toBeUndefined();
  });

  it("is absent when storage is unavailable entirely", () => {
    expect(loadModelBackend(undefined)).toBeUndefined();
  });

  it("reads a configured backend", () => {
    const store = memoryStore();
    saveModelBackend({ endpoint: "https://m.invalid/v1/chat/completions", model: "m" }, store);
    expect(loadModelBackend(store)).toEqual({ endpoint: "https://m.invalid/v1/chat/completions", model: "m" });
  });

  it("treats malformed or half-configured storage as absent", () => {
    const key = "vnprox.assistant.backend";
    expect(loadModelBackend(memoryStore({ [key]: "not json" }))).toBeUndefined();
    expect(loadModelBackend(memoryStore({ [key]: JSON.stringify({ endpoint: "https://m.invalid" }) }))).toBeUndefined();
    expect(loadModelBackend(memoryStore({ [key]: JSON.stringify({ model: "m" }) }))).toBeUndefined();
    expect(loadModelBackend(memoryStore({ [key]: JSON.stringify(["nope"]) }))).toBeUndefined();
  });

  it("clearing it returns to the default absent state", () => {
    const store = memoryStore();
    saveModelBackend({ endpoint: "https://m.invalid/v1", model: "m" }, store);
    clearModelBackend(store);
    expect(loadModelBackend(store)).toBeUndefined();
  });
});

describe("credential handling", () => {
  it("never persists the API key", () => {
    const store = memoryStore();
    saveModelBackend({ endpoint: "https://m.invalid/v1", model: "m", apiKey: "SECRET-KEY-DO-NOT-STORE" }, store);

    const dumped = JSON.stringify(store.data);
    // CONTROL: the endpoint IS stored, so this scan reads real content.
    expect(dumped).toContain("https://m.invalid/v1");
    expect(dumped).not.toContain("SECRET-KEY-DO-NOT-STORE");
    expect(loadModelBackend(store)?.apiKey).toBeUndefined();
  });
});

describe("extractReplyText", () => {
  it("pulls the assistant message out of an OpenAI-shaped body", () => {
    expect(extractReplyText({ choices: [{ message: { content: "hi" } }] })).toBe("hi");
  });

  it("returns an empty string for any other shape, which the parser then refuses", () => {
    expect(extractReplyText(undefined)).toBe("");
    expect(extractReplyText({ choices: [] })).toBe("");
    expect(extractReplyText({ choices: [{ message: {} }] })).toBe("");
    expect(extractReplyText("a string")).toBe("");
  });
});

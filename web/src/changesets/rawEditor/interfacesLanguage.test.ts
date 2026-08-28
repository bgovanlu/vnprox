// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import {
  INTERFACES_LANGUAGE_ID,
  interfacesMonarchLanguage,
  registerInterfacesLanguage,
  type MonacoLanguagesLike,
  type MonarchLanguage,
} from "./interfacesLanguage";

function fakeMonaco(existing: { id: string }[] = []) {
  const register = vi.fn<(language: { id: string }) => void>();
  const setMonarchTokensProvider = vi.fn<(languageId: string, language: MonarchLanguage) => void>();
  const setLanguageConfiguration = vi.fn<(languageId: string, configuration: { comments: { lineComment: string } }) => void>();
  const monaco: MonacoLanguagesLike = {
    languages: {
      getLanguages: () => existing,
      register,
      setMonarchTokensProvider,
      setLanguageConfiguration,
    },
  };
  return { monaco, register, setMonarchTokensProvider, setLanguageConfiguration };
}

describe("registerInterfacesLanguage", () => {
  it("registers the language id when not already present", () => {
    const { monaco, register } = fakeMonaco([]);
    registerInterfacesLanguage(monaco);
    expect(register).toHaveBeenCalledWith({ id: INTERFACES_LANGUAGE_ID });
  });

  it("does not re-register an already-present language id", () => {
    const { monaco, register } = fakeMonaco([{ id: INTERFACES_LANGUAGE_ID }]);
    registerInterfacesLanguage(monaco);
    expect(register).not.toHaveBeenCalled();
  });

  it("always (re)installs the Monarch tokenizer and comment config", () => {
    const { monaco, setMonarchTokensProvider, setLanguageConfiguration } = fakeMonaco([{ id: INTERFACES_LANGUAGE_ID }]);
    registerInterfacesLanguage(monaco);
    expect(setMonarchTokensProvider).toHaveBeenCalledWith(INTERFACES_LANGUAGE_ID, interfacesMonarchLanguage);
    expect(setLanguageConfiguration).toHaveBeenCalledWith(INTERFACES_LANGUAGE_ID, {
      comments: { lineComment: "#" },
    });
  });
});

function ruleFor(pattern: (token: string) => boolean): RegExp {
  const rule = interfacesMonarchLanguage.tokenizer.root.find(([, token]) => pattern(token));
  if (!rule) {
    throw new Error("no matching Monarch rule found");
  }
  return rule[0];
}

describe("interfacesMonarchLanguage", () => {
  it("recognizes comment lines but not auto lines as comments", () => {
    const commentRule = ruleFor((t) => t === "comment");
    expect("# a comment").toMatch(commentRule);
    expect("auto vmbr0").not.toMatch(commentRule);
  });

  it("recognizes the iface header, bare iface, and auto/allow-* keywords", () => {
    const keywordRules = interfacesMonarchLanguage.tokenizer.root.filter(([, token]) => token === "keyword").map(([re]) => re);
    expect(keywordRules.some((re) => "iface vmbr0 inet static".match(re))).toBe(true);
    expect(keywordRules.some((re) => "iface".match(re))).toBe(true);
    expect(keywordRules.some((re) => "auto vmbr0".match(re))).toBe(true);
    expect(keywordRules.some((re) => "allow-hotplug eno1".match(re))).toBe(true);
  });

  it("recognizes CIDR addresses as numbers", () => {
    const numberRule = ruleFor((t) => t === "number");
    expect("10.0.0.1/24").toMatch(numberRule);
  });
});

import { describe, expect, it } from "vitest";
import { toolLabels } from "../src/webview/toolCard";

describe("tool summary labels", () => {
  it.each([
    // Shell calls: the title already carries the command name.
    ["cd", "cd /repo && grep -rn x", { name: "", detail: "cd /repo && grep -rn x" }],
    ["read", "read(internal/tui/sidebar.go)", { name: "", detail: "read(internal/tui/sidebar.go)" }],
    // A title that describes the call keeps the name beside it.
    ["read", "internal/tui/sidebar.go", { name: "read", detail: "internal/tui/sidebar.go" }],
    // Name only, or a title equal to it: never print it twice.
    ["ls", undefined, { name: "ls", detail: "" }],
    ["ls", "ls", { name: "ls", detail: "" }],
    // A prefix that is not a whole word is not a repeat.
    ["cd", "cdk deploy", { name: "cd", detail: "cdk deploy" }],
  ])("%s + %s", (name, title, want) => {
    expect(toolLabels(name, title)).toEqual(want);
  });
});

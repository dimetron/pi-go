import { describe, expect, it } from "vitest";
import { explainPiGoError, renderPiGoErrorMarkdown } from "../src/errorInfo";

describe("Pi-Go error explanations", () => {
  it("explains provider quota failures with recovery steps", () => {
    const info = explainPiGoError(
      new Error("429 Too Many Requests: monthly usage limit reached"),
      { command: "pi", args: ["acp-server"], cwd: "/tmp/project" },
    );
    expect(info.title).toContain("provider limit");
    expect(info.detail).toContain("429 Too Many Requests");
    expect(info.steps.join(" ")).toContain("quota");
    expect(renderPiGoErrorMarkdown(info)).toContain("pi-go.command");
  });

  it("explains a missing command as a VS Code setting problem", () => {
    const info = explainPiGoError(
      Object.assign(new Error("spawn pi ENOENT"), { code: "ENOENT" }),
      { command: "pi", args: ["acp-server"], cwd: "/tmp/project" },
    );
    expect(info.title).toContain("could not start");
    expect(info.steps.join(" ")).toContain("absolute path");
    expect(info.detail).toContain("/tmp/project");
  });
});

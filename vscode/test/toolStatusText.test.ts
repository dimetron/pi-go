import { describe, expect, it } from "vitest";
import { toolStatusText } from "../src/webview/toolCard";

describe("tool status text", () => {
  it("hides the clock on a running tool until a second has passed", () => {
    expect(toolStatusText("in_progress", "running", 0)).toBe("running");
    expect(toolStatusText("in_progress", "running", 999)).toBe("running");
    expect(toolStatusText("in_progress", "running", 2300)).toBe("running · 2.3s");
  });

  it("keeps sub-second final durations for settled tools", () => {
    expect(toolStatusText("completed", "done", 480)).toBe("done · 480ms");
    expect(toolStatusText("failed", "failed", 64_000)).toBe("failed · 1m 04s");
  });

  it("shows the bare label when timing is unknown", () => {
    expect(toolStatusText("pending", "queued", undefined)).toBe("queued");
    expect(toolStatusText("completed", "done", undefined)).toBe("done");
  });
});

import { describe, expect, it } from "vitest";
import { formatPiPingResult, runPiPing, sanitizePiPingOutput } from "../src/ping";

describe("Pi-Go ping output", () => {
  it("removes credential and authorization values from provider output", () => {
    const output = sanitizePiPingOutput(
      "API Key: agw_secret_value\n> Authorization: Bearer abc123\n* Model replied: ok",
    );
    expect(output).not.toContain("agw_secret_value");
    expect(output).not.toContain("abc123");
    expect(output).toContain("API Key: [redacted]");
    expect(output).toContain("Authorization: [redacted]");
  });

  it("formats a successful ping as a compact chat result", () => {
    const result = formatPiPingResult({
      exitCode: 0,
      stdout: "Provider: agentgateway\nModel: ollama-deepseek\n✓ Prompt OK — model is ALIVE",
      stderr: "",
    });
    expect(result.ok).toBe(true);
    expect(result.title).toBe("Pi-Go ping succeeded");
    expect(result.detail).toContain("agentgateway");
    expect(result.detail).toContain("ollama-deepseek");
  });

  it("runs the configured command with the ping subcommand", async () => {
    const result = await runPiPing({ command: "/bin/echo", cwd: "/tmp" });
    expect(result.ok).toBe(true);
    expect(result.detail).toBe("ping");
  });
});

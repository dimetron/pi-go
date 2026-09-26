import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// workbench.action.openView is VS Code's "Open View…" quick pick: it ignores
// a view-id argument, so calling it to focus a view pops the "view " picker
// instead (clicking a session in the Sessions tree did exactly that). Views
// must be focused with "<viewId>.focus" or "workbench.view.extension.<id>".
function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return path.endsWith(".ts") ? [path] : [];
  });
}

describe("view focus commands", () => {
  it("never executes workbench.action.openView", () => {
    const offenders = sourceFiles(join(__dirname, "..", "src")).filter((file) =>
      /executeCommand\(\s*["']workbench\.action\.openView["']/.test(readFileSync(file, "utf8")),
    );
    expect(offenders).toEqual([]);
  });
});

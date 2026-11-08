export const title = "The /run pipeline: what the user sees";

export const state = (v) => {
  switch (v.phase) {
    case "none": return "No run";
    case "running": return v.retries > 0 ? "Agent working, after a retry" : "Agent working";
    case "gating": return "Checking gates";
    case "verifying": return "Checking the plan was carried out";
    case "retrying": return "Restarting the agent";
    case "merging": return "Merging";
    case "done": return "Run finished, merged";
    default: return "Run failed";
  }
};

export const group = (v) => {
  switch (v.phase) {
    case "none": return "idle";
    case "done": return "done";
    case "failed": return "failed";
    case "merging":
    case "retrying": return "risk";
    default: return "active";
  }
};

export const colors = {
  idle: "tan",
  done: "blue",
  failed: "orange",
  risk: "pink",
  active: "purple",
};

export const step = (action) => {
  switch (action) {
    case "Start":
      return "you run a spec";
    case "AgentFailedRetry":
      return "the agent exits badly, and the run retries";
    case "AgentFailedGiveUp":
      return "the agent exits badly, and the budget is spent";
    case "AgentDoneNoGates":
      return "the agent finishes [no gates defined]";
    case "AgentDoneGates":
      return "the agent finishes";
    case "GatesPassed":
      return "the gates pass";
    case "GatesFailedRetry":
      return "a gate fails, and the run retries";
    case "GatesFailedGiveUp":
      return "a gate fails, and the budget is spent";
    case "GatesHung":
      return "a gate hangs";
    case "ChecklistTicked":
      return "the coordinator ticks the plan's boxes";
    case "ChecklistLeftOpen":
      return "a slice is left unticked";
    case "ReadVerdict":
      return "the reviewer's verdict is read";
    case "VerifyPass":
      return "the checklist and the verdict agree";
    case "VerifyFailRetry":
      return "they disagree, and the run retries";
    case "VerifyFailGiveUp":
      return "they disagree, and the budget is spent";
    case "RetrySpawned":
      return "the new agent starts";
    case "RetryBudgetSpent":
      return "no agent could be started";
    case "MergeOk":
      return "the merge lands";
    case "MergeFailed":
      return "the merge fails";
    default:
      return null;
  }
};

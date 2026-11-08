// The /goal loop as the user sees it.
export const title = "The /goal loop: what the user sees";

export const state = (v) => {
  if (v.status === "none") return "No goal";
  if (v.status === "complete") return "Goal complete";
  if (v.status === "paused") return "Goal paused";
  if (v.canceled) return "Goal stopped";
  return v.running ? "Working on a cycle" : "Goal active, idle";
};

export const group = (v) => {
  if (v.status === "none") return "idle";
  if (v.status === "complete") return "done";
  if (v.status === "paused" || v.canceled) return "stopped";
  return "running";
};

export const colors = {
  idle: "tan",
  done: "blue",
  stopped: "orange",
  running: "purple",
};

export const step = (action) => {
  switch (action) {
    case "SetGoalIdle":
      return "you set a goal";
    case "SetGoalBusy":
      return "you set a goal while a turn ran";
    case "Pause":
      return "you pause the goal";
    case "Stop":
      return "you press Esc";
    case "ResumeRenew":
      return "you resume the goal";
    case "ResumeClose":
      return "you resume a goal that was already met";
    case "ResumeStopped":
      return "you resume, and the budget is spent";
    case "Clear":
      return "you clear the goal";
    case "TurnEndQueued":
      return "a turn ends, and a queued prompt runs";
    case "TurnEndRefuse":
      return "a turn ends, and the loop is not armed";
    case "TurnEndSteer":
      return "a turn ends after you replaced its prompt";
    case "TurnEndNoCycle":
      return "a turn ends that the loop did not start";
    case "BeginCycle":
      return "a cycle ends and the loop decides";
    case "ToolComplete":
      return "the agent says the goal is met";
    case "Steer":
      return "you type a new prompt mid-turn";
    case "ManualTurn":
      return "you send an ordinary prompt";
    default:
      return null;
  }
};

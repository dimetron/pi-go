export const title = "A pi session, from launch to exit";

export const state = (v) => {
  if (v.phase === "starting") return "Starting up";
  if (v.phase === "closed") return "Session over";
  if (v.phase === "quitting") return "Shutting down";
  if (v.phase === "canceling") return "Stopping the turn";
  if (v.queued) return "Working, and a prompt is queued";
  if (v.steering) return "Working, on your replacement prompt";
  if (v.goalArmed) return "Working, on a goal cycle";
  return v.phase === "idle" ? "Waiting for you" : "Working on your prompt";
};

export const group = (v) => {
  if (v.phase === "starting") return "startup";
  if (v.phase === "closed" || v.phase === "quitting") return "end";
  if (v.phase === "canceling") return "stopping";
  return v.phase === "idle" ? "idle" : "busy";
};

export const colors = {
  startup: "tan",
  end: "orange",
  stopping: "pink",
  idle: "blue",
  busy: "purple",
};

export const step = (action) => {
  switch (action) {
    case "InitProgress":
      return "a startup step finishes";
    case "InitDone":
      return "startup completes";
    case "InitFailed":
      return "startup fails";
    case "SubmitIdle":
      return "you send a prompt";
    case "SubmitSteers":
      return "you send a prompt while it works";
    case "QueueAnother":
      return "you queue another prompt";
    case "TurnEndQueued":
      return "the turn ends, and your queued prompt runs";
    case "TurnEndIdle":
      return "the turn ends";
    case "TurnEndGoalCycle":
      return "the turn ends, and the goal loop continues";
    case "EscBusy":
      return "you press Esc";
    case "EscIdle":
      return "you press Esc [nothing is running]";
    case "CtrlCBusyFirst":
      return "you press Ctrl+C while it works";
    case "CtrlCBusyAgain":
      return "you press Ctrl+C again while it works";
    case "CtrlCIdleFirst":
      return "you press Ctrl+C";
    case "CtrlCIdleSecond":
      return "you press Ctrl+C again within 2s";
    case "CtrlCTimerFires":
      return "2s pass";
    case "CancelSettlesToIdle":
      return "the turn stops";
    case "CancelSettlesToQueued":
      return "the turn stops, and your queued prompt runs";
    case "GoalSetIdle":
      return "you set a goal";
    case "GoalStop":
      return "you stop the goal";
    case "Quit":
      return "pi exits";
    case "QuitIdle":
      return "you quit";
    default:
      return null;
  }
};

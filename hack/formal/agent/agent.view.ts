export const title = "One subagent, from spawn to verdict";

export const state = (v) => {
  if (!v.registered) return "Not started";
  const done = v.published !== "";
  if (v.cancelAsked) {
    return done ? "Stopped, verdict delivered" : "Stopped, verdict pending";
  }
  if (v.status === "running") return "Running";
  if (!v.eventsOpen && !done) return "Finishing";
  return done ? "Finished, verdict delivered" : "Finishing";
};

export const group = (v) => {
  if (!v.registered) return "idle";
  if (v.cancelAsked) return "stopped";
  if (v.published !== "") return "done";
  return "live";
};

export const colors = {
  idle: "tan",
  done: "blue",
  stopped: "orange",
  live: "purple",
};

export const step = (action) => {
  switch (action) {
    case "SlotAcquired":
      return "a slot frees up and the agent starts";
    case "SpawnRefused":
      return "the agent is refused [after shutdown]";
    case "ProcExits":
      return "the process exits";
    case "Cancel":
      return "you cancel it";
    case "ShutdownRunning":
      return "pi shuts down while it runs";
    case "ShutdownIdle":
      return "pi shuts down";
    case "PublishKept":
      return "its verdict is delivered";
    case "PublishEvicted":
      return "its verdict is delivered [and it is dropped from the list]";
    default:
      return null;
  }
};

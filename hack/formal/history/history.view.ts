export const title = "A session's history, and the three ways it is cut";

export const state = (v) => {
  // A mid-turn rewrite is its own state, never merged into a healthy one: the
  // whole point of the chart is to show where NoRewriteMidTurn breaks.
  if (v.midTurnWrote) return "History thrown away while a turn ran";
  const turn = v.phase === "busy" ? "a turn is running" : "between turns";
  const len = v.ev.length;
  if (len === 0) return `No history, ${turn}`;
  const cut = v.cut;
  if (cut >= len) return `History compacted, ${turn}`;
  // An odd cut starts the surviving tail on a result whose call is gone.
  if (cut % 2 === 1) return `History cut between a call and its result, ${turn}`;
  if (cut > 0) return `History partly compacted, ${turn}`;
  return `History kept, ${turn}`;
};

export const group = (v) => {
  if (v.midTurnWrote) return "broken";
  if (v.phase === "busy") return "live";
  return v.cut > 0 ? "rewritten" : "kept";
};

export const colors = {
  kept: "tan",
  live: "purple",
  rewritten: "blue",
  broken: "orange",
};

export const step = (action) => {
  switch (action) {
    case "RunTurn":
      return "a turn runs and its tool call is answered";
    case "BeginTurn":
      return "you send a prompt";
    case "FinishTurn":
      return "the turn ends";
    case "AutoCompacts":
      return "space is short, so older history is summarized [between turns]";
    case "SlashCompact":
      return "you run /compact";
    case "GaugeClearsMidTurn":
      return "you click the context gauge's [x] while it works";
    default:
      return null;
  }
};

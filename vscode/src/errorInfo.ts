export interface PiGoLaunchConfig {
  command: string;
  args: string[];
  cwd: string;
}

export interface PiGoErrorInfo {
  title: string;
  detail: string;
  steps: string[];
  raw: string;
}

function messageOf(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "object" && error !== null && "message" in error) {
    return String((error as { message: unknown }).message);
  }
  return String(error);
}

function codeOf(error: unknown): string | undefined {
  if (typeof error !== "object" || error === null || !("code" in error)) return undefined;
  return String((error as { code: unknown }).code);
}

function safeLaunch(command: string, args: readonly string[]): string {
  const safe: string[] = [];
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (/^(?:--?(?:api[-_]?key|token|secret|password|authorization))$/i.test(arg)) {
      safe.push(arg, "[redacted]");
      if (i + 1 < args.length) i++;
      continue;
    }
    if (/^(?:api[-_]?key|token|secret|password|authorization|header)=/i.test(arg)) {
      safe.push(`${arg.slice(0, arg.indexOf("="))}=[redacted]`);
      continue;
    }
    safe.push(arg);
  }
  return `${command} ${safe.join(" ")}`.trim();
}

export function explainPiGoError(error: unknown, config: PiGoLaunchConfig): PiGoErrorInfo {
  const raw = messageOf(error);
  const lower = raw.toLowerCase();
  const code = codeOf(error);
  const launch = safeLaunch(config.command, config.args);
  const detail = `${raw}\nLaunch: ${launch}\nWorkspace: ${config.cwd}`;

  if (code === "ENOENT" || lower.includes("enoent") || /spawn .*not found|executable not found/.test(lower)) {
    return {
      title: "Pi-Go could not start",
      detail,
      raw,
      steps: [
        `Set pi-go.command to an absolute path for pi (for example /Users/you/go/bin/pi).`,
        `Keep pi-go.args as ["acp-server"], then reload the VS Code window.`,
        `Run “which pi” in a terminal to find the executable path.`,
      ],
    };
  }

  if (code === "EACCES" || lower.includes("permission denied")) {
    return {
      title: "Pi-Go could not execute the agent",
      detail,
      raw,
      steps: [
        `Check that pi-go.command points to an executable file.`,
        `Run chmod +x on the pi binary if it is not executable.`,
        `Reload VS Code after correcting the setting.`,
      ],
    };
  }

  if (lower.includes("internal error")) {
    return {
      title: "Pi-Go agent reported an internal error",
      detail,
      raw,
      steps: [
        `Run the Ping icon to check the currently configured provider and model.`,
        `If you recently changed ~/.pi-go/config.json, start a new session; existing sessions keep their previous model.`,
        `Open the Pi-Go output log for the ACP server error details.`,
      ],
    };
  }

  if (lower.includes("429") || lower.includes("rate limit") || lower.includes("usage limit")) {
    return {
      title: "Pi-Go reached the provider limit",
      detail,
      raw,
      steps: [
        `Check the configured model provider quota and wait for it to reset, or add provider credits.`,
        `Switch the default role in ~/.pi-go/config.json to a provider/model with available capacity.`,
        `Run pi ping in a terminal to verify the selected provider before retrying.`,
        `If pi is not on PATH, set pi-go.command to its absolute executable path in VS Code settings.`,
      ],
    };
  }

  if (lower.includes("401") || lower.includes("403") || lower.includes("api key") || lower.includes("unauthorized")) {
    return {
      title: "Pi-Go could not authenticate with the model provider",
      detail,
      raw,
      steps: [
        `Check the provider credentials used by pi and the default role in ~/.pi-go/config.json.`,
        `If VS Code was opened from the Dock, reload it after making credentials available to the extension host.`,
        `Run pi ping in a terminal to verify authentication.`,
      ],
    };
  }

  if (lower.includes("connection refused") || lower.includes("connect: cannot")) {
    return {
      title: "Pi-Go could not reach the model service",
      detail,
      raw,
      steps: [
        `Start the configured local service or agentgateway endpoint.`,
        `Check the provider URL in ~/.pi-go/config.json and related environment settings.`,
        `Run pi ping in a terminal to verify connectivity.`,
      ],
    };
  }

  return {
    title: "Pi-Go could not complete the request",
    detail,
    raw,
    steps: [
      `Check the Pi-Go output log for the ACP server details.`,
      `Run pi ping in a terminal to verify the configured model provider.`,
      `Check pi-go.command and pi-go.args in VS Code settings.`,
    ],
  };
}

export function renderPiGoErrorMarkdown(info: PiGoErrorInfo): string {
  const quoted = info.detail
    .split("\n")
    .map((line) => `> ${line}`)
    .join("\n");
  const steps = info.steps.map((step) => `- ${step}`).join("\n");
  return `\n\n### ${info.title}\n\n${quoted}\n\n**What to do**\n\n${steps}\n`;
}

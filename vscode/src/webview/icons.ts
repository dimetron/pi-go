// Small, local SVGs keep the webview independent of icon fonts and network assets.
const paths = {
  plus: '<path d="M12 5v14M5 12h14"/>',
  history: '<circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>',
  ping: '<circle cx="12" cy="12" r="3"/><path d="M5.64 5.64a9 9 0 0 0 0 12.72M18.36 5.64a9 9 0 0 1 0 12.72"/>',
  newChat: '<path d="M21 11.5a9 9 0 0 1-9 9H4l-2 2v-10a9 9 0 1 1 19-1Z"/><path d="M12 7v9M7.5 11.5h9"/>',
  arrow: '<path d="m5 11 7-7 7 7M12 4v16"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor" stroke="none"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
  command: '<rect x="4" y="3" width="16" height="18" rx="3"/><path d="m14 7-4 10"/>',
  code: '<path d="m8 7-5 5 5 5M16 7l5 5-5 5m-3-13-2 20"/>',
};

export function icon(name: keyof typeof paths): SVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.6");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.innerHTML = paths[name];
  return svg;
}

export function iconButton(name: keyof typeof paths, label: string, action: () => void): HTMLButtonElement {
  const button = document.createElement("button");
  button.className = "icon-button";
  button.type = "button";
  button.title = label;
  button.setAttribute("aria-label", label);
  button.append(icon(name));
  button.addEventListener("click", action);
  return button;
}

/** Pi-Go's blue gopher, with a pi mark on its belly. */
export function gopher(): SVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 80 88");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("gopher");
  svg.innerHTML = `
    <g fill="#00add8" stroke="#087e9e" stroke-width="1.5">
      <circle cx="16" cy="18" r="9"/><circle cx="64" cy="18" r="9"/>
      <path d="M13 43C5 40 3 47 8 53l8 3m51-13c8-3 10 4 5 10l-8 3"/>
      <path d="m24 72-5 10q6 6 14-1m23-9 5 10q-6 6-14-1"/>
      <path d="M14 32C14 12 25 6 40 6s26 6 26 26v28c0 16-10 21-26 21S14 76 14 60Z"/>
    </g>
    <g fill="#fff"><ellipse cx="28" cy="28" rx="11" ry="12"/><ellipse cx="52" cy="28" rx="11" ry="12"/></g>
    <g fill="#163845"><circle cx="31" cy="29" r="3.5"/><circle cx="49" cy="29" r="3.5"/></g>
    <path d="M35 39h10v10q-5 5-10 0Z" fill="#fff" stroke="#087e9e" stroke-width="1.5"/>
    <path d="M40 42v9" stroke="#087e9e"/>
    <ellipse cx="40" cy="38" rx="7" ry="5" fill="#e7c7a3"/>
    <ellipse cx="40" cy="36" rx="4" ry="2.5" fill="#163845"/>
    <path d="M29 59h23M34 59l-2 13m14-13v10q0 4 5 2" fill="none" stroke="#fff" stroke-width="4" stroke-linecap="round"/>
    <text x="40" y="72" text-anchor="middle" font-size="13" font-family="Georgia,serif" font-weight="700" fill="#087e9e">π</text>
  `;
  return svg;
}

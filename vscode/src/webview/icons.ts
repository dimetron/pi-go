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
  clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5v5l3.3 1.8"/>',
  loader: '<path d="M12 3.5a8.5 8.5 0 1 1-6.01 2.49"/>',
  check: '<path d="M5 12.5 9.5 17 19 7"/>',
  alert: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5v5.5M12 16.5h.01"/>',
  copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="1.6"/><path d="M15 8.5V6a1.6 1.6 0 0 0-1.6-1.6H6A1.6 1.6 0 0 0 4.4 6v7.4A1.6 1.6 0 0 0 6 15h2.5"/>',
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

/**
 * The pi-go panda mascot (media/pi-go-mascot.png, from the Pi-Go design
 * system). The host passes its webview URI on body[data-mascot]; without one
 * the frame still renders, showing the π mark instead of the image.
 */
export function mascot(src: string | undefined): HTMLElement {
  const frame = document.createElement("div");
  frame.className = "mascot";
  frame.setAttribute("aria-hidden", "true");
  if (src) {
    const img = document.createElement("img");
    img.src = src;
    img.alt = "";
    frame.append(img);
  } else {
    frame.textContent = "π";
  }
  return frame;
}

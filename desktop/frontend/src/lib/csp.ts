// The app's Content-Security-Policy (vite.config.ts puts it in index.html). The page reaches only
// the app itself: the Wails runtime calls Go at its own origin (/wails/runtime), pictures are the
// app's or data:/blob:. What an agent writes cannot load from the web when shown (a picture in an
// answer would tell a server the chat was read) nor send anything out.
//
// Inline scripts: the app has none; an artifact page the person lets run (Artifacts, "Allow
// scripts") is a srcdoc frame, which takes this policy, so its scripts need it — and they still
// reach nothing outside. Markdown is sanitized (DOMPurify) before it is shown: no script in it.

/** The policy; dev adds the Vite dev server's live-reload socket. */
export function csp(dev = false, devPort = 9245): string {
  const connect = ["'self'", "wails:"];
  if (dev) connect.push(`ws://127.0.0.1:${devPort}`, `http://127.0.0.1:${devPort}`);
  return [
    "default-src 'self' wails:",
    "script-src 'self' 'unsafe-inline'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "media-src 'self' data: blob:",
    "frame-src 'self' blob:",
    `connect-src ${connect.join(" ")}`,
    "object-src 'self' blob:", // a PDF in a frame is a plugin document: WebKit may check it here
    "base-uri 'none'",
    "form-action 'none'",
  ].join("; ");
}

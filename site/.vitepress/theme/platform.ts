// One platform choice for the whole site. The home page's tabs and every code
// group in the manual whose tabs carry a platform name (Web, Android, 服务端,
// 宿主) follow it: pick Android once and every page shows Android first.

const KEY = "talkwire-platform";
export const PLATFORMS = ["Web", "Android", "服务端", "宿主"] as const;
export type Platform = (typeof PLATFORMS)[number];

export function readPlatform(): Platform | null {
  try {
    const v = localStorage.getItem(KEY);
    return (PLATFORMS as readonly string[]).includes(v ?? "") ? (v as Platform) : null;
  } catch {
    return null; // storage blocked: the choice simply is not remembered
  }
}

export function savePlatform(p: Platform): void {
  try {
    localStorage.setItem(KEY, p);
  } catch {
    // storage blocked: nothing to do
  }
}

/** Selects the remembered platform in every code group on the page that has it. */
export function applyPlatform(): void {
  const p = readPlatform();
  if (!p) return;
  for (const group of document.querySelectorAll<HTMLElement>(".vp-code-group")) {
    for (const label of group.querySelectorAll<HTMLLabelElement>(".tabs label")) {
      if (label.textContent?.trim() !== p) continue;
      const input = label.htmlFor ? document.getElementById(label.htmlFor) : null;
      if (input instanceof HTMLInputElement && !input.checked) input.click();
    }
  }
}

/** Remembers the platform whenever a reader picks one in a code group. */
export function rememberPlatform(): void {
  document.addEventListener("click", (e) => {
    const label = (e.target as Element | null)?.closest?.(".vp-code-group .tabs label");
    const text = label?.textContent?.trim();
    if (text && (PLATFORMS as readonly string[]).includes(text)) {
      savePlatform(text as Platform);
      requestAnimationFrame(() => applyPlatform());
    }
  });
}

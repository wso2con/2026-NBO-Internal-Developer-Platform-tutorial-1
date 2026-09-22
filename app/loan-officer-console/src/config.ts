// config.ts — runtime configuration (BUILD-SPEC.md §7.5).
//
// The API base URL is read at RUNTIME from /config.js, which the container writes
// at startup from $API_BASE_URL. It is deliberately NOT a VITE_ variable: those
// are substituted at build time and baked into the bundle, and the same image is
// promoted through three environments with three different URLs.
//
// Changing API_BASE_URL and restarting the container must be enough. No rebuild.

declare global {
  interface Window {
    __KIFARU_CONFIG__?: { apiBaseUrl?: string; brandColor?: string }
  }
}

export function apiBaseUrl(): string {
  const configured = window.__KIFARU_CONFIG__?.apiBaseUrl
  if (configured && configured.trim() !== '') {
    return configured.replace(/\/+$/, '')
  }
  // Same-origin fallback, so `npm run dev` works via the Vite proxy without a
  // config.js present. In a container /config.js is always served.
  return ''
}

// Applies $BRAND_COLOR to the brand custom properties, if the container was given
// one. Same mechanism as apiBaseUrl: runtime, never baked in, so ONE image serves
// both the retail and the SME deployments in different colours.
//
// Anything that does not look like a hex colour is ignored rather than written
// into the DOM.
const HEX = /^#[0-9a-fA-F]{6}$/

export function applyBrandColor(): void {
  const colour = window.__KIFARU_CONFIG__?.brandColor?.trim()
  if (!colour || !HEX.test(colour)) return
  const root = document.documentElement
  root.style.setProperty('--navy', colour)
  root.style.setProperty('--navy-light', lighten(colour, 0.18))
}

// Mixes the colour towards white, for the lighter of the two brand variables.
function lighten(hex: string, amount: number): string {
  const n = parseInt(hex.slice(1), 16)
  const mix = (c: number) => Math.round(c + (255 - c) * amount)
  const r = mix((n >> 16) & 255)
  const g = mix((n >> 8) & 255)
  const b = mix(n & 255)
  return `#${((r << 16) | (g << 8) | b).toString(16).padStart(6, '0')}`
}

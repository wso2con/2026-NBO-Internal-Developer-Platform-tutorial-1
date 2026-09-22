// Logo.tsx — the Kifaru Bank mark.
//
// "Kifaru" is Swahili for rhinoceros, so the mark is a rhino in profile: barrel
// body, head carried low, horn raking up from the nose.
//
// Drawn as a single path with fill="currentColor", so it inherits the colour of
// whatever it sits on — navy on the white page, white on the navy masthead — and
// needs no second asset for the inverted case. The eye is a hole punched with
// fill-rule="evenodd" rather than a second shape, for the same reason.
//
// It is a full-body silhouette, which reads clearly from about 32px up. Below
// that the legs and horn merge; if a favicon-sized mark is ever needed it should
// be a separate, simpler drawing rather than this one scaled down.

export default function Logo({ size = 40 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      fill="currentColor"
      role="img"
      aria-label="Kifaru Bank"
      focusable="false"
    >
      <path
        fillRule="evenodd"
        d="M 5,32
           C 5,23 11,17 21,17
           C 29,16 35,18 39,23
           L 43,29
           L 46,27 L 48,30
           L 52,31
           L 57,15
           L 62,34
           L 63,41
           L 56,45
           L 47,44
           L 42,38
           L 40,34
           L 40,49 L 33,49 L 33,36
           L 22,36
           L 22,49 L 15,49 L 15,34
           C 11,34 8,34 5,32 Z
           M 5,29 L 0,31 L 4,35 Z
           M 49,34 a 1.9,1.9 0 1,0 0.01,0 Z"
      />
    </svg>
  )
}

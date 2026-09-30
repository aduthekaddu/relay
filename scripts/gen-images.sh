#!/usr/bin/env bash
# Regenerate the source photographs for the marketing site.
#
# The site never shows these raw: site/scripts/dither.mjs turns each one
# into the house dot/dither duotone (carbon + bone, orange highlight) and
# writes AVIF/WebP at 1x/2x into site/src/assets/. The originals are large
# and stay out of git.
#
#   scripts/gen-images.sh              # all images, one at a time (~40 s each)
#   scripts/gen-images.sh tower desk   # only these
#   OUT=/tmp/relay-img scripts/gen-images.sh
#
# Requires the `codex` CLI signed in with image generation available.
# Afterwards: (cd site && pnpm dither)
set -euo pipefail

OUT="${OUT:-/tmp/relay-img}"
mkdir -p "$OUT"

# Shared art direction, appended to every subject.
STYLE="Photograph, night, long exposure, cinematic, very dark and minimal, \
one single warm orange light source, deep blacks, lots of empty negative \
space, subtle film grain, shot on a full-frame camera with a 35mm lens. \
Absolutely no text, no letters, no numbers, no logos, no watermarks, no people's faces. \
Landscape 3:2 composition."

declare -A PROMPTS=(
  [tower]="A lone steel lattice radio relay tower on a bare hill at night, \
seen from below and far away, a single small orange aviation beacon glowing at its tip, \
the rest of the sky almost black with faint stars, the tower in the right third of the frame."
  [train]="Close-up of two hands holding a smartphone inside a dark night train carriage, \
the phone screen is the only warm light and lights the fingertips, the window behind \
shows blurred streaks of distant lights, the screen content is not readable."
  [desk]="A dark home office at 3 a.m.: a closed sleeping laptop on a wooden desk and one \
lit external monitor glowing warm orange, a chair pushed in, the rest of the room in \
darkness, seen from the doorway, the desk in the lower left of the frame."
  [rack]="Macro detail of a server rack in a dark room, rows of tiny status LEDs, \
exactly one of them glowing warm orange while the others are dim, shallow depth of field, \
most of the frame in soft black shadow."
  [road]="A narrow road winding through dark rolling hills at night, seen from high above, \
the long-exposure light trail of a single car drawing one thin warm orange line \
through the valley, everything else nearly black."
  [windows]="A tall apartment building facade at 3 a.m. photographed straight on, \
a strict grid of dark windows, exactly one window lit warm orange near the centre, \
the building fills the frame edge to edge, flat perspective, no sky."
)
ORDER=(tower train desk rack road windows)

want=("$@")
[ ${#want[@]} -eq 0 ] && want=("${ORDER[@]}")

cd "$OUT"
for name in "${want[@]}"; do
  subject="${PROMPTS[$name]:-}"
  if [ -z "$subject" ]; then
    echo "unknown image: $name (known: ${ORDER[*]})" >&2
    exit 2
  fi
  if [ -s "$name.png" ] && [ -z "${FORCE:-}" ]; then
    echo "skip $name (exists; FORCE=1 to regenerate)"
    continue
  fi
  echo "generating $name…"
  codex exec --skip-git-repo-check -s workspace-write \
    "Use your image generation tool to create this image: $subject $STYLE Save it as $name.png in the current directory. Do not create any other files." \
    >"$OUT/$name.log" 2>&1 || echo "codex failed for $name (see $OUT/$name.log)" >&2
  [ -s "$name.png" ] && echo "ok $name" || echo "missing $name.png" >&2
done

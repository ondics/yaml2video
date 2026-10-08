#!/bin/sh
# Generate clearly labeled, synthetic media for the v2 examples.
set -eu

cd "$(dirname "$0")"
command -v ffmpeg >/dev/null 2>&1 || { echo 'ffmpeg is required' >&2; exit 1; }
# Keep fontconfig's cache writable in restricted development environments.
export XDG_CACHE_HOME="${TMPDIR:-/tmp}/yaml2video-font-cache"
mkdir -p "$XDG_CACHE_HOME"

image() {
    file=$1
    label=$2
    color=$3
    mkdir -p "$(dirname "$file")"
    ffmpeg -hide_banner -loglevel error -y -f lavfi -i "color=c=$color:s=640x640:d=0.1" \
        -vf "drawbox=x=32:y=32:w=576:h=576:color=white@0.12:t=fill,drawbox=x=55:y=80:w=530:h=12:color=white@0.8:t=fill,drawbox=x=55:y=490:w=530:h=12:color=white@0.8:t=fill,drawtext=font=Sans:text='$label':fontcolor=white:fontsize=38:x=(w-text_w)/2:y=(h-text_h)/2" \
        -frames:v 1 -update 1 "$file"
}
logo() {
    file=$1
    mkdir -p "$(dirname "$file")"
    ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'color=c=0x134E4A:s=480x160:d=0.1' \
        -vf "drawbox=x=14:y=14:w=452:h=132:color=white@0.2:t=4,drawtext=font=Sans:text='MOCK LOGO':fontcolor=white:fontsize=42:x=(w-text_w)/2:y=(h-text_h)/2" \
        -frames:v 1 -update 1 "$file"
}
music() {
    file=$1
    frequency=$2
    mkdir -p "$(dirname "$file")"
    ffmpeg -hide_banner -loglevel error -y -f lavfi -i "sine=frequency=$frequency:duration=8:sample_rate=44100" \
        -af 'volume=0.12,afade=t=in:st=0:d=0.2,afade=t=out:st=7.5:d=0.5' -q:a 8 "$file"
}

# Generate each example independently so its YAML works without shared paths.
for kind in simple normal complex; do
    logo "$kind/assets/gesund-am-arbeitsplatz-logo.png"
    image "$kind/assets/schulterkreisen.png" 'SHOULDERS - MOCK' 0x0F766E
    image "$kind/assets/beine-strecken.png" 'LEGS - MOCK' 0x1D4ED8
done
for kind in simple complex; do
    image "$kind/assets/nacken-seitlich.png" 'NECK - MOCK' 0x7C3AED
done
image normal/assets/intro-schreibtisch.png 'DESK - MOCK' 0x0369A1
image normal/assets/brust-oeffnen.png 'CHEST - MOCK' 0xBE185D
image normal/assets/handgelenke.png 'WRISTS - MOCK' 0xB45309
image complex/assets/heller-arbeitsplatz.png 'OFFICE - MOCK' 0x0369A1
image complex/assets/tief-atmen.png 'BREATHE - MOCK' 0x0F766E
image complex/assets/oberkoerper-drehen.png 'BACK - MOCK' 0xBE185D
image complex/assets/finger-strecken.png 'HANDS - MOCK' 0xB45309
image complex/assets/entspannter-arbeitsplatz.png 'RELAX - MOCK' 0x1D4ED8

music simple/assets/ruhiger-beat.mp3 220
music normal/assets/leichter-optimistischer-beat.mp3 330
music complex/assets/motivierende-pulsierende-musik.mp3 440
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'sine=frequency=740:duration=0.25:sample_rate=44100' \
    -af 'volume=0.25,afade=t=out:st=0.1:d=0.15' complex/assets/soft-pop.wav
# Audible tone standing in for narration, not a spoken recording.
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'sine=frequency=280:duration=3:sample_rate=44100' \
    -af 'volume=0.08,afade=t=in:st=0:d=0.2,afade=t=out:st=2.7:d=0.3' complex/assets/ruhige-anleitung-haende.wav

echo 'Generated mock media under simple/assets, normal/assets, and complex/assets.'

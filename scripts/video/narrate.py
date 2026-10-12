#!/usr/bin/env python3
"""Add a spoken narration track to a recording, using a local VoiceStudio (or any
OpenAI-compatible) text-to-speech server.

    python3 scripts/video/narrate.py docs/showcase/v2/forge-custody-fintech.mp4 \
        --cues docs/showcase/v2/forge-custody-fintech.cues.json \
        --model voxcpm2 --voice "Narrator"

The recorder (infrastructure/local/e2e/record-v2.js) writes <name>.cues.json next to each
video: every caption, title card, diagram, code stage and chat line, with the second it
appeared. This tool asks the TTS server to speak each cue, places each clip at its cue time,
and writes:

    <name>-narrated.mp4   the video with the narration as its audio track
    <name>.srt            subtitles timed to where each line is actually spoken
    <name>-narration.wav  the narration on its own

When a line runs longer than the gap before the next cue, it is first sped up (to at most
--max-speed); anything still too long pushes later lines back. The report at the end says how
far the narration drifted from the picture, so you can re-record a scene with a longer pause
or shorten the text. --dry-run does all of that with an estimated speaking rate and no server,
to check the fit before generating anything.

Needs only the Python standard library and ffmpeg (on PATH, --ffmpeg, or the imageio-ffmpeg
package). Server: VoiceStudio's POST /v1/audio/speech on http://127.0.0.1:3900 by default;
set VOICESTUDIO_API_KEY if the server requires a key.

Licence note: VoiceStudio's default OmniVoice weights are CC BY-NC (non-commercial). For
marketing material use an engine whose weights allow commercial use (for example VoxCPM2,
CosyVoice 3 or Kokoro, Apache-2.0 on their model cards) and check the card yourself.
"""
import argparse
import array
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
import wave

RATE = 24000  # every clip is normalised to 24 kHz mono 16-bit before mixing
KINDS = ("caption", "card", "diagram", "stage", "chat")


def find_ffmpeg(explicit):
    if explicit:
        return explicit
    if shutil.which("ffmpeg"):
        return "ffmpeg"
    try:
        import imageio_ffmpeg  # type: ignore
        return imageio_ffmpeg.get_ffmpeg_exe()
    except ImportError:
        sys.exit("ffmpeg not found: install it, pass --ffmpeg, or `pip install imageio-ffmpeg`")


def run(cmd, data=None):
    r = subprocess.run(cmd, input=data, capture_output=True)
    if r.returncode != 0:
        sys.exit("command failed: %s\n%s" % (" ".join(cmd[:4]), r.stderr.decode(errors="replace")[-2000:]))
    return r.stdout


def media_duration(ffmpeg, path):
    # ffmpeg prints "Duration: HH:MM:SS.xx" on stderr for -i with no output.
    r = subprocess.run([ffmpeg, "-hide_banner", "-i", path], capture_output=True)
    m = re.search(r"Duration: (\d+):(\d+):(\d+\.\d+)", r.stderr.decode(errors="replace"))
    if not m:
        sys.exit("cannot read the duration of " + path)
    h, mi, s = m.groups()
    return int(h) * 3600 + int(mi) * 60 + float(s)


def to_pcm(ffmpeg, audio_bytes):
    """Any audio the server returns -> 24 kHz mono signed 16-bit samples."""
    raw = run([ffmpeg, "-hide_banner", "-loglevel", "error", "-i", "pipe:0",
               "-ac", "1", "-ar", str(RATE), "-f", "s16le", "pipe:1"], data=audio_bytes)
    a = array.array("h")
    a.frombytes(raw[: len(raw) - len(raw) % 2])
    return a


def trim_silence(a, threshold=300):
    """Engines often pad clips with silence; trimming it keeps lines from drifting."""
    start = 0
    while start < len(a) and abs(a[start]) < threshold:
        start += 1
    end = len(a)
    while end > start and abs(a[end - 1]) < threshold:
        end -= 1
    pad = int(0.05 * RATE)
    return a[max(0, start - pad): min(len(a), end + pad)]


def speak(args, text, voice, speed, cache_dir):
    body = {"model": args.model, "input": text, "voice": voice, "response_format": "wav", "speed": round(speed, 2)}
    if args.language:
        body["language"] = args.language
    if args.instructions:
        body["instructions"] = args.instructions
    key = hashlib.sha256(json.dumps([args.url, body], sort_keys=True).encode()).hexdigest()[:24]
    cached = os.path.join(cache_dir, key + ".wav")
    if os.path.exists(cached):
        with open(cached, "rb") as f:
            return f.read()
    req = urllib.request.Request(args.url.rstrip("/") + "/v1/audio/speech", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    token = os.environ.get("VOICESTUDIO_API_KEY")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=args.timeout) as r:
            data = r.read()
    except urllib.error.HTTPError as e:
        sys.exit("TTS server refused %r: HTTP %s %s" % (text[:60], e.code, e.read().decode(errors="replace")[:500]))
    except urllib.error.URLError as e:
        sys.exit("cannot reach the TTS server at %s (%s). Is VoiceStudio running?" % (args.url, e.reason))
    with open(cached, "wb") as f:
        f.write(data)
    return data


def estimate(text, speed, wpm):
    """Dry run: silence as long as the line would take to say at `wpm` words a minute."""
    words = max(1, len(text.split()))
    return array.array("h", bytes(2 * int(RATE * words * 60.0 / wpm / speed)))


def clean(text):
    # Read code-ish tokens and symbols in a way a voice handles.
    t = text.replace("→", " to ").replace("·", ",").replace("…", "...")
    t = re.sub(r"\bofb_agent_\S*", "the agent key", t)
    t = re.sub(r"\b0x[0-9a-fA-F]{6,}\b", "the address", t)
    return re.sub(r"\s+", " ", t).strip()


def srt_time(s):
    ms = int(round(s * 1000))
    return "%02d:%02d:%02d,%03d" % (ms // 3600000, ms // 60000 % 60, ms // 1000 % 60, ms % 1000)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("video")
    ap.add_argument("--cues", help="cue file (default: <video without -narrated>.cues.json)")
    ap.add_argument("--url", default=os.environ.get("VOICESTUDIO_URL", "http://127.0.0.1:3900"))
    ap.add_argument("--model", default="tts-1", help="engine id, e.g. voxcpm2, cosyvoice, kittentts; tts-1 = the active engine")
    ap.add_argument("--voice", default="default", help="narrator: a VoiceStudio voice profile name or id")
    ap.add_argument("--agent-voice", help="voice for the AI agent's chat lines (default: --voice)")
    ap.add_argument("--user-voice", help="voice for the user's chat lines (default: --voice)")
    ap.add_argument("--language", default="en")
    ap.add_argument("--instructions", help="style instruction passed to the engine, e.g. 'calm, clear, measured'")
    ap.add_argument("--kinds", default=",".join(KINDS), help="which cue kinds to speak (default: all)")
    ap.add_argument("--speed", type=float, default=1.0)
    ap.add_argument("--max-speed", type=float, default=1.2, help="speed a line up to this before letting it push later lines")
    ap.add_argument("--gap", type=float, default=0.25, help="minimum silence between lines, seconds")
    ap.add_argument("--offset", type=float, default=0.0, help="shift every cue by this many seconds")
    ap.add_argument("--volume", type=float, default=1.0, help="narration gain")
    ap.add_argument("--keep-audio", action="store_true", help="mix with the video's existing audio instead of replacing it")
    ap.add_argument("--out", help="output video (default: <video>-narrated.mp4)")
    ap.add_argument("--ffmpeg")
    ap.add_argument("--timeout", type=int, default=600)
    ap.add_argument("--dry-run", action="store_true", help="no server: estimate timing at --wpm and write the SRT and report only")
    ap.add_argument("--wpm", type=float, default=150)
    args = ap.parse_args()

    ffmpeg = find_ffmpeg(args.ffmpeg)
    base = re.sub(r"\.(mp4|webm|mov|mkv)$", "", args.video)
    cues_path = args.cues or base + ".cues.json"
    with open(cues_path) as f:
        doc = json.load(f)
    kinds = set(k.strip() for k in args.kinds.split(","))
    cues = [c for c in doc["cues"] if c.get("kind") in kinds and c.get("text")]
    cues.sort(key=lambda c: c["t"])
    if not cues:
        sys.exit("no cues to speak in " + cues_path)

    video_len = media_duration(ffmpeg, args.video)
    cache_dir = os.path.join(os.path.dirname(os.path.abspath(base)) or ".", ".narration-cache")
    os.makedirs(cache_dir, exist_ok=True)

    placed = []  # (start, samples, cue, speed)
    cursor = 0.0
    for i, c in enumerate(cues):
        text = clean(c["text"])
        voice = args.voice
        if c["kind"] == "chat":
            voice = (args.agent_voice if c.get("speaker") == "agent" else args.user_voice) or args.voice
        want = max(0.0, c["t"] + args.offset)
        start = max(want, cursor)
        nxt = cues[i + 1]["t"] + args.offset if i + 1 < len(cues) else video_len
        room = max(0.5, nxt - start - args.gap)
        speed = args.speed
        clip = estimate(text, speed, args.wpm) if args.dry_run else trim_silence(to_pcm(ffmpeg, speak(args, text, voice, speed, cache_dir)))
        if len(clip) / RATE > room and speed < args.max_speed:
            speed = min(args.max_speed, speed * (len(clip) / RATE) / room)
            clip = estimate(text, speed, args.wpm) if args.dry_run else trim_silence(to_pcm(ffmpeg, speak(args, text, voice, speed, cache_dir)))
        placed.append((start, clip, c, speed, want))
        cursor = start + len(clip) / RATE + args.gap
        print("%7.1fs %-8s %+5.1fs  x%.2f  %s" % (start, c["kind"], start - want, speed, text[:70]))

    end = max(video_len, cursor)
    track = array.array("h", bytes(2 * int(end * RATE + RATE)))
    for start, clip, _, _, _ in placed:
        o = int(start * RATE)
        for j, v in enumerate(clip):
            s = track[o + j] + int(v * args.volume)
            track[o + j] = 32767 if s > 32767 else -32768 if s < -32768 else s

    out = args.out or base + "-narrated.mp4"
    srt = re.sub(r"-narrated\.mp4$|\.mp4$", "", out) + ".srt"
    with open(srt, "w") as f:
        for n, (start, clip, c, _, _) in enumerate(placed, 1):
            f.write("%d\n%s --> %s\n%s\n\n" % (n, srt_time(start), srt_time(start + len(clip) / RATE), c["text"]))

    drift = [s - w for s, _, _, _, w in placed]
    late = [(c["t"], s - w, c["text"][:60]) for s, _, c, _, w in placed if s - w > 2.0]
    print("\n%d lines, %.0fs of speech in a %.0fs video. Max drift %.1fs; %d lines more than 2s late."
          % (len(placed), sum(len(p[1]) for p in placed) / RATE, video_len, max(drift), len(late)))
    for t, d, txt in late[:15]:
        print("  cue at %6.1fs spoken %.1fs late: %s" % (t, d, txt))
    if end > video_len + 0.5:
        print("  narration runs %.1fs past the end of the video; the last frame is held." % (end - video_len))
    print("Wrote " + srt)
    if args.dry_run:
        print("Dry run: no audio generated. Re-run without --dry-run with VoiceStudio running.")
        return

    wav_path = re.sub(r"-narrated\.mp4$|\.mp4$", "", out) + "-narration.wav"
    with wave.open(wav_path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(track[: int(end * RATE)].tobytes())

    vcodec = ["-c:v", "copy"] if args.video.endswith(".mp4") and end <= video_len + 0.5 else \
        ["-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p"]
    vfilter = ["-vf", "tpad=stop_mode=clone:stop_duration=%.2f" % (end - video_len)] if end > video_len + 0.5 else []
    if args.keep_audio:
        amap = ["-filter_complex", "[0:a][1:a]amix=inputs=2:duration=longest[a]", "-map", "0:v", "-map", "[a]"]
    else:
        amap = ["-map", "0:v", "-map", "1:a"]
    run([ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-i", args.video, "-i", wav_path]
        + amap + vfilter + vcodec + ["-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart", out])
    print("Wrote %s and %s" % (out, wav_path))


if __name__ == "__main__":
    main()

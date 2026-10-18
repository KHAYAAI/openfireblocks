# Narrating the demo videos with VoiceStudio

This guide adds a spoken voice-over to the recordings in `docs/showcase/v2/`, using
[VoiceStudio](https://github.com/debpalash/VoiceStudio), an open-source text-to-speech app that
runs on your own computer, and `scripts/video/narrate.py`.

## How it fits together

1. **The recorder writes the script and the timing.**
   - `infrastructure/local/e2e/record-v2.js` saves a cue file, `<name>.cues.json`, next to each video.
   - The cue file holds every caption, title card, diagram, code panel and chat line, with the second it appeared on screen.
2. **VoiceStudio speaks each line.**
   - It runs a local server on `http://127.0.0.1:3900` with an OpenAI-compatible endpoint, `POST /v1/audio/speech`.
   - Nothing leaves your machine.
3. **`narrate.py` builds the finished video.** It places each spoken line at its cue time, then writes three files:
   - `<name>-narrated.mp4`: the video with the voice-over as its audio track.
   - `<name>.srt`: subtitles timed to the speech.
   - `<name>-narration.wav`: the voice-over on its own, for editing elsewhere.

## 1. Install VoiceStudio (once)

**macOS or Linux:**

```sh
curl -fsSL https://voicestudio.sh/install | sh
```

**Windows (PowerShell):**

```powershell
irm https://voicestudio.sh/install | iex
```

You can also download the app from the project's
[Releases page](https://github.com/debpalash/VoiceStudio/releases/latest).

**Hardware:**
- Apple Silicon Macs and NVIDIA GPUs are fast.
- Other computers work on the CPU, just more slowly (about 5 GB free disk).
- Intel Macs run only the interface and need a remote backend.

## 2. Choose a voice model you may use commercially

> **Do not use VoiceStudio's default model for these videos.** Its default engine, OmniVoice, has
> **non-commercial** weights (CC BY-NC). VoiceStudio's own licence inventory says so:
> `backend/config/model_licenses.json` lists it as `noncommercial`. Our demo videos are marketing,
> which is commercial use.

Pick an engine whose model card says **Apache-2.0**, for example:

| Engine (`--model`) | Notes |
|---|---|
| `voxcpm2` (VoxCPM2) | Natural English. Can design a voice from a text description ("calm, warm, mid-range") |
| `cosyvoice` (CosyVoice 3) | Strong multilingual, voice cloning |
| Kokoro (`mlx-audio`, Apple Silicon) | Small and fast, with good preset voices |
| `kittentts` | Very small, runs on any CPU. Less natural |

VoiceStudio marks every model as "commercial use not yet reviewed", including these. **Read the model card before publishing.** If the card asks for attribution, add it to the video description.

In the app, open **Models** and install the engine. Then create a **voice profile**, for example
"FORGE Narrator", either by describing the voice (voice design) or by recording a reference.

**Cloning a real person's voice**, including your own or a colleague's:
- Get their written consent first. A voice is biometric personal information under POPIA.
- Only ever clone a voice you have permission to use.

## 3. Check the timing without generating audio

```sh
python3 scripts/video/narrate.py docs/showcase/v2/forge-custody-fintech.mp4 --dry-run
```

**What it does:** estimates each line at 150 words a minute and writes the `.srt` subtitle file. It
lists any lines that would be spoken more than 2 seconds after they appear on screen.

**If many lines run late, you have three options:**
- shorten the caption text in `record-v2.js` and re-record;
- give a scene longer to play out (the `ms` argument to `caption` and `card`);
- narrate fewer kinds of cue, for example `--kinds caption,card,stage`, which leaves the chat to subtitles.

## 4. Generate the narrated video

Start VoiceStudio (its backend listens on port 3900), then run:

```sh
python3 scripts/video/narrate.py docs/showcase/v2/forge-custody-fintech.mp4 \
  --model voxcpm2 --voice "FORGE Narrator" \
  --agent-voice "FORGE Agent" --user-voice "FORGE Customer" \
  --instructions "clear, calm, professional; South African English"
```

| Option | What it does |
|---|---|
| `--voice` | The narrator's voice profile (name or id) |
| `--agent-voice`, `--user-voice` | Separate voices for the AI agent and the person chatting with it. Omit both for a single voice |
| `--kinds` | Which cue kinds to speak: `caption,card,diagram,stage,chat` (the default is all of them) |
| `--max-speed` | How far a long line may be sped up to fit before the following lines are pushed later. Default 1.2 |
| `--offset` | Shifts every line by this many seconds, if the voice consistently starts too early or too late |
| `--keep-audio` | Mixes the narration with any sound the video already has, instead of replacing it |
| `--ffmpeg` | Path to ffmpeg, if it isn't on your PATH. Alternatively, `pip install imageio-ffmpeg` |

If the server needs a key, set the `VOICESTUDIO_API_KEY` environment variable. The tool also caches every spoken line in `.narration-cache/`, so re-running after you change one caption only regenerates that one line.

## 5. Check before publishing

- Watch the whole video. Make sure no line is spoken over the wrong screen. The script prints
  each line's drift.
- Upload the `.srt` file with the video, for viewers with the sound off and for accessibility.
- **Disclose the synthetic voice**, for example in the description: "Narration: AI-generated
  voice." It's honest, and some platforms require it.
- The on-screen statements stay as recorded. Real API calls are real, and the stand-ins are
  named on screen. The narration says nothing the captions don't.

## Notes

- **VoiceStudio's licence:** VoiceStudio is AGPL-3.0. Using it to produce audio files doesn't
  place those files under the AGPL. Hosting a modified copy of VoiceStudio for other people would
  bring the AGPL's obligations.
- **Why it can't run here:** VoiceStudio couldn't be run in the cloud session that built this,
  because model downloads (huggingface.co) are blocked by that environment's network policy.
  `narrate.py` was tested end to end against a stand-in server that implements the same
  `/v1/audio/speech` contract. It is not yet tested against a real VoiceStudio install.

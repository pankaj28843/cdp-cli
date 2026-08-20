#!/usr/bin/env python3
"""Generate the checked-in synthetic transcription fixture corpus.

The corpus is deliberately generated once and committed as test input. The
installed service uses one fixture on a bounded cadence for provider health
verification; this is not evasion traffic. No user audio or expected
transcript state is written by the service.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import subprocess
import tempfile
from pathlib import Path


SENTENCES = [
    "Could you open the latest project notes before the meeting starts?",
    "Please remind me to review the deployment checklist this afternoon.",
    "The small orange notebook is beside the keyboard on my desk.",
    "Which route is fastest when the bridge is closed for maintenance?",
    "I would like a quiet room with reliable internet and natural light.",
    "Can you move the dollar key closer to the beginning of the row?",
    "The backup completed successfully, but the report still needs verification.",
    "Let us compare the local result with the remote result before deciding.",
    "What time does the train leave from the central station tomorrow morning?",
    "Please save this draft without submitting the form or sending a message.",
    "The new microphone sounds clear even when the room is noisy.",
    "I forgot whether the test server is running on Linux or macOS.",
    "Could you explain why the connection was closed after the first request?",
    "The garden needs water, but the forecast says rain is coming tonight.",
    "Please check that every required field has a helpful validation message.",
    "The blue folder contains the design references for the next release.",
    "How many independent providers are ready to accept a transcription request?",
    "I want the most frequently used key to remain easy to reach.",
    "The service should recover gracefully when a browser tab disappears.",
    "Can we keep the interface simple while still exposing advanced settings?",
    "The invoice total is one hundred twenty four dollars and fifty cents.",
    "Please do not store the audio recording after the transcript is accepted.",
    "Which device has the newest build installed right now?",
    "The child process stopped, so the supervisor should report a clear reason.",
    "I need an answer that works on both the laptop and the virtual machine.",
    "The window is narrow, but the primary action must remain visible.",
    "Could you sort these favorite keys into the order I use most often?",
    "The changed host key must block the connection until I approve the replacement.",
    "A short retry with a bounded timeout is safer than an endless request.",
    "Please write the result in plain language for someone new to the project.",
    "The local model should remain available when the online provider is offline.",
    "How can we prove that a stale stream never becomes authoritative again?",
    "The tablet and phone should show the same saved preferences after syncing.",
    "I would rather see one useful error than several confusing warnings.",
    "Please leave unrelated changes untouched while you update the provider adapter.",
    "The test should fail first so that the new behavior is genuinely protected.",
    "Can you measure the disk usage before and after the cleanup?",
    "The green status indicator means the service is ready for a real request.",
    "I want the application to work even when the terminal is temporarily hidden.",
    "The remote session belongs to tmux, not to the lifetime of this connection.",
    "Please confirm that the credentials never appear in logs or screenshots.",
    "Which capability was observed most recently by the headed browser?",
    "The audio file is short enough to fit within the bounded request limit.",
    "I use the question mark often, so it should not be buried in a group.",
    "The new screen needs a clear title, a single primary action, and useful empty state.",
    "Could you retry only the failed provider while leaving the healthy one alone?",
    "The server is reachable from home, but the laptop address changes outside.",
    "Please make the default behavior safe for unattended background operation.",
    "The release build passed locally before it was copied to the test device.",
    "I want to understand the tradeoff before we add another dependency.",
    "The authentication session is valid, but the capability evidence is outdated.",
    "Can the service refresh both pieces of evidence without opening a browser per request?",
    "The response should include a stable provider-neutral schema for the client.",
    "Please keep the retry budget small enough to protect battery and bandwidth.",
    "The first sentence is a statement, and the next one asks a question.",
    "I would like one fixture with a comma, a dash, and a colon.",
    "The number forty two should remain understandable when spoken aloud.",
    "Please check whether the WebM file has a valid container header.",
    "The WebM file should use a mono sixteen kilohertz Opus stream.",
    "Could a capability refresh failure make the provider report not ready?",
    "The health check should distinguish missing evidence from expired evidence.",
    "I prefer a direct request when the persisted owner-only template is fresh.",
    "The headed browser is a repair boundary, not the normal transcription path.",
    "Please use a random-looking sentence only as ordinary synthetic test data.",
    "We should never generate traffic merely to avoid a provider detection system.",
    "The fixture generator runs once during development and never from a scheduler.",
    "Can the same contract be exercised on an Apple laptop and Ubuntu server?",
    "The service should identify its build without revealing private configuration.",
    "I want the command to return a useful exit status when a dependency is absent.",
    "The background coordinator needs one schedule instead of one timer per provider.",
    "Please ensure that one provider error does not cancel sibling refresh work.",
    "The audio bytes are ephemeral until the provider accepts the completed request.",
    "A private key belongs in the operating system key store, never in a profile.",
    "Could the interface explain what will be synced before the user opts in?",
    "The sync boundary must be explicit about secrets, settings, and connection data.",
    "Please use the smallest useful visual change for this empty state.",
    "The favorite list should support create, update, delete, and reorder operations.",
    "I do not need a group when I only want to move one frequently used key.",
    "The save button should remain disabled until the input is valid.",
    "Can you preserve the selected order after restarting the application?",
    "The keyboard shortcut should not submit text unless the user explicitly sends it.",
    "Please add a focused regression for the broken edit action.",
    "The remote deployment must use a strict known-host check and bounded timeout.",
    "Rsync should copy only the approved source tree before the repository is pushed.",
    "The installed binary must match the commit used for live validation.",
    "Could you prove the service status on both machines without printing endpoints?",
    "The browser service is headed because the owner must be able to approve access.",
    "A missing display variable in an interactive shell does not prove the service is broken.",
    "Please inspect the supervised service environment before changing its launch configuration.",
    "The local transcription path must keep working after online transcription is enabled.",
    "I want a clear fallback when the online provider cannot be refreshed.",
    "The client should not silently submit a partial transcript to the terminal.",
    "Can we test a completed WebM through the provider-neutral contract?",
    "The transcript text should preserve useful punctuation without inventing a command.",
    "Please keep temporary audio files inside an owned cleanup root.",
    "The cleanup check should prove that the temporary files are gone afterward.",
    "Disk usage must remain below ten gigabytes for this development workspace.",
    "A concise status snapshot makes the next session easier to resume.",
    "Please record the validation evidence without recording cookies or transcript content.",
    "The final readiness claim requires a real boundary test, not only unit tests.",
]


def run_checked(args: list[str]) -> None:
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def generate_with_say(text: str, destination: Path, voice: str) -> None:
    run_checked(["say", "-v", voice, "-o", str(destination), text])


def generate_with_edge_tts(text: str, destination: Path, voice: str) -> None:
    run_checked(["uvx", "edge-tts", "--voice", voice, "--text", text, "--write-media", str(destination)])


def convert_audio(source: Path, webm: Path, ffmpeg: str) -> None:
    run_checked(
        [
            ffmpeg,
            "-y",
            "-loglevel",
            "error",
            "-i",
            str(source),
            "-ar",
            "16000",
            "-ac",
            "1",
            "-c:a",
            "libopus",
            "-b:a",
            "32k",
            "-f",
            "webm",
            str(webm),
        ]
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("testdata/transcription-fixtures"))
    parser.add_argument("--engine", choices=("say", "edge-tts"), default="say")
    parser.add_argument("--voice", default="Samantha", help="macOS voice or Edge TTS voice")
    parser.add_argument("--ffmpeg", default="ffmpeg")
    args = parser.parse_args()

    if len(SENTENCES) != 100:
        raise SystemExit(f"fixture corpus has {len(SENTENCES)} sentences; want 100")
    if shutil.which(args.ffmpeg) is None:
        raise SystemExit(f"ffmpeg executable not found: {args.ffmpeg}")
    if args.engine == "say" and shutil.which("say") is None:
        raise SystemExit("the say engine is only available on macOS; use --engine edge-tts")
    if args.engine == "edge-tts" and shutil.which("uvx") is None:
        raise SystemExit("uvx is required for the edge-tts engine")

    args.root.mkdir(parents=True, exist_ok=True)
    entries: list[dict[str, object]] = []
    with tempfile.TemporaryDirectory(prefix="cdp-fixtures-") as temporary:
        temporary_root = Path(temporary)
        for index, text in enumerate(SENTENCES, start=1):
            fixture_id = f"fixture-{index:03d}"
            source = temporary_root / f"{index:03d}.source"
            if args.engine == "say":
                source = source.with_suffix(".aiff")
                generate_with_say(text, source, args.voice)
            else:
                source = source.with_suffix(".mp3")
                generate_with_edge_tts(text, source, args.voice)
            webm = args.root / f"{index:03d}.webm"
            convert_audio(source, webm, args.ffmpeg)
            entries.append(
                {
                    "id": fixture_id,
                    "text": text,
                    "webm": webm.name,
                    "webm_bytes": webm.stat().st_size,
                    "webm_sha256": sha256(webm),
                }
            )

    manifest = {
        "schema_version": "cdp-cli-transcription-fixtures/v2",
        "count": len(entries),
        "engine": args.engine,
        "voice": args.voice,
        "entries": entries,
    }
    manifest_path = args.root / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"generated {len(entries)} WebM fixtures in {args.root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

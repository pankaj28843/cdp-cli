# Synthetic transcription fixtures

This directory contains exactly 100 checked-in WebM fixtures. Each entry has
one small mono 16 kHz Opus WebM file, plus the sentence used to generate it
and its hash in `manifest.json`.

The sentences are synthetic test data. They are useful for exercising file
validation, format handling, provider-neutral request routing, cleanup, and
bounded live smoke tests. They are not expected transcripts for a particular
provider: speech recognition output can vary by model and provider.

The generator is intentionally one-shot. It must not be scheduled as a
keepalive, health probe, or provider-detection workaround. Generate the corpus
on a development machine with:

```sh
python3 scripts/generate_transcription_fixtures.py
```

The default macOS `say` engine avoids a network dependency. The optional
`--engine edge-tts --voice <voice>` mode is available when a specific synthetic
Edge voice is useful, but its network service should likewise be used only for
bounded fixture generation.

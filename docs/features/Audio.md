# Audio

Captures system audio output (and optionally microphone input) so operators can listen in for diagnostics.

## Features
- Streams the current playback or mic channel to the controller.
- Adjusts capture volume or device selection when the platform allows.
- Buffers audio so brief network blips do not drop significant data.

## Basic Workflow
1. Enable the Audio panel for a client to request a capture session.
2. The agent begins forwarding audio packets while the UI plays them.
3. Disable the session once listening is no longer necessary.

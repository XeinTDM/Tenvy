# Remote Desktop

Streams the full desktop and relays keyboard/mouse control through the Remote Desktop Engine plugin as part of the core control module.

## Features
- Captures high-fidelity frames (multi-monitor aware) for a faithful view of the host.
- Batches pointer and keyboard events into normalized bursts that the agent injects.
- Accepts runtime tuning of quality, encoder hints, monitors, and transport preferences.
- Validates the Remote Desktop Engine plugin version before starting so the controller always meets requirements.

## Basic Workflow
1. Start a session from the Remote Desktop workspace; the server verifies plugin availability.
2. The agent begins streaming the display while the UI renders the frames.
3. Input from the controller and any setting updates are sent to the agent during the session.
4. End the session when done to stop frame capture and input injection.

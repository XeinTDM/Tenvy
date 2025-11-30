# Keylogger

Streams keystrokes in standard or offline mode, letting operators monitor typing even when briefly disconnected.

## Features
- Records typed keys with timestamps and modifier states.
- Buffers events locally in offline mode until the controller reconnects.
- Sends batches of keystrokes to the controller for replay or analysis.

## Basic Workflow
1. Choose the preferred keylogger mode and start a session.
2. The agent streams keystroke batches back to the controller.
3. Stop the session when enough data has been collected.

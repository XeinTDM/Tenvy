# App VNC

Provides an HVNC-style stream that targets a single application window rather than the entire desktop.

## Features
- Lets operators pick a specific process or window to mirror.
- Streams only the chosen application so unrelated monitors remain hidden.

## Basic Workflow
1. Launch App VNC for a client and pick the target window or process.
2. The agent captures the window and delivers encoded frames to the controller.
3. Operators interact with the window via the streamed input channel.
4. Stop App VNC to release the capture hook and end the session.

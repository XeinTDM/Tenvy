# Trigger Monitor

Requests the agent to refresh its display list so streaming modules stay in sync with current hardware.

## Features
- Re-enumerates attached monitors and updates the reported list.
- Useful after display adapters change or virtual displays shift.
- Feeds updated monitor data into Remote Desktop and App VNC selections.

## Basic Workflow
1. Invoke Trigger Monitor when display metadata looks stale.
2. The agent re-enumerates monitors and uploads the refreshed list.
3. Use the updated data when configuring streams.

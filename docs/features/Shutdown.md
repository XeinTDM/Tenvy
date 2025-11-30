# Shutdown

Issues a shutdown request so the remote host powers off cleanly after maintenance.

## Features
- Initiates the OS shutdown sequence remotely.
- Allows specifying graceful or forced behavior when supported.
- Logs the action for auditing later.

## Basic Workflow
1. Trigger Shutdown from the power menu.
2. The agent executes the shutdown path and goes offline.
3. Wait for the controller to mark the client as offline before leaving.

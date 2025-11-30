# Disconnect

Terminates the live control link while leaving the agent registered for later reconnection.

## Features
- Stops active streams and command queues without uninstalling the agent.
- Keeps metadata intact so the agent can reconnect later.
- Clears transient sessions to conserve resources.

## Basic Workflow
1. Issue the Disconnect command from the client toolbar.
2. The agent tears down live channels but stays online.
3. Wait for the controller to reflect the idle state before leaving.

# Reconnect

Commands the agent to re-establish its control link after transient network hiccups.

## Features
- Attempts to revive the transport without reinstalling the agent.
- Keeps the agent registered while only the live session restarts.
- Useful when brief packet loss disrupts the console connection.

## Basic Workflow
1. Trigger Reconnect from the client toolbar.
2. The agent reopens communication channels to the controller.
3. Resume any pending workflows once connectivity returns.

# Sleep

Puts the host into a low-power sleep or suspend state for energy management.

## Features
- Triggers the OS suspend mechanism remotely.
- Keeps the agent configured to wake for the next session.
- Useful when brief downtime is acceptable.

## Basic Workflow
1. Invoke Sleep from the power controls.
2. The agent suspends the system via OS APIs.
3. Wake the host manually or through scheduled actions when needed.

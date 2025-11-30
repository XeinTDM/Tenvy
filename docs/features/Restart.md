# Restart

Commands a reboot so services restart or new configuration takes effect.

## Features
- Performs a full system reboot remotely.
- Ensures control services restart automatically when the OS returns.
- Logs the request along with reconnect activity for audits.

## Basic Workflow
1. Select Restart from the power options.
2. The agent reboots and rejoins the controller when ready.
3. Resume operations once the agent reconnects.

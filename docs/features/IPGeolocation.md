# IP Geolocation

Resolves the agent public IP into geographic metadata for situational awareness.

## Features
- Queries a geolocation service to map IP to country, city, and ASN.
- Caches recent lookups to avoid repeated requests.
- Displays the resolved data alongside the agent record for quick reference.

## Basic Workflow
1. Open the IP Geolocation tool for an agent.
2. The controller requests a lookup for the current public IP.
3. Review the location metadata before proceeding with operations.

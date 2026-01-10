# Remote Desktop Engine IPC Contract

The remote desktop engine plugin is now hosted out-of-process and communicates
with the agent over a **MessagePack** protocol carried across the
plugin's stdin/stdout streams. Each message is an object containing an `id`
field, a `method` string, and an optional `params` payload. Responses reuse the
identifier and either provide a `result` object or an `error` payload with a
human-readable message.

## Supported Methods

| Method          | Direction | Payload                                      | Description |
|-----------------|-----------|----------------------------------------------|-------------|
| `configure`     | Agent → Plugin | `configEnvelope` (engine.Config minus logger/client) | Applies the latest controller configuration. The plugin is responsible for instantiating its own HTTP client using the provided timeout. |
| `startSession`  | Agent → Plugin | `RemoteDesktopCommandPayload`               | Begins a streaming session. |
| `updateSession` | Agent → Plugin | `RemoteDesktopCommandPayload`               | Applies runtime session changes (quality, encoder hints, etc.). |
| `handleInput`   | Agent → Plugin | `RemoteDesktopCommandPayload`               | Forwards aggregated input events to the active session. |
| `deliverFrame`  | Agent → Plugin | `RemoteDesktopFramePacket`                  | Sends captured frame data for transport to the controller. |
| `stopSession`   | Agent → Plugin | `{ "sessionId": string }`                  | Terminates the active session. |
| `shutdown`      | Agent → Plugin | _none_                                      | Signals graceful shutdown; the plugin must flush state, respond, and exit. |

The plugin must respond to each request with a success result (`{"status":"ok"}`)
or an error object (`{"error":{"message":"..."}}`). Any log output should be
written to stderr to avoid interfering with the IPC channel.

## Error Handling

If the agent detects that the plugin process exited unexpectedly, it records
the exit status using the plugin manager and surfaces the buffered stderr output
in the agent logs. Subsequent engine calls will restart the plugin. Conversely,
the plugin should treat malformed requests as fatal and return an error so the
agent can propagate the failure to the controller.

## Testing

Integration coverage lives under `internal/agent/remote_desktop_ipc_test.go` and
uses a fake plugin binary that exercises the full MessagePack pipeline. Any change to
this protocol should update the test fixtures and documentation to avoid
regressions.

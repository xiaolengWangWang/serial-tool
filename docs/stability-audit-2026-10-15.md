# Desktop stability audit

Audit run on 2026-10-16 against `codex/android-stability-design`, after commit `61da9b3`.

## Executed checks

| Check | Result | Duration | Evidence |
|---|---|---:|---|
| `go test -count=1 ./...` | Pass | 26.3 s | Linux, macOS, core, AI attachment, and docs packages passed. |
| `go vet ./...` | Pass | 0.5 s | No diagnostics. |
| `go test ./core -run 'TestTCPClientAutoReconnect|TestUDPExplicitTargetAndPeerMetadata|TestStoreWritersAcrossDateRotation|TestConcurrent|TestTCPConnectionIdentityRoutingAndIsolation' -count=1` | Pass | 4.0 s | Loopback TCP reconnect, UDP peer metadata, TCP connection isolation, and SQLite rotation checks passed. No test matched `TestConcurrent`. |
| `go test -race ./core -run 'TestStale|TestManualDisconnect|TestTCPClientAutoReconnect' -count=1` | Pass | 7.2 s | Stale-session, manual disconnect, and reconnect tests passed under the race detector. |

## Lifecycle findings

- TCP passive disconnect and reconnect behavior passed the existing loopback tests.
- TCP server connection identity and isolation passed the existing loopback test.
- UDP target and peer metadata passed the existing loopback test.
- SQLite writers across date rotation passed the existing storage test.
- A regression test reproduced an asynchronous TCP disconnect event entering a replacement session when the log callback paused the old reader. Commit `61da9b3` binds asynchronous TCP, UDP, serial, and listener events to their originating epoch; the regression and targeted race checks pass.
- Manual TCP disconnect still records its event in the ending session and passed the local disconnect test.
- The targeted audit command contained no concurrent-send test with a matching name. The full suite passed, but this audit did not establish a dedicated stress result for concurrent sends.

## Hardware and environment limits

No physical serial adapter, Bluetooth device, or target network appliance was exercised. Windows was not available for this run. These results cover automated tests and local loopback only; they do not establish reliability for specific USB/serial drivers, radio conditions, or remote device firmware.

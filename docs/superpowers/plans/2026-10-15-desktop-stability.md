# Desktop Stability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validate the existing desktop connection lifecycle and prevent a stale disconnect event from being stored in a replacement session.

**Architecture:** Keep the current Go engine and storage schema. An event emitted by an asynchronous reader must carry the reader's connection epoch into the existing `emitPacket` epoch guard; the user initiated disconnect retains its current-session event path.

**Tech Stack:** Go 1.26; existing `core` tests and SQLite store.

**Spec:** `docs/superpowers/specs/2026-10-15-stability-android-design.md`

## Global Constraints

- Keep Windows, macOS, and Linux public behavior and data format compatible with v0.11.6.
- Repair reproducible lifecycle defects in the common engine; do not restructure the engine speculatively.
- Never allow a prior connection's record to enter a new SQLite session.

## Review Focus

- A TCP client closes while a new session starts: the old close event must not enter the new session (Task 1).
- A UDP socket fails as the user reconnects: the old error event must not enter the new session (Task 1).
- A serial read fails after replacement: the old event must not enter the new session (Task 1).
- The user disconnects normally: its event must still be stored in the session being ended (Task 1).
- A TCP reconnect races with manual disconnect: no connection may survive as the active session (Task 2).

---

### Task 1: Bind asynchronous disconnect events to their original session

**Files:**
- Modify: `core/engine.go`
- Test: `core/disconnect_test.go`

**Interfaces:**
- Produce: `(*Engine).recordEventAt(epoch uint64, message string)`; keep `recordEvent(message string)` for the synchronous manual-disconnect path.
- Use existing `recordPacketAt(epoch, Packet)` and `emitPacket(Packet)` epoch guard.

- [ ] **Step 1: Write failing tests** `TestStaleTCPDisconnectEventCannotEnterReplacementSession`, `TestStaleUDPErrorCannotEnterReplacementSession`, and `TestStaleSerialErrorCannotEnterReplacementSession` in `core/disconnect_test.go`. Use an `onLog` callback that blocks on the relevant error line, replace the session while blocked, then release it; query `received_data` for the old event in the new session and assert zero. Keep or add `TestManualDisconnectEventStaysInEndingSession` asserting one local event.
- [ ] **Step 2: Run** `go test ./core -run 'TestStale.*EventCannotEnterReplacementSession|TestManualDisconnectEventStaysInEndingSession' -count=1`; expect at least one stale-event test to fail before the fix.
- [ ] **Step 3: Implement** `recordEventAt` in `core/engine.go`; pass the captured epoch from `acceptLoop`, `readTCP`, `readUDP`, and `readSerial`. Keep `Disconnect` on `recordEvent`, with the current epoch after its increment. Preserve the existing packet source and byte content.
- [ ] **Step 4: Run** the targeted command again, then `go test ./...` and `go test -race ./core -run 'TestStale|TestManualDisconnect|TestTCPClientAutoReconnect' -count=1`; expect all to pass.
- [ ] **Step 5: Commit** only `core/engine.go` and `core/disconnect_test.go` as `fix(core): isolate asynchronous disconnect events by session`.

### Task 2: Complete the stability audit and record reproducible limits

**Files:**
- Create: `docs/stability-audit-2026-10-15.md`
- Modify only if a new reproducible failure is found: its owning `core` file and one regression test.

**Interfaces:**
- Consume: Task 1's epoch-bound event path and existing `core` tests.
- Produce: an audit report with executed command, result, and hardware limitations for each scenario.

- [ ] **Step 1: Run** `go test -count=1 ./...`, `go vet ./...`, and targeted TCP/UDP/storage/race tests; record actual outputs and durations. Do not repeat the full race suite unless a concrete failure requires it.
- [ ] **Step 2: Check** connection, passive disconnect, TCP auto-reconnect, concurrent send, UDP peer selection, and SQLite rotation using existing tests and local loopback endpoints. Add one focused failing test only for a newly observed defect, then fix the shared path and rerun it.
- [ ] **Step 3: Write** `docs/stability-audit-2026-10-15.md` with an evidence table and exact untested hardware paths; avoid declaring unsupported device combinations fixed.
- [ ] **Step 4: Run** `git diff --check` and the relevant checks for any code changed in Step 2; expect zero errors and a clean test run.
- [ ] **Step 5: Commit** the audit report and any focused repair as `docs: record desktop stability audit` (use a separate `fix(core): ...` commit first if Step 2 changed code).

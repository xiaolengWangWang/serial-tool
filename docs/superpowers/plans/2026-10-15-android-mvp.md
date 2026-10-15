# Android MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a signed, self-installable CommBox APK with convenient phone/tablet USB, Bluetooth, and TCP/UDP debugging, Modbus templates, local records, and in-app updates.

**Architecture:** A native Kotlin/Compose app keeps one active `SessionController` and a small `Connection` interface. Android-specific transport classes handle permissions and byte I/O; pure Kotlin session and Modbus code stays JVM-testable. The desktop Go engine stays in place, with protocol parity checked by fixed vectors.

**Tech Stack:** Android Kotlin, Jetpack Compose, Android USB Host/Bluetooth/socket/SQLite APIs, `usb-serial-for-android`; Gradle wrapper 9.6.0, AGP 9.4.0, Kotlin/Compose compiler 2.4.10, Compose BOM 2026.09.00, JDK 17, `compileSdk 37`, `targetSdk 36`, `minSdk 26`.

**Spec:** `docs/superpowers/specs/2026-10-15-stability-android-design.md`

## Global Constraints

- Package/application ID: `com.xiaoleng.commbox`; `versionName` follows the release tag and `versionCode` only increases.
- Phone and tablet, Chinese labels, one active session; basic capture remains available without account or network access except the chosen transport and update check.
- Preserve raw RX/TX bytes and expose the actual bytes before a Modbus send. TCP reads are chunks, not protocol frames.
- USB permission and Bluetooth runtime permissions are requested when the relevant connection is selected.
- BLE supports Nordic UART Service and a configurable GATT UART profile; it cannot promise arbitrary private BLE protocols.
- Only a checksum-verified, same-package, same-key APK may enter the system installer. The release key and password never enter git.

## File Map

- `apps/android/` owns the Gradle project, manifest, Compose screen, Android transport adapters, local store, and updater.
- `session/SessionController.kt` owns active-session state and stale-event rejection; `session/Connection.kt` defines configs and the byte transport contract.
- `transport/NetworkConnections.kt`, `UsbConnection.kt`, `SppConnection.kt`, and `BleConnection.kt` each own one platform connection family; `AndroidConnections.kt` dispatches configs to them.
- `protocol/Modbus.kt` owns frame generation and validation; `data/LocalStore.kt` owns profiles and bounded packet history; `update/ApkUpdater.kt` owns release selection, download verification, and installer handoff.
- `.github/workflows/release.yml` adds an Android build gate to the existing release flow; `scripts/publish-gitee.ps1` already mirrors every release asset.

## Review Focus

- A late USB/Bluetooth/network callback after disconnect must not appear in the new session (Task 1).
- Rapid TCP reconnect, failed host lookup, or UDP socket close must return the UI to a truthful state (Task 2).
- USB denial, Bluetooth denial/off state, or unplugging must show a clear recoverable message (Tasks 4–6).
- A BLE write larger than the negotiated payload must be split in order without losing bytes (Task 6).
- A missing checksum, hostile download URL, stale version, or interrupted APK download must never launch installation (Task 9).

---

### Task 1: Bootstrap app and single-session controller

**Files:** Create `apps/android/settings.gradle.kts`, `build.gradle.kts`, `gradle.properties`, `gradlew`, `gradlew.bat`, `gradle/wrapper/*`, `app/build.gradle.kts`, `app/src/main/AndroidManifest.xml`, `app/src/main/java/com/xiaoleng/commbox/MainActivity.kt`, `session/Connection.kt`, `session/SessionController.kt`; test `app/src/test/java/com/xiaoleng/commbox/session/SessionControllerTest.kt`.

**Interfaces:** `interface ConnectionConfig`; `interface Connection { suspend fun send(bytes: ByteArray); suspend fun close() }`; `interface ConnectionFactory { suspend fun open(config: ConnectionConfig, onBytes: (ByteArray, String?) -> Unit, onClosed: (Throwable?) -> Unit): Connection }`; `SessionController(factory: ConnectionFactory, scope: CoroutineScope)` exposes `connect(config)`, `send(bytes)`, `disconnect()`, and `StateFlow<SessionState>`. `PacketEvent(timestampMs: Long, direction: Direction, transport: TransportKind, bytes: ByteArray, peer: String?)` is the common record; non-UDP transports pass `null` for peer.

- [ ] **Step 1: Provision** JDK 17 and Android SDK under a user-owned path (or use an existing installation), then create the pinned Gradle wrapper and minimal Android app. Record exact SDK path only in local configuration, excluded from git.
- [ ] **Step 2: Write failing tests** `lateCallbackIgnoredAfterDisconnect`, `secondConnectClosesFirst`, and `failedOpenReturnsDisconnectedWithError` using a fake `ConnectionFactory`.
- [ ] **Step 3: Run** `cd apps/android && ./gradlew :app:testDebugUnitTest --tests '*SessionControllerTest'`; expect the new tests to fail before the controller exists.
- [ ] **Step 4: Implement** the interfaces and controller with a monotonically increasing session token; rerun the targeted tests and `:app:assembleDebug`, expecting success.
- [ ] **Step 5: Commit** the runnable skeleton and controller as `feat(android): add app and session lifecycle`.

### Task 2: TCP and UDP byte connections

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/transport/NetworkConnections.kt` and `AndroidConnections.kt`; test `apps/android/app/src/test/java/com/xiaoleng/commbox/transport/NetworkConnectionsTest.kt`; modify `session/Connection.kt`.

**Interfaces:** `TcpConfig(host: String, port: Int, listen: Boolean, autoReconnect: Boolean)` and `UdpConfig(host: String, port: Int, listen: Boolean)` implement `ConnectionConfig`; `NetworkConnectionFactory` implements `ConnectionFactory` for those configs; `AndroidConnections` dispatches configs to the appropriate factory.

- [ ] **Step 1: Write failing loopback tests** for TCP client/server round-trip, UDP target/reply, invalid port/host, and disconnect during a reconnect attempt; assert no stale bytes after session replacement.
- [ ] **Step 2: Run** `./gradlew :app:testDebugUnitTest --tests '*NetworkConnectionsTest'`; expect failures.
- [ ] **Step 3: Implement** blocking socket I/O on dedicated coroutines, a bounded initial TCP dial, explicit close, and optional TCP-client reconnect with cancellation. Treat TCP reads as chunks; preserve each UDP datagram's peer.
- [ ] **Step 4: Rerun** the network tests and `:app:assembleDebug`; expect pass and no main-thread network access.
- [ ] **Step 5: Commit** as `feat(android): add TCP and UDP connections`.

### Task 3: Phone and tablet debugging screen

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/ui/CommBoxScreen.kt`; modify `MainActivity.kt`; test `apps/android/app/src/androidTest/java/com/xiaoleng/commbox/ui/CommBoxScreenTest.kt`.

**Interfaces:** `CommBoxScreen(state: SessionState, onConnect: (ConnectionConfig) -> Unit, onSend: (ByteArray) -> Unit, onDisconnect: () -> Unit)`; connection panel supplies TCP/UDP fields now and extends to hardware transports in later tasks.

- [ ] **Step 1: Write UI assertions** for visible connection state, HEX/text send validation, send disabled while disconnected, and a wide layout with settings beside the capture list at width `>=600dp`.
- [ ] **Step 2: Run** connected Android tests on an emulator; expect failures before the screen exists. If no emulator is available, build the test APK and record this as a device-validation gap.
- [ ] **Step 3: Implement** one Compose screen with a persistent send bar, a connection sheet on narrow screens, a settings pane on wide screens, Chinese error/permission copy, and bounded visible history. Hold the controller in an Activity ViewModel so rotation does not drop the session.
- [ ] **Step 4: Rerun** UI tests or the documented build gate, and manually exchange bytes with a loopback TCP/UDP endpoint.
- [ ] **Step 5: Commit** as `feat(android): add adaptive debugging screen`.

### Task 4: USB OTG serial

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/transport/UsbConnection.kt`; modify `Connection.kt`, `AndroidConnections.kt`, `CommBoxScreen.kt`, `AndroidManifest.xml`, and `app/build.gradle.kts`; test `app/src/androidTest/java/com/xiaoleng/commbox/transport/UsbConnectionTest.kt`.

**Interfaces:** `UsbConfig(deviceId: Int, port: Int, baud: Int, dataBits: Int, stopBits: Int, parity: Int)`; `UsbConnectionFactory` discovers supported ports and implements `ConnectionFactory` after USB Host permission succeeds.

- [ ] **Step 1: Write USB tests** for invalid serial parameters, denied permission, and detach cleanup; run the instrumented test with a fake permission/device seam and confirm failure.
- [ ] **Step 2: Add** `usb-serial-for-android` 3.11.0 and device selection showing model/VID/PID/port; request USB permission only on connect.
- [ ] **Step 3: Implement** open/read/write/close and detach handling; preserve raw bytes and do no disk writes on the read callback.
- [ ] **Step 4: Run** `:app:testDebugUnitTest` and `:app:assembleDebug`; on a supported adapter verify RX/TX and unplug behavior, recording the device identity. If hardware is unavailable, record unverified physical I/O.
- [ ] **Step 5: Commit** as `feat(android): add USB serial debugging`.

### Task 5: Classic Bluetooth SPP

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/transport/SppConnection.kt`; modify `Connection.kt`, `AndroidConnections.kt`, `CommBoxScreen.kt`, and `AndroidManifest.xml`; test `app/src/test/java/com/xiaoleng/commbox/transport/SppConnectionTest.kt`.

**Interfaces:** `SppConfig(address: String)`; `SppConnectionFactory` lists paired devices, scans when requested, and opens an RFCOMM `BluetoothSocket` using the standard SPP UUID.

- [ ] **Step 1: Write a small permission/device-selection test** verifying denied `BLUETOOTH_CONNECT`/`BLUETOOTH_SCAN` prevents opening and that a failed socket returns a user-readable error.
- [ ] **Step 2: Run** that test; expect failure before the adapter exists.
- [ ] **Step 3: Implement** Android-version-aware permission requests, paired-device selection and optional scan, blocking connect off the main thread, stream RX/TX, and close on radio-off/disconnect.
- [ ] **Step 4: Run** `:app:testDebugUnitTest` and `:app:assembleDebug`; verify round-trip and radio-off behavior with one SPP device if available.
- [ ] **Step 5: Commit** as `feat(android): add classic Bluetooth serial`.

### Task 6: BLE UART

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/transport/BleConnection.kt`; modify `Connection.kt`, `AndroidConnections.kt`, `CommBoxScreen.kt`, and `AndroidManifest.xml`; test `app/src/test/java/com/xiaoleng/commbox/transport/BleChunksTest.kt`.

**Interfaces:** `BleConfig(address: String, serviceUuid: UUID, writeUuid: UUID, notifyUuid: UUID, writeWithResponse: Boolean)`; `BleConnectionFactory` discovers GATT services and notifications; `splitBleWrite(bytes: ByteArray, payloadSize: Int): List<ByteArray>` is pure Kotlin.

- [ ] **Step 1: Write failing tests** for NUS default UUIDs, user UUID validation, payload sizes `1`, `20`, and `mtu-3`, ordered split/reassembly, and disconnect during a queued write.
- [ ] **Step 2: Run** `:app:testDebugUnitTest --tests '*BleChunksTest'`; expect failures.
- [ ] **Step 3: Implement** scan/connect/service discovery, optional pairing prompts, notification subscription, one-at-a-time writes, MTU-aware splitting, and close cleanup. Expose a NUS preset and editable UUID fields.
- [ ] **Step 4: Rerun** tests and `:app:assembleDebug`; verify notifications and a payload larger than one BLE write with a compatible physical device if available.
- [ ] **Step 5: Commit** as `feat(android): add configurable BLE UART`.

### Task 7: Modbus send templates

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/protocol/Modbus.kt`; modify `CommBoxScreen.kt`; test `app/src/test/java/com/xiaoleng/commbox/protocol/ModbusTest.kt`.

**Interfaces:** `buildRtu(unit: Int, function: Int, address: Int, values: List<Int>, quantity: Int): ByteArray`; `buildTcp(transaction: Int, unit: Int, function: Int, address: Int, values: List<Int>, quantity: Int): ByteArray`. Support functions `0x03`, `0x04`, `0x06`, and `0x10`.

- [ ] **Step 1: Write failing fixed-vector tests**: RTU read one register `01 03 00 00 00 01 84 0A`, TCP read one register `00 01 00 00 00 06 01 03 00 00 00 01`, write-single and write-multiple vectors, plus bounds for unit/address/quantity/value.
- [ ] **Step 2: Run** `:app:testDebugUnitTest --tests '*ModbusTest'`; expect failures.
- [ ] **Step 3: Implement** RTU CRC/MBAP encoding and an editable template sheet that previews HEX before sending. Offer RTU only for USB/SPP/BLE and Modbus TCP only for TCP; keep UDP as raw send.
- [ ] **Step 4: Rerun** vector tests and manually compare one generated frame with desktop `core.CRC16Modbus`/analysis output.
- [ ] **Step 5: Commit** as `feat(android): add Modbus request templates`.

### Task 8: Profiles, bounded local history, and export

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/data/LocalStore.kt`; modify `SessionController.kt`, `CommBoxScreen.kt`; test `app/src/androidTest/java/com/xiaoleng/commbox/data/LocalStoreTest.kt`.

**Interfaces:** `LocalStore.saveProfile(config: ConnectionConfig)`, `recentProfiles(limit: Int): List<ConnectionConfig>`, `record(event: PacketEvent)`, `recentPackets(limit: Int): List<PacketEvent>`, and `export(uri: Uri)`; private SQLite storage keeps at most 10,000 records and 32 MiB of raw payload total.

- [ ] **Step 1: Write a failing store test** that saves binary `00 FF`, reopens the DB, checks unchanged bytes and profile order, then exceeds both retention limits and verifies oldest-first eviction.
- [ ] **Step 2: Run** the instrumented store test; expect failure before the store exists.
- [ ] **Step 3: Implement** `SQLiteOpenHelper`, one background writer, bound enforcement, profile reuse, and export through Android's document picker. Surface write errors without blocking RX/TX.
- [ ] **Step 4: Rerun** store tests and `:app:assembleDebug`; confirm export can be reopened as text with HEX bytes and timestamps.
- [ ] **Step 5: Commit** as `feat(android): save profiles and bounded capture history`.

### Task 9: Verified APK update and signed release

**Files:** Create `apps/android/app/src/main/java/com/xiaoleng/commbox/update/ApkUpdater.kt`; modify `MainActivity.kt`, `CommBoxScreen.kt`, `AndroidManifest.xml`, `app/build.gradle.kts`, `.github/workflows/release.yml`; test `app/src/test/java/com/xiaoleng/commbox/update/ApkUpdaterTest.kt`.

**Interfaces:** `interface UpdateHttp { suspend fun getJson(url: String): String; suspend fun download(url: String, dest: File) }`; `ApkUpdater(cacheDir: File, http: UpdateHttp, install: (File) -> Unit)` exposes `check(currentVersion: String): UpdateInfo?`, `downloadVerified(info: UpdateInfo): File`, and `launchInstaller(file: File)`; release asset name `CommBox-<version>-Android.apk`.

- [ ] **Step 1: Write failing update tests** for Gitee/GitHub release selection, absent SHA256, SHA mismatch, disallowed host, old/equal version, and interrupted download. Assert none of the unsafe cases reaches `launchInstaller`.
- [ ] **Step 2: Run** `:app:testDebugUnitTest --tests '*ApkUpdaterTest'`; expect failures.
- [ ] **Step 3: Implement** bounded HTTPS release checks, mirror fallback, SHA256-verified private-cache download, APK package/signing-certificate comparison, `FileProvider` installer intent, and Android's install-source permission handoff. Do not log download tokens, keys, or APK contents.
- [ ] **Step 4: Add** a separate Android CI job. Build debug APK without secrets; when `publish=true`, require release signing secrets, sign the APK with the persistent key, upload it to the existing release assets, and include it in the release SHA256 list. Use the same application ID and increasing `versionCode`; create a local user-owned release key if none exists and record its fingerprint outside git.
- [ ] **Step 5: Run** unit tests, `:app:assembleDebug`, signed `:app:assembleRelease`, and an old-to-new signed APK install test on emulator/device. Commit as `feat(android): add verified in-app APK updates`.

### Task 10: Final validation and handoff

**Files:** Create `apps/android/README.md`; modify root `README.md` and the release workflow only where verified builds require it.

**Interfaces:** Consume Tasks 1–9; produce installation, supported-device, BLE UUID, update, and signing-key-backup instructions.

- [ ] **Step 1: Run** `go test ./...`, `go vet ./...`, `cd apps/android && ./gradlew :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease`; capture exact results.
- [ ] **Step 2: Run** Android connected tests and phone/tablet UI checks where an emulator or device exists. Try USB, SPP, and BLE on available physical hardware; list untested hardware exactly.
- [ ] **Step 3: Verify** APK package ID, version code/name, signing certificate fingerprint, SHA256, and a same-key upgrade; ensure the release key remains outside git and is backed up.
- [ ] **Step 4: Document** build/install steps, device support and limits, Modbus address convention, APK update flow, and test evidence in `apps/android/README.md`; add a root README link.
- [ ] **Step 5: Run** `git diff --check`, inspect `git status`, and commit docs as `docs: add Android build and validation guide`.
